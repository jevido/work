package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// avatarRoot builds a config root with one agent folder per name given.
func avatarRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(AgentsDir(root), name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// writeAvatar writes a file into an agent's folder and returns the path
// AvatarPath would report for it: resolved, since that is what it returns.
func writeAvatar(t *testing.T, root, id, name string) string {
	t.Helper()
	path := filepath.Join(AgentsDir(root), id, name)
	if err := os.WriteFile(path, []byte("not really an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestAvatarPathFindsBothNames(t *testing.T) {
	for _, name := range avatarNames {
		root := avatarRoot(t, "anton")
		want := writeAvatar(t, root, "anton", name)

		got, err := AvatarPath(root, "anton")
		if err != nil {
			t.Fatalf("AvatarPath(%s): %v", name, err)
		}
		if got != want {
			t.Errorf("AvatarPath(%s) = %q, want %q", name, got, want)
		}
	}
}

// webp wins over png so the office draws the smaller of the two, matching what
// Scan reports as the agent's avatar.
func TestAvatarPathPrefersWebp(t *testing.T) {
	root := avatarRoot(t, "anton")
	want := writeAvatar(t, root, "anton", "avatar.webp")
	writeAvatar(t, root, "anton", "avatar.png")

	got, err := AvatarPath(root, "anton")
	if err != nil {
		t.Fatalf("AvatarPath: %v", err)
	}
	if got != want {
		t.Errorf("AvatarPath = %q, want the webp %q", got, want)
	}
}

// No avatar is the common case: nothing to serve, and nothing wrong.
func TestAvatarPathNoAvatar(t *testing.T) {
	root := avatarRoot(t, "anton")

	got, err := AvatarPath(root, "anton")
	if err != nil {
		t.Fatalf("AvatarPath: %v", err)
	}
	if got != "" {
		t.Errorf("AvatarPath = %q, want empty", got)
	}
}

// A missing agent folder is the same "nothing to draw" as a folder with no
// picture in it. Nothing else in the folder is reachable either.
func TestAvatarPathOnlyServesAvatars(t *testing.T) {
	root := avatarRoot(t, "anton")
	writeAvatar(t, root, "anton", PersonalityFileName)

	for _, id := range []string{"anton", "nobody"} {
		got, err := AvatarPath(root, id)
		if err != nil {
			t.Fatalf("AvatarPath(%q): %v", id, err)
		}
		if got != "" {
			t.Errorf("AvatarPath(%q) = %q, want empty", id, got)
		}
	}
}

func TestAvatarPathRejectsIDs(t *testing.T) {
	root := avatarRoot(t, "anton")
	writeAvatar(t, root, "anton", "avatar.png")
	// A file to try and reach, one level above the agents folder.
	secret := filepath.Join(root, "avatar.png")
	if err := os.WriteFile(secret, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"",
		".",
		"..",
		"../",
		"../..",
		"anton/..",
		"anton/skills",
		"/etc",
		`..\..`,
		".hidden",
		"anton\x00",
		string(make([]byte, maxAgentIDLen+1)),
	}
	for _, id := range bad {
		got, err := AvatarPath(root, id)
		if err == nil {
			t.Errorf("AvatarPath(%q) = %q, want an error", id, got)
		}
		if got != "" {
			t.Errorf("AvatarPath(%q) = %q, want empty", id, got)
		}
	}
}

func TestAvatarPathNoRoot(t *testing.T) {
	if _, err := AvatarPath("  ", "anton"); err == nil {
		t.Error("AvatarPath with no root: want an error")
	}
}

// An agent folder is whatever is on disk, which includes a symlink to
// somewhere else. Following one out of the config folder would turn this into
// a file server for the whole machine.
func TestAvatarPathRefusesEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := avatarRoot(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "avatar.png"), []byte("elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(AgentsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(AgentsDir(root), "sneaky")); err != nil {
		t.Fatal(err)
	}

	got, err := AvatarPath(root, "sneaky")
	if err == nil {
		t.Errorf("AvatarPath = %q, want an error", got)
	}
	if got != "" {
		t.Errorf("AvatarPath = %q, want empty", got)
	}
}

// A symlink that stays inside the config folder is fine: reusing one picture
// for two agents is a reasonable thing to do with a folder of files.
func TestAvatarPathAllowsSymlinkInsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := avatarRoot(t, "anton", "chris")
	shared := writeAvatar(t, root, "anton", "avatar.png")
	if err := os.Symlink(shared, filepath.Join(AgentsDir(root), "chris", "avatar.png")); err != nil {
		t.Fatal(err)
	}

	got, err := AvatarPath(root, "chris")
	if err != nil {
		t.Fatalf("AvatarPath: %v", err)
	}
	if got != shared {
		t.Errorf("AvatarPath = %q, want %q", got, shared)
	}
}
