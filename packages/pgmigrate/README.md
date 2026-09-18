# pgmigrate

Runs a directory of `.sql` files against Postgres, once each, safely from more
than one process at a time.

Both services here migrate on start, and both had their own copy of this before
it moved: the same advisory lock, the same one transaction per file, the same
"filenames are zero-padded so sorting them sorts them by version". Only one of
the two copies had tests.

What each caller keeps is the part that genuinely differs — which directory,
which bookkeeping table, and which advisory lock number. The last one is not a
formality: the two services may share a Postgres, and if they agreed on a lock,
one would block on the other's migrations for a reason neither log explains.

```go
pgmigrate.Run(ctx, pool, migrations, pgmigrate.Config{
    Dir:   "migrations",
    Table: "schema_migrations",
    Lock:  8_251_749_006_331,
}, logger)
```

Files are read from an `fs.FS`, in practice an `embed.FS`, so they ship inside
the binary rather than beside it — deploying is copying one file, and there is
no way to run a build against a schema it was not compiled with.

The lock is taken before anything is read, and that ordering is the whole point
of the package. `CREATE TABLE IF NOT EXISTS` is not atomic in Postgres: two
instances starting together can both find the bookkeeping table absent, both
create it, and the loser dies on a unique index rather than getting the "if not
exists" it asked for. That is every rolling deploy.

A filename that is not `NNNN_description.sql` is refused rather than sorted into
an arbitrary place. `VersionOf` is exported so each caller can test that its own
files obey the rule without starting a database.
