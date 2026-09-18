# Work sync server

The server behind `work.jevido.app`. It stores an append-only log of operations
per workspace and hands that log back in order. That is almost the whole job:
it will also merge the log for you and hand back the resulting document, for the
one client that cannot merge it itself.

It holds **no Anthropic credentials and never runs `claude`**. Agents run on the
desktop, against your own signed-in CLI; what reaches the server is the result —
ops describing what changed. A compromised server leaks your task board, not
your Claude account.

## The contract

This document is the API contract. The desktop app and the web viewer are both
written against it, so **the shapes below are fixed**. Anything added later is
added as a new field or a new endpoint, never as a change to an existing one.

- Base URL: `https://work.jevido.app`
- All request and response bodies are JSON, `Content-Type: application/json`.
- All times are RFC 3339 in UTC.
- Unknown fields in a request body are ignored, so a newer client can talk to an
  older server. Clients must ignore unknown response fields for the same reason.

### Authentication

Every workspace has two keys, handed out once when the workspace is created:

| Key | Prefix | May |
| --- | --- | --- |
| Write key | `wk_` | append ops, read the log, read workspace metadata |
| Read key | `rk_` | read the log, read workspace metadata |

Send one as a bearer token:

```
Authorization: Bearer wk_3f9a1c…
```

The key identifies the workspace as well as the caller, so no workspace ID
appears in any path. A read key is what you paste into the web viewer: it can
see the board and cannot change it.

A key goes in the header and **nowhere else**. There is no `?key=` parameter on
any endpoint and there will not be one: a key in a URL is a key in this server's
access log, in every proxy's log between here and you, and in the `Referer` of
anything the page links to. A viewer link carries the read key in the URL
*fragment* — `https://work.jevido.app/#rk_…` — which the browser keeps to itself
and never sends. The page reads it and puts it in the header.

Keys are stored as SHA-256 hashes. The server cannot show you a key again after
it is created — losing one means rotating it.

There are no accounts. A key is the whole identity model: no sign-up, no
sessions, no email address, and no attribution anywhere in the schema — no
author, assignee or last-edited-by column exists to be filled in. An op's
`actor` is a replica label used to break a clock tie, and nothing reads it as a
person.

### Errors

Every non-2xx response has the same body:

```json
{ "error": { "code": "unauthorized", "message": "unknown or expired key" } }
```

| Status | `code` | Means |
| --- | --- | --- |
| 400 | `bad_request` | malformed JSON, or a field that fails validation |
| 401 | `unauthorized` | missing, malformed, unknown or revoked key |
| 403 | `forbidden` | valid key without the rights for this endpoint (a read key posting ops) |
| 404 | `not_found` | no such route |
| 405 | `method_not_allowed` | right path, wrong method |
| 409 | `node_deleted` | an op targets a node already deleted; see `POST /v1/ops` |
| 413 | `too_large` | request body over the limit |
| 429 | `rate_limited` | too many requests; retry after `Retry-After` seconds |
| 500 | `internal` | the server's fault; safe to retry |

`message` is for humans and may change. Branch on `code`, never on `message`.

Rate limiting is per key — 10 requests a second with a burst of 40 — and
`/v1/health` is never limited. A viewer polling every few seconds and a desktop
replica syncing hard are both far underneath that, so hitting a 429 means a
loop, not load. The budget is held in memory, so it is per server instance.

## Endpoints

### `GET /` and everything not under `/v1/`

The web viewer, when `WORK_SITE_DIR` names a directory holding its built files
— which it does in the image, and does not in a local `go run .`. A path that
is not a file in that directory answers `index.html` at 200 rather than a 404,
because those paths belong to the client router and reloading one has to work.

With no site configured the root is what every unknown path is: a 404 with
`{"error":{"code":"not_found",…}}`.

`/v1/…` is registered ahead of the root either way, so an endpoint that does
not exist is always the JSON error and never this page. That matters more than
it sounds: a static handler mounted in front of a JSON API breaks the API, not
the page, by answering 200 and HTML to something that parses JSON.

The viewer holds a **read** key, and reaches these endpoints from the same
origin it was served from. That is not a convenience — the key arrives in a URL
fragment, which only the browser ever sees, so the page and the API it calls
have to be one origin or the key can never reach a request.

### `GET /v1/health`

No auth. Liveness plus a database round-trip.

```json
{ "status": "ok" }
```

Returns 500 with `{"error":{"code":"internal",…}}` if the database is
unreachable.

### `POST /v1/workspaces`

Creates a workspace and returns its keys. **This is the only time the keys are
readable.**

Open by default: a server with no `WORK_SIGNUP_TOKEN` set creates a workspace
for anyone who can reach it, rate limited like every other endpoint. Setting
`WORK_SIGNUP_TOKEN` closes it to whoever holds that token, sent as the bearer;
a request without it, or with the wrong one, is then a 401.

Request:

```json
{ "name": "jevido/work" }
```

`name` is a label for humans, 1–200 characters. It is not unique and not an
identifier.

Response `201 Created`:

```json
{
  "workspace": {
    "id": "ws_9c938701f37f5bae73592c4f27d7790b",
    "name": "jevido/work",
    "head": 0,
    "createdAt": "2026-09-10T12:00:00Z"
  },
  "writeKey": "wk_3f9a1c7e5b2d4a8f6c0e9b3d7a5f1c8e",
  "readKey": "rk_8b4d2f6a0c9e7b5d3a1f8c6e4b2d0a9f"
}
```

Workspace IDs and keys are opaque. They are `ws_`, `wk_` and `rk_` followed by
32 hex characters today, and nothing but the prefix is promised — do not parse
one, derive one, or sort by one.

**These two keys are readable once.** Not as a policy: `workspace_keys` stores
a SHA-256 of each key and nothing else, so after this response the plaintext
exists only wherever the caller put it. There is no endpoint that reads a key
back and there cannot be one. To get another, mint one.

### `POST /v1/keys`

Write key. Issues another key for the same workspace.

```json
{ "access": "read" }
```

`access` is `"read"` or `"write"`. Response `201 Created`:

```json
{
  "key": "rk_8b4d2f6a0c9e7b5d3a1f8c6e4b2d0a9f",
  "access": "read"
}
```

This is what "show me the read key again" resolves to. Somebody asking for it
wants a read-only link to send a colleague, and a fresh read key is that —
identical in every way that matters to the one handed out at creation.

Write access, and not because minting writes to the log. It is because a read
key that could mint a write key would be a read key with write access, one
request later.

**Minting is not rotation.** Every key already in use keeps working; nothing is
revoked. A workspace may hold any number of keys of either kind, and cutting off
every other machine in it would be a startling amount of damage for one button.

### `GET /v1/workspace`

Read or write key. Metadata for the workspace the key belongs to.

```json
{
  "workspace": {
    "id": "ws_9c938701f37f5bae73592c4f27d7790b",
    "name": "jevido/work",
    "head": 4127,
    "createdAt": "2026-09-10T12:00:00Z"
  },
  "access": "write"
}
```

`access` is `"read"` or `"write"` — the viewer uses it to decide whether to show
anything that would write. `head` is the highest sequence number in the log; a
fresh workspace has `head: 0`.

### `POST /v1/ops`

Write key. Appends ops to the log.

Request:

```json
{
  "ops": [
    {
      "id": "01JBQ8Z3K4M5N6P7Q8R9S0T1V2",
      "kind": "set-fields",
      "actor": "desktop-6f2a",
      "clock": 91,
      "node": "n_7f3c",
      "fields": { "title": "Ship the sync server", "status": "doing" }
    }
  ]
}
```

At most 500 ops per request, and 1 MiB of body. An empty `ops` array is a 400 —
a client with nothing to send should not be sending. Ops are appended in the
order given, in one transaction: either every op in the request lands or none
does.

Every op is validated against the same rules the desktop merges by, and one
that fails them takes the whole request down with a 400 naming it. The log
cannot come to hold an op that a replica would later refuse to apply.

Response `200 OK`:

```json
{
  "head": 4128,
  "accepted": [{ "id": "01JBQ8Z3K4M5N6P7Q8R9S0T1V2", "seq": 4128 }],
  "duplicates": []
}
```

- `accepted` — ops written by this request, each with the sequence number it
  got.
- `duplicates` — ops whose `id` was already in the log. **This is not an
  error.** The entry carries the `seq` the op already had, so a client that lost
  the response to a previous attempt can retry the identical request and
  reconcile.
- `head` — the workspace's highest sequence number after this request.

Retrying a request is always safe: op IDs are the idempotency key, so a replay
appends nothing and reports every op as a duplicate.

An op whose `id` matches an existing op with **different content** is rejected
with 400 `bad_request` rather than silently ignored — that is a client bug, not
a retry.

#### What a write is never refused for

**Being behind.** There is no version to be level with and no `If-Match` to
send. A replica can be a thousand ops behind, or a week offline, and everything
in its outbox is accepted and ordered by the `clock` each op carries. The server
is the merge point and the ordering authority, not the source of truth: the
desktop is routinely *ahead* of it, holding edits it has not pushed, and a
server that refused stale writes would make being offline lossy — which is the
one thing the log exists to prevent.

So `head` is not a precondition. It is where your ops landed.

#### The one thing a write is refused for

An op aimed at a node the log has already tombstoned is refused with 409
`node_deleted`. That is not lateness; there is nothing left to write to, and no
later op brings the node back. Retrying the identical request will never
succeed.

Which node has to be alive depends on the kind:

| `kind` | Refused when this node is a tombstone |
| --- | --- |
| `create-node` | `node` — a delete that overtook the create still wins, so the create adds nothing |
| `set-fields` | `node` |
| `move-node` | `node`, and **not** `parent` — moving under a deleted parent is how a node reaches `detached`, which is a state the viewer renders |
| `extract-to-task` | `task`, and **not** `node` — extracting from a node deleted meanwhile still produces the task and still links back, so the tombstone records where its content went |
| `delete-node` | never — deleting what is already deleted is the outcome asked for, and refusing it would leave a client retrying an op it cannot otherwise be rid of |

**Only deletes already in the log count. Deletes inside the same request do
not.** A batch is a set of ops that arrived together, not a sequence played in
array order: an outbox routinely holds a node created, edited and then deleted,
and all of it has to be pushable. Judging a batch against its own interior would
make acceptance depend on the order a client happened to serialise it in, which
is the one thing the log promises not to care about.

A consequence, stated plainly rather than discovered: the same edit can be
accepted in a batch alongside the delete and refused when sent after it. The
document converges either way — a tombstone is in neither `tree` nor
`detached` — but which fields a tombstone carries can differ between a replica
whose edit landed and one whose edit was refused. That content is not rendered
anywhere, and the alternative is an offline replica pushing edits forever at a
node that no longer exists.

The `message` names the op and the node, for a human reading a log. A client
does not parse it: on a 409 it catches up with `GET /v1/ops`, merges, and drops
the ops targeting nodes that are now tombstones — which tells it about every
one of them at once rather than one per refused request.

### `GET /v1/ops`

Read or write key. Returns the log in order.

| Query | Default | Means |
| --- | --- | --- |
| `since` | `0` | return ops with `seq` strictly greater than this |
| `limit` | `1000` | maximum ops to return, capped at `1000` |

`GET /v1/ops` with no parameters starts the whole log from the beginning.

```json
{
  "head": 4128,
  "ops": [
    {
      "seq": 4127,
      "receivedAt": "2026-09-10T12:03:11Z",
      "op": {
        "id": "01JBQ8Z3K4M5N6P7Q8R9S0T1V2",
        "kind": "set-fields",
        "actor": "desktop-6f2a",
        "clock": 91,
        "node": "n_7f3c",
        "fields": { "title": "Ship the sync server", "status": "doing" }
      }
    }
  ],
  "more": true
}
```

`more` is true when there are ops after the last one returned. Page by calling
again with `since` set to the `seq` of the last op you got, until `more` is
false. When `more` is false, the last `seq` you have equals `head`.

Sequence numbers are **gapless**: a workspace's ops are numbered 1, 2, 3, … with
no holes, ever. So a client holding up to `seq` N knows it has the complete
history up to N, and `head - N` is exactly how far behind it is. This is why
there is no cursor type to carry around — the sequence number is the cursor.

**Live updates are polling.** There is no websocket and no SSE stream. Call
`GET /v1/ops?since=<your head>` on an interval; it is a single indexed query
that returns an empty list when there is nothing new.

### `GET /v1/document`

Read or write key. The whole log, merged, as a document.

This is the only endpoint that materialises anything, and it exists for the one
consumer that cannot merge for itself: the web viewer is a browser page and
`packages/ops` is Go. *Applying the log*, below, says why that is an exception
rather than the new normal.

```json
{
  "head": 4128,
  "tree": [
    {
      "id": "n_7f3c",
      "position": "m",
      "fields": { "title": "Ship the sync server", "status": "doing" },
      "children": [
        {
          "id": "n_91ab",
          "parent": "n_7f3c",
          "position": "a",
          "fields": { "title": "Retry the first connect" }
        }
      ]
    }
  ],
  "detached": [
    {
      "id": "n_4d0e",
      "parent": "n_1b55",
      "position": "c",
      "fields": { "title": "Its parent was deleted from another replica" }
    }
  ]
}
```

| Field | Means |
| --- | --- |
| `head` | The sequence number this document is merged through. The same number `GET /v1/workspace` reports, so one request tells a poller both what the document is and whether it is current. |
| `tree` | The live nodes reachable from the roots, each with its `children` nested inside it, siblings in position order. |
| `detached` | The live nodes `tree` cannot reach, ordered by `id`. Flat — never nested, even when one detached node is another's parent. |

`tree` and `detached` are always arrays, never `null`. A node appears in exactly
one of them.

A node carries `id` always, and `parent`, `position` and `fields` when they are
set. `parent` is absent on a root. This is the merge's own output shape, so the
viewer and the desktop app render the same structure from the same code.

**`detached` is not an error case and has to be rendered.** A node lands there
when its parent is a tombstone or sits under one, when its parent is a node no
op has mentioned yet, or when concurrent moves put it in a cycle. The first two
are what an eventually-consistent tree looks like mid-convergence and clear up
when the missing op arrives; the third can outlive convergence. A viewer that
drops `detached` is showing an incomplete workspace and cannot tell that it is.
Showing it as a second list — "not filed" — is showing all of it.

Tombstones are in neither list. There is no `deleted` field in this payload: a
deleted node is absent, not marked. A viewer that needs to say *what* was
deleted reads `GET /v1/ops` and merges for itself.

#### Polling it

The response carries a strong `ETag`. Send it back as `If-None-Match` and an
unchanged document answers `304 Not Modified` with an empty body:

```
GET /v1/document HTTP/1.1
Authorization: Bearer rk_8b4d2f6a0c9e7b5d3a1f8c6e4b2d0a9f
If-None-Match: "ws_9c938701f37f5bae73592c4f27d7790b:4128"
```

`304` is the one response on this server without a JSON body. Every *error* is
still the shape in the table above; `304` is not an error. The `ETag` is opaque
— echo it, do not parse it, and do not derive one from `head`.

Sending no `If-None-Match` always gets a `200` and the whole document, so a
client that ignores this section is correct, only chattier.

The server keeps each workspace's merged state between requests and advances it
by the ops that arrived since, so a poll that finds nothing new is one indexed
query returning no rows — not a replay of the log. Poll it on the same interval
you would poll `GET /v1/ops`.

## Applying the log

Ops are defined by the `dev.jevido/work/packages/ops` package, which is the one
merge implementation, shared by the desktop app and this server. Every consumer
reaches the same state because every consumer runs that same code — there is no
second implementation to disagree with it.

An earlier version of this document drew the line differently: *the server
stores and orders ops; it does not merge them and does not materialise a
document.* The first half still holds. `POST /v1/ops` and `GET /v1/ops` are the
whole sync protocol, and they are all the desktop app uses — it merges locally
and keeps working with this server unreachable, which is the reason the log,
rather than a document, is what goes on the wire.

The second half is no longer true, and `GET /v1/document` is where it stopped
being true. The web viewer is a browser page; `packages/ops` is Go; a browser
cannot run it. The only other way to show a document in a browser is a second
merge implementation in TypeScript, and two merges that drift apart show two
different documents for one log — a bug with no correct side and no way to see
it from either. So the server hosts the merge for the client that cannot host
it, and that endpoint is a convenience, not the protocol. The invariant that
mattered survives intact: **one merge implementation.** What changed is that it
can be reached over HTTP, not that there are two of it.

**The log's order is not the merge order.** `seq` is arrival order at the
server, which depends on who had network when. Convergence comes from the op's
own `clock` and `actor`, so replaying the same set of ops in any order gives the
same result. Do not sort by `seq` and treat the last write as the winner.

### The op envelope

| Field | Type | Present for | Means |
| --- | --- | --- | --- |
| `id` | string | all | Unique. The idempotency key. A ULID or UUID. |
| `kind` | string | all | One of the kinds below. |
| `actor` | string | all | Which replica wrote it. Also the tiebreak when two ops share a `clock`. |
| `clock` | number | all | Lamport counter. Higher wins. |
| `node` | string | all | The node the op targets. |
| `fields` | object | `create-node`, `set-fields`, `extract-to-task` | Field name to arbitrary JSON value. |
| `parent` | string | `create-node`, `move-node`, `extract-to-task` | Parent node ID. Empty means a root. |
| `position` | string | `create-node`, `move-node`, `extract-to-task` | Sort key among siblings, compared as a string. |
| `task` | string | `extract-to-task` | ID of the task node being created. |

`clock` must be at least 1 — zero is reserved for "never written". Identifiers
(`id`, `actor`, `node`, `task`, `parent`) are capped at 128 bytes, `position` at
256, field names at 128, and one op carries at most 256 fields. An op that
breaks any of these is a 400 naming the field, and the same limits are enforced
by the desktop before it sends, so nothing that would be refused here is ever
put on the wire.

### The kinds

| `kind` | Does |
| --- | --- |
| `create-node` | Creates `node` under `parent` at `position` with `fields`. |
| `set-fields` | Merges `fields` into `node`, one field at a time. |
| `delete-node` | Tombstones `node`. Permanent. |
| `move-node` | Re-parents `node` to `parent` at `position`. |
| `extract-to-task` | Creates task node `task` under `parent` at `position` with `fields`, and links `node` to it. |

### The three merge rules

1. **Fields are last-write-wins, one field at a time.** Two replicas editing
   different fields of the same node both keep their edit. Two editing the same
   field: the higher `clock` wins, and if the clocks are equal, the higher
   `actor` wins.

   Where a node *sits* is stamped separately from its fields, so a move and an
   edit never clobber one another. But `parent` and `position` are one slot
   between them, not two: a move that wins takes both halves, and a move that
   loses takes neither. Stamping them apart would look like finer-grained
   merging and would be worse — two replicas moving the same node at once would
   land it under one replica's parent at the other's position, which is a place
   neither of them chose.

   A consequence for clients: `move-node` always states both. Sending one with
   no `position` moves the node *to* the empty position; it does not keep the
   position the node had. Send the position you want, every time.

   **The losing op stays in the log.** Losing a field is not being rejected —
   the op is stored, ordered and handed back like any other, and only the value
   it wrote is not the one showing. That is what a client needs to tell someone
   their edit was overwritten: it can see its own op in the log next to the one
   that beat it, and undoing means sending the value again with a higher clock.
   Nothing here is undone on a client's behalf.

2. **Delete beats a concurrent edit, in either order.** Deleting is one-way. A
   node deleted anywhere is deleted everywhere, whether the delete arrived
   before the edit or after it, and no clock value undoes it. Nothing in this
   API undeletes a node — restoring means creating a new one.

   Later edits do still merge into a deleted node's fields. That sounds like the
   edit winning and is not: the node stays deleted and stays out of the tree.
   The reason is that a tombstone which swallowed later writes would keep
   whichever fields happened to arrive before the delete, and that differs per
   replica. Merging them keeps a tombstone's contents the same everywhere, so a
   viewer can say *what* was deleted rather than only that something was.

   This is a merge rule, not an endpoint rule, and the two are not in
   disagreement. The merge applies whatever op it is handed. `POST /v1/ops`
   declines to *store* a new op aimed at a node it already knows is a tombstone
   — see *The one thing a write is refused for* — because there is no node left
   for it to be about.

3. **Ops are idempotent by `id`.** Applying an op twice changes nothing the
   second time. This holds in the merge as well as at the endpoint, so a client
   replaying its own outbox is safe.

Children of a tombstoned node are kept in the log but are not reachable from the
tree, so a child that arrives after its parent was deleted does not resurrect
the parent.

### Reserved field names

`extract-to-task` writes two conventional fields, both ordinary LWW fields you
can read like any other:

- `taskId` on the source node — the node ID of the task extracted from it.
- `extractedFrom` on the task node — the node ID it came from.

This is what links a task back to the region of the map it was pulled out of,
and it is the link a client follows to show one beside the other. One
extraction records **one** source node: `extractedFrom` is a node ID, not a
list. A task gathered from several nodes at once has no shape here yet — it
would be a new field carrying the rest, added the way anything else is added,
and nothing needs it today.

## Running it

```sh
cd server
go build ./...
go test ./...
```

Configuration is environment variables only:

| Variable | Default | Means |
| --- | --- | --- |
| `DATABASE_URL` | — | Postgres connection string. Required. |
| `PORT` | `8080` | Port to listen on. |
| `WORK_SIGNUP_TOKEN` | — | Bearer token for `POST /v1/workspaces`. Unset leaves workspace creation open to anyone who can reach the server, which is the default; set, it must be at least 24 characters, because a short one leaves creation open to guessing while looking closed. |
| `WORK_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `WORK_SITE_DIR` | — | Directory holding the built viewer, served at `/`. Unset serves the API only, and `/` answers the same `not_found` as any other unknown path. Checked at startup: set and missing an `index.html` is a refusal to start, not a 500 later. |

Migrations are embedded in the binary and run at startup, in order, inside a
transaction each, recorded in a `schema_migrations` table. Starting the server
against an empty database is all the setup there is.

`services/sync` is a directory of the one module, and the dependency runs one
way on purpose: nothing in `apps/studio` imports anything here, so the app must
not grow a Postgres driver in its dependency tree to get at the shared op code,
so the dependency runs one way: this module imports
`dev.jevido/work/packages/ops`, and nothing in the desktop app imports anything
here.

### Tests

`go test ./...` runs without a database — the HTTP layer is tested against an
in-memory store. The Postgres store's own tests need a real database and skip
themselves when `TEST_DATABASE_URL` is unset:

```sh
TEST_DATABASE_URL=postgres://localhost/work_test?sslmode=disable go test ./...
```

That skip is right on a laptop with no Postgres and wrong everywhere else: a run
that skips the database tests passes while testing none of the code this server
is. So there is a second variable, and CI sets it:

```sh
WORK_REQUIRE_POSTGRES=1 \
  TEST_DATABASE_URL=postgres://localhost/work_test?sslmode=disable \
  go test -race ./...
```

With `WORK_REQUIRE_POSTGRES` set, a missing `TEST_DATABASE_URL` fails instead of
skipping. It is a promise that a database was supplied, and the tests hold the
caller to it; see `services/sync/internal/pgtest`.

`.github/workflows/ci.yml` runs this module against a `postgres:17-alpine`
service on every push, with `-race`, and then asserts from `go test -json` that
`services/sync` and `services/sync/store` reported passing tests and skipped
none. The guard above lives in Go and the assertion lives in the workflow, on
purpose: removing either one still leaves a red run.
