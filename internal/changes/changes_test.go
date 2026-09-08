package changes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo builds a throwaway git repository with one committed file.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}

	write(t, dir, "tracked.txt", "committed\n")
	gitOK(t, dir, "add", ".")
	gitOK(t, dir, "commit", "-m", "initial")
	return dir
}

func gitOK(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func find(changes []Change, path string) (Change, bool) {
	for _, c := range changes {
		if c.Path == path {
			return c, true
		}
	}
	return Change{}, false
}

func TestDiffReportsModifiedAddedAndDeleted(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	snap, err := Take(ctx, dir)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}

	write(t, dir, "tracked.txt", "committed\nand edited\n")
	write(t, dir, "brand-new.txt", "fresh\n")
	if err := os.Remove(filepath.Join(dir, "tracked.txt")); err == nil {
		// Put it back: this test wants a modification, not a deletion.
		write(t, dir, "tracked.txt", "committed\nand edited\n")
	}

	changes, err := snap.Diff(ctx)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	mod, ok := find(changes, "tracked.txt")
	if !ok {
		t.Fatalf("tracked.txt missing from %+v", changes)
	}
	if mod.Status != "modified" {
		t.Errorf("status = %q, want modified", mod.Status)
	}
	if !strings.Contains(mod.Patch, "+and edited") {
		t.Errorf("patch does not show the edit:\n%s", mod.Patch)
	}

	added, ok := find(changes, "brand-new.txt")
	if !ok {
		t.Fatalf("brand-new.txt missing from %+v", changes)
	}
	if added.Status != "added" {
		t.Errorf("status = %q, want added", added.Status)
	}
}

// TestDiffIgnoresEditsMadeBeforeTheRun is the point of taking a snapshot: work
// you had already done must not be attributed to the agent.
func TestDiffIgnoresEditsMadeBeforeTheRun(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	write(t, dir, "tracked.txt", "committed\nmy own work\n")
	write(t, dir, "mine.txt", "also mine\n")

	snap, err := Take(ctx, dir)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}

	changes, err := snap.Diff(ctx)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("got %+v, want nothing attributed to the run", changes)
	}
}

// TestRevertRestoresPreRunContentNotHead is the safety property: a file the
// user had already edited goes back to *their* version.
func TestRevertRestoresPreRunContentNotHead(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	write(t, dir, "tracked.txt", "committed\nmy own work\n")

	snap, err := Take(ctx, dir)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}

	write(t, dir, "tracked.txt", "committed\nmy own work\nagent meddling\n")

	if err := snap.Revert(ctx, "tracked.txt"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got, want := read(t, dir, "tracked.txt"), "committed\nmy own work\n"; got != want {
		t.Errorf("content = %q, want the pre-run version %q", got, want)
	}
}

func TestRevertRestoresACleanFileFromGit(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	snap, err := Take(ctx, dir)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	write(t, dir, "tracked.txt", "rewritten by an agent\n")

	if err := snap.Revert(ctx, "tracked.txt"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got, want := read(t, dir, "tracked.txt"), "committed\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestRevertDeletesAFileTheRunCreated(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	snap, err := Take(ctx, dir)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	write(t, dir, "created.txt", "by an agent\n")

	if err := snap.Revert(ctx, "created.txt"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "created.txt")); !os.IsNotExist(err) {
		t.Errorf("file still present, want it removed (err %v)", err)
	}
}

func TestRevertRefusesPathsOutsideTheRepository(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	snap, err := Take(ctx, dir)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}

	for _, bad := range []string{"", "/etc/passwd", "../escape.txt", "../../etc/passwd"} {
		if err := snap.Revert(ctx, bad); err == nil {
			t.Errorf("Revert(%q) succeeded, want it refused", bad)
		}
	}
}

func TestIsRepo(t *testing.T) {
	ctx := context.Background()
	if dir := newRepo(t); !IsRepo(ctx, dir) {
		t.Errorf("IsRepo(%q) = false, want true", dir)
	}
	if plain := t.TempDir(); IsRepo(ctx, plain) {
		t.Errorf("IsRepo(%q) = true, want false for a plain directory", plain)
	}
}
