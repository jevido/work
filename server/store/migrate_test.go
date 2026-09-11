package store

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dev.jevido/work/internal/ops"
)

// freshSchema gives a test an empty schema to itself, so migrations can be run
// from nothing rather than against the database TestMain already migrated.
//
// A schema rather than a database: it needs no extra privileges, and setting
// search_path on the pool means the migrations run unmodified, exactly as they
// will on a new deployment.
func freshSchema(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	name := "migrate_test_" + strings.ToLower(strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	if _, err := shared.Exec(t.Context(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, name)); err != nil {
		t.Fatalf("clearing the schema: %v", err)
	}
	if _, err := shared.Exec(t.Context(), fmt.Sprintf(`CREATE SCHEMA %q`, name)); err != nil {
		t.Fatalf("creating the schema: %v", err)
	}
	t.Cleanup(func() {
		// A fresh context: t.Context() is already cancelled by the time cleanup
		// runs, and the schema has to go either way.
		if _, err := shared.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, name)); err != nil {
			t.Logf("dropping the schema: %v", err)
		}
	})

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parsing TEST_DATABASE_URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = name
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("connecting to the fresh schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

// TestMigrateFromEmpty is the one that matters for a deploy: starting the
// server against a database that has never seen it is the whole setup story, so
// it has to be exercised rather than assumed. The rest of the suite runs
// against an already-migrated database and would pass with a broken migration.
func TestMigrateFromEmpty(t *testing.T) {
	s := freshSchema(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := Migrate(t.Context(), s, quiet); err != nil {
		t.Fatalf("migrating from empty: %v", err)
	}

	t.Run("everything the queries need is there", func(t *testing.T) {
		for _, table := range []string{"workspaces", "workspace_keys", "ops", "schema_migrations"} {
			var exists bool
			err := s.pool.QueryRow(t.Context(),
				`SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&exists)
			if err != nil {
				t.Fatalf("looking for %s: %v", table, err)
			}
			if !exists {
				t.Errorf("table %s was not created", table)
			}
		}
		// The unique index is not a nicety: it is the backstop that stops two
		// racing requests writing the same op twice.
		for _, index := range []string{"ops_pkey", "ops_id_per_workspace", "workspace_keys_pkey"} {
			var exists bool
			err := s.pool.QueryRow(t.Context(),
				`SELECT count(*) > 0 FROM pg_indexes
				  WHERE schemaname = current_schema() AND indexname = $1`, index).Scan(&exists)
			if err != nil {
				t.Fatalf("looking for %s: %v", index, err)
			}
			if !exists {
				t.Errorf("index %s was not created", index)
			}
		}
	})

	t.Run("it is recorded so it does not run again", func(t *testing.T) {
		var applied int
		if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
			t.Fatalf("counting migrations: %v", err)
		}
		if applied == 0 {
			t.Fatal("nothing was recorded, so every start would re-run the migrations")
		}

		// Every start runs this, so running it again has to be a no-op rather
		// than an error about a table that already exists.
		for range 3 {
			if err := Migrate(t.Context(), s, quiet); err != nil {
				t.Fatalf("re-migrating: %v", err)
			}
		}
		var after int
		if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&after); err != nil {
			t.Fatalf("counting migrations: %v", err)
		}
		if after != applied {
			t.Errorf("re-running recorded %d migrations, want the same %d", after, applied)
		}
	})

	t.Run("the schema works, not just exists", func(t *testing.T) {
		// A migration can create every table and still be wrong. Driving one
		// append through it proves the columns, types and constraints line up
		// with what the queries expect.
		created, err := s.CreateWorkspace(t.Context(), "fresh")
		if err != nil {
			t.Fatalf("creating a workspace on the fresh schema: %v", err)
		}
		auth, err := s.Lookup(t.Context(), created.WriteKey)
		if err != nil {
			t.Fatalf("looking up the new key: %v", err)
		}
		result, err := s.Append(t.Context(), auth.WorkspaceID, []ops.Op{anOp("m1", 1)})
		if err != nil {
			t.Fatalf("appending to the fresh schema: %v", err)
		}
		if result.Head != 1 || result.Accepted[0].Seq != 1 {
			t.Errorf("first op landed at head %d seq %d, want 1 and 1", result.Head, result.Accepted[0].Seq)
		}
	})
}

func TestVersionOfRejectsMisnamedMigrations(t *testing.T) {
	// The filename is the version, and sorting the filenames is what orders
	// them. A name that does not fit has to stop the server rather than run in
	// whatever order it happens to sort into.
	tests := map[string]bool{
		"0001_initial.sql":      true,
		"0012_add_a_column.sql": true,
		"1_initial.sql":         false,
		"001_initial.sql":       false,
		"00012_initial.sql":     false,
		"initial.sql":           false,
		"abcd_initial.sql":      false,
		"0001-initial.sql":      false,
		"_0001_initial.sql":     false,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := versionOf(name)
			if (err == nil) != want {
				t.Errorf("versionOf(%q) error = %v, want an error: %v", name, err, !want)
			}
		})
	}

	t.Run("every migration that ships is named correctly", func(t *testing.T) {
		// The rule is only worth having if the files obey it, and a new
		// migration is exactly when someone would get the name wrong.
		entries, err := migrations.ReadDir("migrations")
		if err != nil {
			t.Fatalf("listing migrations: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("no migrations are embedded, so the binary would start against an empty database")
		}
		seen := make(map[int]string, len(entries))
		for _, entry := range entries {
			version, err := versionOf(entry.Name())
			if err != nil {
				t.Errorf("%s: %v", entry.Name(), err)
				continue
			}
			if other, clash := seen[version]; clash {
				t.Errorf("%s and %s are both version %d", entry.Name(), other, version)
			}
			seen[version] = entry.Name()
			if !strings.HasSuffix(entry.Name(), ".sql") {
				t.Errorf("%s is not a .sql file", entry.Name())
			}
		}
	})
}

// TestConcurrentMigrateFromEmpty is the rolling deploy: two instances of the
// server start against a database that has never seen it, at the same moment.
//
// This failed before the advisory lock moved ahead of the first statement.
// CREATE TABLE IF NOT EXISTS is not atomic in Postgres — both sessions find
// schema_migrations absent, both create it, and the loser dies on
// pg_type_typname_nsp_index rather than getting the "if not exists" it asked
// for. Taking the lock afterwards was too late, because the table that records
// what has been applied is itself the first thing being created.
func TestConcurrentMigrateFromEmpty(t *testing.T) {
	s := freshSchema(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	const instances = 6
	start := make(chan struct{})
	failures := make(chan error, instances)

	var wg sync.WaitGroup
	for range instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := Migrate(context.Background(), s, quiet); err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)

	for err := range failures {
		t.Errorf("concurrent migrate: %v", err)
	}

	// One row per migration, not one per instance that tried.
	var applied, files int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("counting applied migrations: %v", err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	files = len(entries)
	if applied != files {
		t.Errorf("%d migrations recorded, want %d: an instance applied one twice", applied, files)
	}
}
