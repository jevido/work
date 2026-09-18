# Work

A monorepo. One rule decides which directory a program goes in:

| Directory | What lives there |
|---|---|
| `apps/` | something a person opens and looks at |
| `services/` | something that runs with nobody looking at it |
| `packages/` | something two of the above share |
| `infra/` | how any of it is built, run and deployed |

```
apps/studio/      The desktop workbench: Wails, Go and Svelte. A visual office of agents.
apps/website/     work.jevido.app — the read-only viewer somebody is sent a link to.
apps/charted/     The documentation site's reader.
services/sync/    The server behind work.jevido.app. HTTP and Postgres, no UI.
services/charted/ The server behind the documentation site. Renders and stores its pages.
packages/ops/     The merge. One implementation, run by the app and the server both.
packages/pgmigrate/ Running .sql files against Postgres, once each, safely.
packages/site/    Serving a built single-page bundle, index.html as the fallback.
packages/mindmap/ Map layout and canvas rendering. No document model in it.
packages/ui/      Panel dragging, tail-following. Behaviour, not chrome.

infra/sync/       Dockerfile and compose for the sync server and the website
infra/charted/    Dockerfile and compose for Charted
infra/dev/        Both, on a laptop, with their databases
```

Every directory above has a README saying what belongs in it, and so does every
program and package inside them. Start with [`apps`](apps/README.md), or with
[`apps/studio`](apps/studio/README.md) for the thing most of this exists to
run.

Two of the three apps are a page and nothing else — `index.html`, `src/`, a vite
config — because their server is a separate program under `services/`. The
desktop app is the exception and cannot be split: a Wails binary is Go and a
front end compiled into one file, and that file is the thing a person opens.

## One Go module

`go.mod` at the root covers everything, so `go build ./...` and `go test ./...`
here reach the app and the services together. That is a deliberate change from
the split the repository used to have, where the sync server was its own module
with a `replace ../` back into the app: a shared package behind a replace
directive is a dependency that neither side can see properly, and it kept
`packages/ops` — the code both of them run — out of CI for as long as it
existed.

What keeps the boundary is Go's own rule about `internal/`, not module edges.
`apps/studio/internal/...` is importable only from inside `apps/studio`;
anything two programs share has to be moved to `packages/` first, where the move
is visible in a diff.

The front ends are separate npm projects with no workspace tying them together.
What they share is reached through two aliases, `@mindmap/*` and `@ui/*`,
declared in each app's `vite.config.ts` and `tsconfig.json`.

## Doing things

Everything is a [Task](https://taskfile.dev) target. `task` is not a shell
builtin and does not ship with Go — you need one of these two before any of the
commands below work:

```sh
# Either: install Task itself, and then `task <name>` works everywhere.
go install github.com/go-task/task/v3/cmd/task@latest   # or: pacman -S go-task
                                                       # or: brew install go-task/tap/go-task

# Or: use the copy embedded in the Wails CLI, which you need for the desktop
# app anyway. `wails3 task <name>` is the same runner reading the same files.
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.18
```

Both read `Taskfile.yml` in the directory you are standing in, so run them from
the repository root. Everything below is written as `task`; put `wails3` in
front of it if you took the second route.

### Working on Work

```sh
task up      # both services and their databases, in Docker
task dev     # the same, then the desktop app in the foreground
task down    # stop everything, and discard both databases
```

`task up` is the whole background half of the system: the sync server the
desktop app joins, Charted for documentation mode to publish to, and a Postgres
each. It waits until both answer their health check before it prints where they
are, so nothing it names is still starting. Running it twice is free.

`task dev` is that, then `wails3 dev` — the desktop app with live reload,
holding the terminal. The first start asks for a config folder; point it at an
empty directory and Work writes the team into it.

The two readers are their own terminals, because each wants one:

```sh
task website:dev   # the viewer      http://localhost:5175
task charted:dev   # the docs reader http://localhost:5176
```

What is running, and on what:

| | | |
|---|---|---|
| sync server | http://localhost:8080 | signup token `devdevdevdevdevdevdevdevdev` |
| charted | http://localhost:8081 | write token `dev` |
| sync's Postgres | `127.0.0.1:5432` | `work` / `work`, plus a `work_test` database |
| charted's Postgres | `127.0.0.1:5433` | `charted` / `charted` |

The signup token is the word repeated because the server refuses anything under
24 characters — it is not pretending to be a secret, and nothing here is
reachable from outside this machine. Both databases live in tmpfs, so `task
down` is also how you reset them.

`task status` says what is up, `task logs` follows both services at once.

### Everything else

```sh
task                # what there is
task build          # every program
task test           # the whole Go module
task check          # go vet, then every front end's typecheck
task sync:test      # the sync server's tests against the local Postgres
```

Anything under `studio:`, `charted:` or `website:` is that app answering for
itself, from its own Taskfile. The desktop app's sits in `apps/studio` beside
the Wails build config, and `wails3` has to be run from there for anything it
builds or packages.

## Deployment

Everything about it is in [`infra`](infra/README.md): one directory per deployed
thing, each with its own Dockerfile and compose file, all built from a context
rooted at the repository because the Go module is. Nothing about the desktop app
is in those images — its releases come from `.github/workflows/release.yml` and
are attached to a git tag.
