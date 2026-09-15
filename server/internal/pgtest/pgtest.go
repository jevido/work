// Package pgtest decides, in one place, whether the tests that need a real
// Postgres run or skip.
//
// The tests in this module that talk to a database skip themselves when
// TEST_DATABASE_URL is unset, so a contributor with no Postgres still gets the
// rest of the suite. That default is right locally and dangerous in CI: a
// workflow that forgets the variable reports green while testing none of the
// code that the deployed server actually runs on.
//
// So CI sets WORK_REQUIRE_POSTGRES, and then a missing database is a failure
// rather than a skip. The variable is the workflow's promise that it wired the
// service up; this package is what holds it to it.
package pgtest

import (
	"os"
	"testing"
)

const (
	// URLVar is the connection string for the test database.
	URLVar = "TEST_DATABASE_URL"
	// RequireVar, when set to anything non-empty, turns "no database, skip"
	// into "no database, fail".
	RequireVar = "WORK_REQUIRE_POSTGRES"
)

// URL returns the test database connection string, or "" when there is none.
func URL() string { return os.Getenv(URLVar) }

// Required reports whether a missing database has to fail the run.
func Required() bool { return os.Getenv(RequireVar) != "" }

// Reason is what to tell a human when there is no database.
const Reason = URLVar + " is not set, so the Postgres-backed tests cannot run"

// Unavailable ends the test because there is no database: skipped normally,
// failed when the run promised one. It never returns.
func Unavailable(t testing.TB) {
	t.Helper()
	if Required() {
		t.Fatalf("%s, but %s is set: the run claimed a database and did not supply one", Reason, RequireVar)
	}
	t.Skip(Reason)
}
