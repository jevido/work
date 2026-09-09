package config

import (
	"os"
	"path/filepath"
	"testing"
)

// isolate points the config helpers at a temporary home, so a test never reads
// or writes the developer's own config.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Both are set because UserConfigDir consults XDG_CONFIG_HOME on Linux and
	// derives from HOME everywhere else.
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return dir
}

// A missing file is the first run, not a failure.
func TestLoadMissingIsEmpty(t *testing.T) {
	isolate(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Root != "" {
		t.Errorf("Root = %q, want empty", c.Root)
	}
}

func TestSaveThenLoad(t *testing.T) {
	isolate(t)
	if err := Save(Config{Root: "/tmp/agents"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Root != "/tmp/agents" {
		t.Errorf("Root = %q", c.Root)
	}
}

// Saving twice must leave one config file and no temporary files behind.
func TestSaveLeavesNoDebris(t *testing.T) {
	isolate(t)
	for _, root := range []string{"/one", "/two"} {
		if err := Save(Config{Root: root}); err != nil {
			t.Fatalf("Save %s: %v", root, err)
		}
	}

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Errorf("config dir holds %d entries, want just the config file", len(entries))
	}
}

// A corrupt file is reported rather than silently replaced: it may be the only
// record of where the user's agents live.
func TestLoadCorruptIsAnError(t *testing.T) {
	isolate(t)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("want an error for a corrupt config")
	}
}
