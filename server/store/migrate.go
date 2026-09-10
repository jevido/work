package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock is an arbitrary but fixed number, used as a Postgres advisory
// lock so that two instances starting at the same time do not both try to
// create the same table. The value means nothing; only that everyone agrees on
// it.
const migrationLock = 8_251_749_006_331

// Migrate brings the database up to date, and is safe to run on every start.
//
// Migrations are embedded in the binary rather than shipped beside it, so
// deploying is copying one file and there is no way to run a version of the
// server against a schema it was not built for.
func Migrate(ctx context.Context, s *Store, logger *slog.Logger) error {
	files, err := pending(ctx, s)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring a connection to migrate on: %w", err)
	}
	defer conn.Release()

	// The lock is held on one connection for the whole run and released by
	// unlocking rather than by returning it to the pool, because a pooled
	// connection can outlive this function.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return fmt.Errorf("taking the migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLock); err != nil {
			logger.Error("releasing the migration lock", "err", err)
		}
	}()

	// Re-read now the lock is held: another instance may have applied
	// everything between the first read and here, and applying a migration
	// twice is worse than starting a moment later.
	files, err = pending(ctx, s)
	if err != nil {
		return err
	}

	for _, file := range files {
		body, err := migrations.ReadFile(path.Join("migrations", file))
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file, err)
		}
		version, err := versionOf(file)
		if err != nil {
			return err
		}

		// One transaction per migration. Postgres runs DDL transactionally, so
		// a migration that fails half way leaves nothing behind and the next
		// start tries the same one again.
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return fmt.Errorf("running migration %s: %w", file, err)
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, version, file)
			if err != nil {
				return fmt.Errorf("recording migration %s: %w", file, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		logger.Info("migration applied", "version", version, "name", file)
	}
	return nil
}

// pending lists the migration files not yet recorded as applied, in order.
func pending(ctx context.Context, s *Store) ([]string, error) {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    int         PRIMARY KEY,
			name       text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return nil, fmt.Errorf("creating the migration table: %w", err)
	}

	rows, err := s.pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scanning applied migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}
	var todo []string
	for _, entry := range entries {
		version, err := versionOf(entry.Name())
		if err != nil {
			return nil, err
		}
		if !applied[version] {
			todo = append(todo, entry.Name())
		}
	}
	// Filenames are zero-padded, so sorting them as strings sorts them by
	// version. versionOf is what enforces the padding.
	slices.Sort(todo)
	return todo, nil
}

// versionOf reads the number a migration filename starts with.
func versionOf(name string) (int, error) {
	digits, _, found := strings.Cut(name, "_")
	if !found || len(digits) != 4 {
		return 0, fmt.Errorf("migration %q is not named NNNN_description.sql", name)
	}
	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("migration %q does not start with a number: %w", name, err)
	}
	return version, nil
}
