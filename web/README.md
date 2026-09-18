# Work — read-only viewer

A single static page. You are sent a link, it shows you somebody's outline and
their plan, and it updates while you watch. It cannot change anything.

Plain Svelte, deliberately — not SvelteKit. There is one page, no routing and
nothing to server-render: the sync server hands out these files and answers
`/v1`, and that is the whole deployment.

## The link

```
https://work.jevido.app/#k=rk_8b4d2f6a…
```

The key is in the **fragment**, which is the only part of a URL a browser never
sends to a server. It is therefore not in the server's access log, not in a
proxy's, and not in a `Referer` header — and the page sets
`<meta name="referrer" content="no-referrer">` so that nothing it loads later
puts it in one either.

On load the page:

1. reads `k` out of the fragment,
2. saves it in `localStorage`,
3. removes the fragment with `history.replaceState`.

Replace rather than push, so Back does not return to the version of the address
that still had the key in it. After that the address bar is just the host —
which matters because a read key is a credential and address bars end up in
screenshots, in shared screens, and in whatever the browser suggests to the next
person who types the first letter of the domain.

The key stays in `localStorage` so a bookmark works. **Forget it on this
device** is at the bottom of the page, and it is there because a page that
remembers a credential across visits on a machine that is not always one
person's needs a way to stop.

## What "read-only" means here

Nothing in `web/` issues a request other than `GET`. There is no `POST` in the
directory, and a read key would be refused on one anyway (`403`, see
`server/README.md`).

The one thing that looks like it should write is folding a branch. `collapsed`
is a field on a node, so it is part of the shared document — somebody folding a
branch in the desktop app is telling everybody "this part is settled". A viewer
that wrote to it would be a read-only page that changes other people's screens.
So it does not: the document's folds are respected, and unfolding here is
remembered here and nowhere else. Same for following a task back to the idea it
came from, which opens whatever is in the way and marks the line, locally.

## What it talks to

The read half of the API in `server/README.md`, at the origin that served the
page:

| | |
| --- | --- |
| `GET /v1/workspace` | the name |
| `GET /v1/ops?since=N` | the log, paged, `Authorization: Bearer rk_…` |

Live updates are polling every five seconds, because the server has no
websocket and no SSE. A hidden tab does not poll: a page nobody is looking at
should not spend somebody's data plan on it, and it catches up the moment it
becomes visible again.

Requests are relative, so the page works wherever the server puts it and needs
no CORS.

## What a document means

`ops.ts` and `model.ts` are imported from the desktop app through the `@doc`
alias in `vite.config.ts`, rather than copied. The viewer replays the same op
log the desktop writes; two implementations of that replay would show two
different documents for the same log, and the difference would look like a
missing line rather than like a bug. Only plain modules are reached through the
alias — nothing with a Svelte rune in it — so this build never compiles anything
from the app.

`ops.ts` is itself a port of `internal/ops`, which is the normative
implementation. The properties it promises are asserted in
`frontend/src/lib/workspace/merge.check.ts` (`npm run check:merge`).

The field names on top of that merge are conventions, not protocol. In full:

| Field | On | Means |
| --- | --- | --- |
| `type` | any node | `"task"` for a line on the plan; absent or `"idea"` for the outline |
| `text` | any node | the line |
| `collapsed` | outline nodes | whether its children are folded away |
| `status` | task nodes | `"todo"`, `"doing"` or `"done"` |
| `taskId` | an idea | the task extracted from it — reserved by the protocol |
| `extractedFrom` | a task | the idea it came from — reserved by the protocol |

One tree holds both views. Tasks sit at the top level and are told from outline
lines by `type` alone.

## The install block

The footer, under a board, offers the desktop binary: `components/Install.svelte`.

It is in the footer and nowhere else on purpose. Everybody who reaches this page
was sent a link by somebody running Work, which makes them the only audience
with a reason to want it — and a download button above a board somebody opened
to read one line is an advert in front of the thing they came for. It is
therefore also absent from the gate screen, which is the one place on this page
somebody arrives with no board at all.

No version number appears in the file. The links are
`releases/latest/download/...`, which GitHub redirects to the newest release, so
a push to main does not leave a stale number here that nothing on this page
could know had gone stale.

The architecture is a guess, and it says so by offering the other one beside it.
`navigator.userAgent` gets the OS right and the architecture only when the
string happens to carry it — Chrome on Linux reports `x86_64` whatever the
machine is — so `getHighEntropyValues` refines it where it exists, which is
Chromium only. Firefox keeps the guess.

macOS and Windows get no button at all. Releases are Linux, and a button that
hands somebody a binary their machine cannot run is worse than a sentence
telling them to build from source.

The install steps are open rather than folded behind a summary: the binary
arrives unexecutable and draws into a webview it does not carry, so a download
on its own does nothing. A page that knows the missing step and hides it sends
people away.

## Running it

```sh
npm install
npm run dev            # proxies /v1 to http://127.0.0.1:8080
WORK_SERVER=https://work.jevido.app npm run dev
npm run build          # static files in web/dist
npm run check          # svelte-check
```

`npm run build` writes `web/dist`, and that directory is what the sync server
serves at `/`. In production the image builds it: the `Dockerfile`'s `site`
stage runs `npm run check && npm run build` here and copies the result to
`/srv/site`, which is what `WORK_SITE_DIR` points at. Nothing has to be built
and uploaded separately, and `docker build .` on a laptop produces the same
image as the deploy.

Served by hand rather than by `http.FileServer`, in `server/api/static.go`:
anything that is not a file in the directory falls through to `index.html` at
200, so reloading a route the client owns works, and `/v1/…` is registered
ahead of the root so a mistyped endpoint is still a JSON 404 rather than this
page. Locally, `WORK_SITE_DIR=../web/dist` from `server/` does the same thing
without a container.

## Accessibility notes

Things that are easy to lose in a refactor, so they are written down:

- The outline is nested `<ul>`/`<li>`. The nesting *is* the level and the
  sibling count, and a screen reader reads both without an `aria-level`
  attribute repeating what the markup already says.
- The plan is an `<ol>`. The order is the content.
- A task's state is a word before it is a colour. So is the connection status.
- Fold controls are 24px, because this page opens on phones far more often than
  the desktop app does.
- There is a skip link to the outline: the outline can be hundreds of lines and
  the plan is after it.
- Long lines wrap rather than being clipped. This is a reading view.
- There is a light colour scheme. The app is dark because the office is; a link
  somebody was sent opens in whatever their machine is set to, often outdoors.
