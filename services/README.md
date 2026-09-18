# services

Programs that run with nobody looking at them. One directory each.

```
sync/       The server behind work.jevido.app: HTTP, Postgres, and the merge.
charted/    The documentation site: renders pages on write, serves and searches them.
```

Both are the same shape, deliberately — `main.go`, an `api/` that is only HTTP,
a `store/` that is only Postgres, and `store/migrations/` embedded in the
binary. A new service should look like these two before it looks like anything
else.

Both also serve a static bundle from [`apps`](../apps/README.md): sync hands out
`apps/website`, Charted hands out `apps/charted`. The serving itself is
[`packages/site`](../packages/README.md), so there is one implementation of the
single-page fallback rather than one per server.

Neither imports anything from an app. The dependency runs one way on purpose:
services read `packages/`, apps read `packages/`, and nothing under `apps/`
appears in a service's import graph — which is what lets the desktop app be
built without a Postgres driver in it.

## Running them

```sh
task sync:up        # sync and its Postgres, on :8080
task charted:up     # charted and its Postgres, on :8081
task test           # every Go test in the repository, these included
```

The Postgres-backed tests skip themselves unless `TEST_DATABASE_URL` is set;
`task sync:test` sets it and fails rather than skipping when the database is
configured and unreachable. How each is built and deployed is
[`infra`](../infra/README.md).
