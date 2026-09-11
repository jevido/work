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
	"github.com/jackc/pgx/v5/pgxpool"
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
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring a connection to migrate on: %w", err)
	}
	defer conn.Release()

	// The lock is taken before anything is read, and before anything is
	// created, because the first thing pending does is create the table it
	// then reads. CREATE TABLE IF NOT EXISTS is not atomic in Postgres: two
	// sessions can both find the table absent and both try to make it, and one
	// of them dies on pg_type_typname_nsp_index rather than getting the "if not
	// exists" it asked for. That is two instances of this server starting
	// together, which is every rolling deploy.
	//
	// It costs one lock round-trip on a start with nothing to do. That is a
	// startup, once, against a schema check that already costs a query.
	//
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

	files, err := pending(ctx, conn)
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
//
// It takes the connection the migration lock is held on rather than the pool,
// so that it cannot run before the caller has taken that lock.
func pending(ctx context.Context, conn *pgxpool.Conn) ([]string, error) {
	_, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    int         PRIMARY KEY,
			name       text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return nil, fmt.Errorf("creating the migration table: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
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
