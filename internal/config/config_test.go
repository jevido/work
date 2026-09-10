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

// A terminal launch answers the question itself: the directory you started
// Work in is the project, and it is worth remembering for the launches that
// cannot say.
func TestStartDirPrefersADeliberateCwd(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	dir, remember := StartDir(project, "/somewhere/else")
	if dir != project {
		t.Errorf("dir = %q, want %q", dir, project)
	}
	if !remember {
		t.Error("a new project directory should be remembered")
	}
}

// Starting from the same project twice must not rewrite the config.
func TestStartDirDoesNotRememberWhatItAlreadyKnows(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	if _, remember := StartDir(project, project); remember {
		t.Error("an unchanged directory should not be written back")
	}
}

// The launcher case: cwd is $HOME, so the last real project stands in.
func TestStartDirFallsBackWhenLaunchedFromHome(t *testing.T) {
	home := isolate(t)
	project := t.TempDir()

	dir, remember := StartDir(home, project)
	if dir != project {
		t.Errorf("dir = %q, want the saved project %q", dir, project)
	}
	if remember {
		t.Error("a fallback is not a new choice and should not be written back")
	}
}

// Some launchers start a process in "/" rather than $HOME. Same answer.
func TestStartDirFallsBackWhenLaunchedFromRoot(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	if dir, _ := StartDir("/", project); dir != project {
		t.Errorf("dir = %q, want the saved project %q", dir, project)
	}
}

// A remembered project that has since been moved or deleted is not an answer,
// so Work stays where it was started rather than running somewhere gone.
func TestStartDirIgnoresASavedDirectoryThatIsGone(t *testing.T) {
	home := isolate(t)

	dir, remember := StartDir(home, filepath.Join(home, "deleted-project"))
	if dir != home {
		t.Errorf("dir = %q, want %q", dir, home)
	}
	if remember {
		t.Error("$HOME is not a project and must not be remembered as one")
	}
}

// First run from a launcher: nothing saved, nowhere better to go. The window
// still has to open, so cwd stands -- it just must not be recorded as a
// project the next launch would fall back to.
func TestStartDirFirstRunFromHome(t *testing.T) {
	home := isolate(t)

	dir, remember := StartDir(home, "")
	if dir != home {
		t.Errorf("dir = %q, want %q", dir, home)
	}
	if remember {
		t.Error("$HOME must not be remembered as a project")
	}
}

// WorkDir survives a write that is only about another field.
func TestUpdateKeepsWorkDir(t *testing.T) {
	isolate(t)
	if err := Save(Config{WorkDir: "/src/project"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Update(func(c *Config) { c.Root = "/src/agents" }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.WorkDir != "/src/project" {
		t.Errorf("WorkDir = %q, want it preserved", c.WorkDir)
	}
}
