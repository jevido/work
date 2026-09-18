# infra

How each deployable thing is built and run. Nothing in here is imported by
anything; it is the outside of the repository rather than part of it.

```
infra/sync/       The sync server, and the website it serves
infra/charted/    Charted: services/charted, serving apps/charted
infra/dev/        Both of them on a laptop, with their databases
```

One directory per deployed thing, and one compose file each. That is not
tidiness: Coolify parses the compose file it deploys to work out what the
application is, and a second web service inside the sync server's file was once
enough for it to re-derive the domain — `work.jevido.app` came off the proxy and
was replaced by a generated host, with the container still running and healthy
behind it.

## Build context

Every image is built from the **repository root**, not from the directory its
Dockerfile sits in, so each compose file says `context: ../..`. The monorepo is
one Go module: the manifest that resolves any service's imports is `go.mod` at
the top, and `packages/ops` — which the sync server compiles — is outside
`services/sync` entirely.

`.dockerignore` therefore lives at the root as well, where the context is.

```sh
docker build -f infra/sync/Dockerfile .
docker build -f infra/charted/Dockerfile .
```

## Locally

```sh
task up             # both services and both databases, and wait for them
task down           # stop them, and discard the databases
task status         # what is up, and on what port
task logs           # follow both at once

task sync:up        # or one at a time: sync on :8080, Postgres on :5432
task charted:up     # charted on :8081, Postgres on :5433
```

Both dev stacks keep their database in tmpfs, so bringing one down is also how
you reset it. They use different ports on purpose — documentation mode reads one
and publishes to the other, so both are up at once.

## Deploying

Two Coolify resources, one per compose file:

| Resource | Domain | Compose path | Wants set in Coolify |
|---|---|---|---|
| sync | work.jevido.app | `infra/sync/compose.yml` | `SERVICE_PASSWORD_POSTGRES` (generated), `WORK_SIGNUP_TOKEN` (24 characters or more) |
| charted | charted.jevido.app | `infra/charted/compose.yml` | `SERVICE_PASSWORD_CHARTEDPG` (generated), `CHARTED_TOKEN` |

Both domains are named in their compose file rather than left to Coolify, which
would otherwise generate an sslip.io host — correct, and not the address
anybody has been given.

No environment is named in either file. The domain, the database password and
the tokens are per-application state that Coolify holds, which is what keeps
every credential out of the repository — there is no `.env` to forget to ignore,
because there are no literal secrets to put in one.

Desktop releases do not come from here. They are built by
`.github/workflows/release.yml` and attached to a git tag.
