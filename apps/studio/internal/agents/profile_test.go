package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three shapes a skill arrives in: a folder holding a SKILL.md, a bare
// markdown file, and a symlink into a shared skills folder -- which is how one
// skill is given to two agents without copying it.
func TestReadSkills(t *testing.T) {
	shared := t.TempDir()
	if err := os.MkdirAll(filepath.Join(shared, "yapyak"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	skills := filepath.Join(dir, SkillsDirName)
	if err := os.MkdirAll(filepath.Join(skills, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(skills, "accessibility.md"), "# a11y\n")
	if err := os.Symlink(filepath.Join(shared, "yapyak"), filepath.Join(skills, "yapyak")); err != nil {
		t.Fatal(err)
	}
	// Not skills: parked by rename, hidden by an editor, and a file that is
	// not a skill document at all.
	if err := os.MkdirAll(filepath.Join(skills, "_parked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skills, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(skills, "notes.txt"), "not a skill\n")

	got := ReadSkills(dir)
	want := []string{"accessibility", "go", "yapyak"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ReadSkills = %v, want %v", got, want)
	}
}

// No skills/ at all is what a folder made by hand looks like until the next
// reload repairs it, and it is not an error.
func TestReadSkillsMissingFolder(t *testing.T) {
	if got := ReadSkills(t.TempDir()); got != nil {
		t.Errorf("ReadSkills = %v, want nil", got)
	}
	if got := ReadSkills(""); got != nil {
		t.Errorf("ReadSkills(\"\") = %v, want nil", got)
	}
}

// A folder-defined specialist: everything the profile shows comes off disk,
// the heading is dropped because the panel already names them, and the line
// Anton routes on is the first line of prose.
func TestReadProfileFromFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, AgentsDirName, "jeff")
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, PersonalityFileName),
		"# Jeff\n\nPerformance engineer who ties stacks together.\n\nYou are Jeff.\n")
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName, "wails"), 0o755); err != nil {
		t.Fatal(err)
	}

	list, err := Scan(rootWithCoordinator(t, root))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var jeff Agent
	for _, a := range list {
		if a.ID == "jeff" {
			jeff = a
		}
	}
	if jeff.ID == "" {
		t.Fatal("jeff not scanned")
	}

	p, err := ReadProfile(jeff)
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if p.Blurb != "Performance engineer who ties stacks together." {
		t.Errorf("blurb = %q", p.Blurb)
	}
	if len(p.Skillset) != 0 {
		t.Errorf("skillset = %v, want none for a folder-defined agent", p.Skillset)
	}
	if strings.Join(p.Skills, ",") != "wails" {
		t.Errorf("skills = %v, want [wails]", p.Skills)
	}
	if strings.HasPrefix(p.Personality, "#") {
		t.Errorf("heading kept: %q", p.Personality)
	}
	if !strings.HasSuffix(p.Personality, "You are Jeff.") {
		t.Errorf("personality = %q, want the whole body", p.Personality)
	}
	if p.Dir != dir {
		t.Errorf("dir = %q, want %q", p.Dir, dir)
	}
}

// An edit made while the panel is open lands on the next open, without a
// reload: the profile reads the file rather than the roster's summary.
func TestReadProfileReadsFileNotSummary(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, PersonalityFileName), "# Dennis\n\nStale line.\n")
	a := Agent{ID: "dennis", Dir: dir}

	if p, _ := ReadProfile(a); p.Blurb != "Stale line." {
		t.Fatalf("blurb = %q", p.Blurb)
	}
	// Summary is what the last scan saw; the file has moved on since.
	a.Summary = "Stale line."
	write(t, filepath.Join(dir, PersonalityFileName), "# Dennis\n\nRefactors chaos into structure.\n")

	p, err := ReadProfile(a)
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if p.Blurb != "Refactors chaos into structure." {
		t.Errorf("blurb = %q, want the line now in the file", p.Blurb)
	}
}

// A built-in keeps its skillset, because that is Work's rather than the
// folder's, and it is what the coordinator routes it on.
func TestReadProfileKeepsBuiltinSkillset(t *testing.T) {
	anton, ok := Default().Get(CoordinatorFolder)
	if !ok {
		t.Fatal("no coordinator")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, PersonalityFileName), "# Anton\n\nCoordinates.\n")
	anton.Dir = dir

	p, err := ReadProfile(anton)
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if len(p.Skillset) == 0 {
		t.Error("skillset dropped")
	}
	if p.Blurb != strings.Join(anton.Skillset, ", ") {
		t.Errorf("blurb = %q, want the skillset", p.Blurb)
	}
}

// An agent with no folder behind them at all: nothing to read, nothing to
// report, and not an error.
func TestReadProfileNoFolder(t *testing.T) {
	p, err := ReadProfile(Agent{ID: "ghost"})
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if p.Dir != "" || p.Skills != nil || p.Personality != "" {
		t.Errorf("read something from nowhere: %+v", p)
	}
}

// A file that is only a heading has no body, which is not the same as having
// no file: the heading is the name, and the name is not a personality.
func TestPersonalityBodyHeadingOnly(t *testing.T) {
	if got := personalityBody("# Chris\n"); got != "" {
		t.Errorf("personalityBody = %q, want empty", got)
	}
	if got := personalityBody("No heading here.\n"); got != "No heading here." {
		t.Errorf("personalityBody = %q", got)
	}
}

// rootWithCoordinator makes the root scannable: Scan refuses a team with no
// coordinator, and these tests are about a specialist.
func rootWithCoordinator(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, AgentsDirName, CoordinatorFolder)
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, PersonalityFileName), "# Anton\n\nCoordinates.\n")
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
