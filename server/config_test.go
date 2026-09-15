package main

import (
	"log/slog"
	"strings"
	"testing"
)

// set puts the environment into a known state, so a test is about what it
// changes rather than about what happened to be exported.
func set(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range []string{"DATABASE_URL", "PORT", "WORK_SIGNUP_TOKEN", "WORK_LOG_LEVEL", "WORK_SITE_DIR"} {
		t.Setenv(name, values[name])
	}
}

func TestLoad(t *testing.T) {
	const url = "postgres://localhost/work?sslmode=disable"

	t.Run("the defaults are the documented ones", func(t *testing.T) {
		set(t, map[string]string{"DATABASE_URL": url})
		cfg, err := load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.addr != ":8080" {
			t.Errorf("addr = %q, want :8080", cfg.addr)
		}
		if cfg.logLevel != slog.LevelInfo {
			t.Errorf("logLevel = %v, want info", cfg.logLevel)
		}
		// Unset is what closes workspace creation, so this default is a
		// security decision and not a convenience.
		if cfg.signupToken != "" {
			t.Errorf("signupToken = %q, want empty so creation stays closed", cfg.signupToken)
		}
		// Unset is the normal case for a local run, and it means the API only.
		// It is not an error and must never become one: every `go run .` on a
		// machine with no built viewer on it goes through here.
		if cfg.siteDir != "" {
			t.Errorf("siteDir = %q, want empty", cfg.siteDir)
		}
	})

	t.Run("WORK_SITE_DIR is kept verbatim", func(t *testing.T) {
		// Kept, not resolved or checked. Whether the directory is really there
		// is openSite's question, and answering it here would put the failure
		// in the wrong message.
		set(t, map[string]string{"DATABASE_URL": url, "WORK_SITE_DIR": "/srv/site"})
		cfg, err := load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.siteDir != "/srv/site" {
			t.Errorf("siteDir = %q, want /srv/site", cfg.siteDir)
		}
	})

	t.Run("a missing database is fatal", func(t *testing.T) {
		set(t, nil)
		if _, err := load(); err == nil {
			t.Fatal("load accepted a config with no DATABASE_URL")
		}
	})

	t.Run("PORT becomes the listen address", func(t *testing.T) {
		set(t, map[string]string{"DATABASE_URL": url, "PORT": "9000"})
		cfg, err := load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.addr != ":9000" {
			t.Errorf("addr = %q, want :9000", cfg.addr)
		}
	})

	t.Run("log levels", func(t *testing.T) {
		levels := map[string]slog.Level{
			"debug": slog.LevelDebug,
			"info":  slog.LevelInfo,
			"warn":  slog.LevelWarn,
			"error": slog.LevelError,
			"ERROR": slog.LevelError,
		}
		for name, want := range levels {
			set(t, map[string]string{"DATABASE_URL": url, "WORK_LOG_LEVEL": name})
			cfg, err := load()
			if err != nil {
				t.Errorf("WORK_LOG_LEVEL=%s: %v", name, err)
				continue
			}
			if cfg.logLevel != want {
				t.Errorf("WORK_LOG_LEVEL=%s gave %v, want %v", name, cfg.logLevel, want)
			}
		}

		set(t, map[string]string{"DATABASE_URL": url, "WORK_LOG_LEVEL": "chatty"})
		if _, err := load(); err == nil {
			t.Error("load accepted an unknown log level")
		}
	})

	t.Run("a short signup token is refused", func(t *testing.T) {
		// Refusing is the point. A short token leaves workspace creation open
		// to guessing while looking closed, which is worse than leaving it
		// unset, because the operator believes it is shut.
		for _, short := range []string{"x", "hunter2", strings.Repeat("a", 23)} {
			set(t, map[string]string{"DATABASE_URL": url, "WORK_SIGNUP_TOKEN": short})
			if _, err := load(); err == nil {
				t.Errorf("load accepted a %d character signup token", len(short))
			}
		}

		long := strings.Repeat("a", 24)
		set(t, map[string]string{"DATABASE_URL": url, "WORK_SIGNUP_TOKEN": long})
		cfg, err := load()
		if err != nil {
			t.Fatalf("load refused a 24 character token: %v", err)
		}
		if cfg.signupToken != long {
			t.Errorf("signupToken = %q, want it kept verbatim", cfg.signupToken)
		}
	})
}
