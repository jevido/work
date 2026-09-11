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

Keys are stored as SHA-256 hashes. The server cannot show you a key again after
it is created — losing one means rotating it.

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
| 413 | `too_large` | request body over the limit |
| 429 | `rate_limited` | too many requests; retry after `Retry-After` seconds |
| 500 | `internal` | the server's fault; safe to retry |

`message` is for humans and may change. Branch on `code`, never on `message`.

Rate limiting is per key — 10 requests a second with a burst of 40 — and
`/v1/health` is never limited. A viewer polling every few seconds and a desktop
replica syncing hard are both far underneath that, so hitting a 429 means a
loop, not load. The budget is held in memory, so it is per server instance.

## Endpoints

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

Gated by a server-configured signup token (`WORK_SIGNUP_TOKEN`), sent as the
bearer token. Without that token configured the endpoint returns 403 and the
server can only serve workspaces that already exist.

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

- `accepted` — ops written by this request, each with the sequence number it got.
- `duplicates` — ops whose `id` was already in the log. **This is not an error.**
  The entry carries the `seq` the op already had, so a client that lost the
  response to a previous attempt can retry the identical request and reconcile.
- `head` — the workspace's highest sequence number after this request.

Retrying a request is always safe: op IDs are the idempotency key, so a replay
appends nothing and reports every op as a duplicate.

An op whose `id` matches an existing op with **different content** is rejected
with 400 `bad_request` rather than silently ignored — that is a client bug, not
a retry.

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
`internal/ops` is Go. *Applying the log*, below, says why that is an exception
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

Ops are defined by the `dev.jevido/work/internal/ops` package, which is the one
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
being true. The web viewer is a browser page; `internal/ops` is Go; a browser
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
| `WORK_SIGNUP_TOKEN` | — | Bearer token for `POST /v1/workspaces`. Unset disables workspace creation; set, it must be at least 24 characters, because a short one leaves creation open to guessing while looking closed. |
| `WORK_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |

Migrations are embedded in the binary and run at startup, in order, inside a
transaction each, recorded in a `schema_migrations` table. Starting the server
against an empty database is all the setup there is.

`server/` is its own Go module on purpose. The desktop app must not grow a
Postgres driver in its dependency tree to get at the shared op code, so the
dependency runs one way: this module imports `dev.jevido/work/internal/ops`, and
nothing in the desktop app imports anything here.

### Tests

`go test ./...` runs without a database — the HTTP layer is tested against an
in-memory store. The Postgres store's own tests need a real database and skip
themselves when `TEST_DATABASE_URL` is unset:

```sh
TEST_DATABASE_URL=postgres://localhost/work_test?sslmode=disable go test ./...
```
