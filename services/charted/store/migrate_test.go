package store

import (
	"strings"
	"testing"

	"dev.jevido/work/packages/pgmigrate"
)

// The rule is only worth having if this service's files obey it, and a new
// migration is exactly when somebody gets the name wrong. The rule itself lives
// in packages/pgmigrate and is tested there; this is about these files.
//
// No database needed, deliberately: everything else in this package wants a
// Postgres, and this is the one check that would otherwise be skipped on the
// machine where the mistake was made.
func TestEveryMigrationIsNamedCorrectly(t *testing.T) {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations are embedded, so the binary would start against an empty database")
	}
	seen := make(map[int]string, len(entries))
	for _, entry := range entries {
		version, err := pgmigrate.VersionOf(entry.Name())
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
}

// Two services may share a Postgres, so their locks and their bookkeeping
// tables must not be the same. A collision would be a startup that hangs, or
// one service reading the other's history as its own.
func TestTheLockIsNotTheSyncServers(t *testing.T) {
	const syncLock = 8_251_749_006_331
	if migrationLock == syncLock {
		t.Error("charted and sync take the same advisory lock")
	}
}
