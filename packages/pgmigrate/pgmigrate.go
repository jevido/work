// Package pgmigrate runs a directory of .sql files against Postgres, once each,
// safely from more than one process at a time.
//
// Shared because both services here migrate on start and both had their own
// copy of this: the same advisory lock, the same one-transaction-per-file, the
// same "filenames are zero-padded so sorting them sorts them by version". Only
// one of the two copies had tests. What stays with each caller is the part that
// genuinely differs -- which directory, which bookkeeping table, and which lock
// number.
package pgmigrate

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is what one service's migrations need beyond the files themselves.
type Config struct {
	// Dir is the directory inside Files holding the .sql files.
	Dir string

	// Table records what has been applied. One per service, because two
	// services sharing a Postgres must not read each other's history as their
	// own.
	Table string

	// Lock is the advisory lock number taken for the whole run. It means
	// nothing beyond "everyone migrating this schema agrees on it" -- and two
	// services that may share a database must not agree, or one of them blocks
	// on the other's migrations for a reason neither log explains.
	Lock int64
}

// Run brings the database up to date, and is safe to call on every start.
//
// Migrations are read from an fs.FS -- in practice an embed.FS, so they ship
// inside the binary rather than beside it and there is no way to run a build
// against a schema it was not compiled with.
func Run(ctx context.Context, pool *pgxpool.Pool, files fs.FS, cfg Config, logger *slog.Logger) error {
	if cfg.Dir == "" || cfg.Table == "" || cfg.Lock == 0 {
		return fmt.Errorf("pgmigrate: incomplete config %+v", cfg)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgmigrate: acquiring a connection to migrate on: %w", err)
	}
	defer conn.Release()

	// The lock is taken before anything is read, and before anything is
	// created, because the first thing pending does is create the table it then
	// reads. CREATE TABLE IF NOT EXISTS is not atomic in Postgres: two sessions
	// can both find the table absent and both try to make it, and one of them
	// dies on a unique index rather than getting the "if not exists" it asked
	// for. That is two instances of a server starting together, which is every
	// rolling deploy.
	//
	// It costs one lock round trip on a start with nothing to do, and it is
	// held on one connection for the whole run and released by unlocking rather
	// than by returning it to the pool, because a pooled connection can outlive
	// this function.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, cfg.Lock); err != nil {
		return fmt.Errorf("pgmigrate: taking the migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, cfg.Lock); err != nil {
			logger.Error("releasing the migration lock", "err", err)
		}
	}()

	todo, err := pending(ctx, conn, files, cfg)
	if err != nil {
		return err
	}

	for _, file := range todo {
		body, err := fs.ReadFile(files, cfg.Dir+"/"+file)
		if err != nil {
			return fmt.Errorf("pgmigrate: reading migration %s: %w", file, err)
		}
		version, err := VersionOf(file)
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
			if _, err := tx.Exec(ctx,
				fmt.Sprintf(`INSERT INTO %s (version, name) VALUES ($1, $2)`, cfg.Table),
				version, file); err != nil {
				return fmt.Errorf("recording migration %s: %w", file, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("pgmigrate: %w", err)
		}
		logger.Info("migration applied", "version", version, "name", file, "table", cfg.Table)
	}
	return nil
}

// pending lists the migration files not yet recorded as applied, in order.
//
// It takes the connection the lock is held on rather than the pool, so that it
// cannot run before the caller has taken that lock.
func pending(ctx context.Context, conn *pgxpool.Conn, files fs.FS, cfg Config) ([]string, error) {
	// The table name is interpolated rather than bound, because a table name
	// cannot be a bind parameter in Postgres. It is a compile-time constant in
	// every caller -- never anything from a request -- and Config is the only
	// way in.
	if _, err := conn.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			version    int         PRIMARY KEY,
			name       text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`, cfg.Table)); err != nil {
		return nil, fmt.Errorf("pgmigrate: creating %s: %w", cfg.Table, err)
	}

	rows, err := conn.Query(ctx, fmt.Sprintf(`SELECT version FROM %s`, cfg.Table))
	if err != nil {
		return nil, fmt.Errorf("pgmigrate: reading applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("pgmigrate: scanning an applied migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgmigrate: reading applied migrations: %w", err)
	}

	entries, err := fs.ReadDir(files, cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("pgmigrate: listing %s: %w", cfg.Dir, err)
	}
	var todo []string
	for _, entry := range entries {
		version, err := VersionOf(entry.Name())
		if err != nil {
			return nil, err
		}
		if !applied[version] {
			todo = append(todo, entry.Name())
		}
	}
	// Filenames are zero-padded, so sorting them as strings sorts them by
	// version. VersionOf is what enforces the padding.
	slices.Sort(todo)
	return todo, nil
}

// VersionOf reads the number a migration filename starts with.
//
// Exported because it is the rule that makes the sort above correct, and a
// caller adding a migration wants to be able to test that its own files obey
// it without starting a database.
func VersionOf(name string) (int, error) {
	digits, _, found := strings.Cut(name, "_")
	if !found || len(digits) != 4 {
		return 0, fmt.Errorf("pgmigrate: migration %q is not named NNNN_description.sql", name)
	}
	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("pgmigrate: migration %q does not start with a number: %w", name, err)
	}
	return version, nil
}
