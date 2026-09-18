package store

import (
	"context"
	"embed"
	"log/slog"

	"dev.jevido/work/packages/pgmigrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock is an arbitrary but fixed number used as a Postgres advisory
// lock, so two instances starting together do not both try to create the same
// table.
//
// Deliberately not the sync server's number, for the reason its own comment
// gives: the two may share a Postgres.
const migrationLock = 8_251_749_006_332

// Migrate brings the database up to date, and is safe to run on every start.
func Migrate(ctx context.Context, s *Store, logger *slog.Logger) error {
	return pgmigrate.Run(ctx, s.pool, migrations, pgmigrate.Config{
		Dir:   "migrations",
		Table: "charted_migrations",
		Lock:  migrationLock,
	}, logger)
}
