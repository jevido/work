package store

import (
	"context"
	"embed"
	"log/slog"

	"dev.jevido/work/packages/pgmigrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock is an arbitrary but fixed number, used as a Postgres advisory
// lock so that two instances starting at the same time do not both try to
// create the same table. The value means nothing; only that everyone agrees on
// it.
//
// Deliberately not Charted's number. The two may share a Postgres, and two
// services blocking on each other's migrations would be a startup that hangs
// for a reason neither log explains.
const migrationLock = 8_251_749_006_331

// Migrate brings the database up to date, and is safe to run on every start.
//
// The running is packages/pgmigrate, shared with Charted. What is here is what
// is this service's: its own files, its own bookkeeping table, its own lock.
//
// Migrations are embedded in the binary rather than shipped beside it, so
// deploying is copying one file and there is no way to run a version of the
// server against a schema it was not built for.
func Migrate(ctx context.Context, s *Store, logger *slog.Logger) error {
	return pgmigrate.Run(ctx, s.pool, migrations, pgmigrate.Config{
		Dir:   "migrations",
		Table: "schema_migrations",
		Lock:  migrationLock,
	}, logger)
}
