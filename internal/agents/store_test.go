package agents

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh root gets the coordinator and nobody else: the team is the user's
// to author, and a folder they did not create is a folder they did not ask for.
func TestEnsureSeedsCoordinatorOnly(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	dir := filepath.Join(root, AgentsDirName, CoordinatorFolder)
	if _, err := os.Stat(filepath.Join(dir, PersonalityFileName)); err != nil {
		t.Errorf("no %s: %v", PersonalityFileName, err)
	}
	if info, err := os.Stat(filepath.Join(dir, SkillsDirName)); err != nil || !info.IsDir() {
		t.Errorf("no %s directory: %v", SkillsDirName, err)
	}

	entries, err := os.ReadDir(filepath.Join(root, AgentsDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("seeded %v, want only %s", names, CoordinatorFolder)
	}
}

// A one-agent office is the state right after setup, so Anton alone must be a
// scannable, seatable team rather than an edge case.
func TestScanCoordinatorAlone(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	list, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(list) != 1 || list[0].ID != CoordinatorFolder {
		t.Fatalf("Scan = %+v, want just the coordinator", list)
	}
	AssignDesks(list)
	if list[0].Desk.X == 0 && list[0].Desk.Y == 0 {
		t.Error("coordinator got no desk")
	}
}

// A folder created by hand -- in an editor, with nothing but a name -- is a
// complete agent after Ensure and a routable one after Scan.
func TestScanPicksUpHandMadeFolder(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	dir := filepath.Join(root, AgentsDirName, "data-wrangler")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, PersonalityFileName),
		[]byte("# Wrangler\n\nOwns schemas and migrations.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "avatar.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure runs before every Scan, and is what completes the folder.
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}

	list, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d agents, want 2", len(list))
	}
	if list[0].ID != "anton" || list[0].Role != RoleCoordinator {
		t.Errorf("coordinator is not first: %+v", list[0])
	}

	var got Agent
	for _, a := range list {
		if a.ID == "data-wrangler" {
			got = a
		}
	}
	if got.ID == "" {
		t.Fatal("hand-made folder was not scanned")
	}
	if got.Name != "Data wrangler" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Role != RoleSpecialist {
		t.Errorf("Role = %q", got.Role)
	}
	if got.Blurb() != "Owns schemas and migrations." {
		t.Errorf("Blurb = %q", got.Blurb())
	}
	if got.Avatar != filepath.Join(dir, "avatar.png") {
		t.Errorf("Avatar = %q", got.Avatar)
	}
	if got.Colour == "" {
		t.Error("no colour assigned")
	}
	if info, err := os.Stat(filepath.Join(dir, SkillsDirName)); err != nil || !info.IsDir() {
		t.Errorf("skills directory not created: %v", err)
	}
}

// Built-in agents keep the structure Work gives them; the folder only supplies
// the personality. Recreating one is therefore a mkdir -- the folder name is
// enough to get Work's colour, skillset and prompt back.
func TestScanKeepsBuiltInStructure(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, AgentsDirName, "jeff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	list, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	byID := make(map[string]Agent, len(list))
	for _, a := range list {
		byID[a.ID] = a
	}
	jeff := byID["jeff"]
	if jeff.Colour != "#5bc8a0" {
		t.Errorf("Colour = %q", jeff.Colour)
	}
	if len(jeff.Skillset) == 0 || jeff.SystemPrompt == "" {
		t.Error("built-in skillset or system prompt lost")
	}
	if jeff.Dir == "" {
		t.Error("Dir not set, so PERSONALITY.md would never be read")
	}
	if byID["anton"].PlanModel != "sonnet" {
		t.Error("coordinator lost its planning model")
	}
}

// Without a coordinator there is nobody to hand a task to, so a scan that
// finds none has to fail rather than return a team that cannot work.
func TestScanRequiresCoordinator(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, AgentsDirName, "jeff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(root); err == nil {
		t.Fatal("want an error when there is no coordinator")
	}
}

// Hidden folders are not colleagues.
func TestScanSkipsHiddenFolders(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, AgentsDirName, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, a := range list {
		if a.ID == ".git" {
			t.Fatal(".git became an agent")
		}
	}
}

// ReadPersonality is called on every dispatch, so an edit has to be visible
// without a reload.
func TestReadPersonalityIsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PersonalityFileName)
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadPersonality(dir); got != "first" {
		t.Fatalf("got %q", got)
	}
	if err := os.WriteFile(path, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadPersonality(dir); got != "second" {
		t.Errorf("stale read: %q", got)
	}
}

// A missing personality file is normal, not an error.
func TestReadPersonalityMissing(t *testing.T) {
	got, err := ReadPersonality(t.TempDir())
	if err != nil || got != "" {
		t.Errorf("got %q, %v", got, err)
	}
}

// An oversized file is truncated rather than billed for on every task.
func TestReadPersonalityIsCapped(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, MaxPersonalityBytes*2)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, PersonalityFileName), big, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPersonality(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxPersonalityBytes {
		t.Errorf("len = %d, want %d", len(got), MaxPersonalityBytes)
	}
}

// The generated layout has to reproduce the hand-placed office for the team
// Work ships, or every existing screenshot and expectation shifts.
func TestAssignDesksMatchesTheBuiltInOffice(t *testing.T) {
	list := Default().All()
	AssignDesks(list)

	want := map[string]Desk{
		"anton": {X: 500, Y: 140, SeatX: 500, SeatY: 208},
		"jeff":  {X: 320, Y: 430, SeatX: 320, SeatY: 498},
		"chris": {X: 680, Y: 430, SeatX: 680, SeatY: 498},
	}
	for _, a := range list {
		if got := a.Desk; got != want[a.ID] {
			t.Errorf("%s desk = %+v, want %+v", a.ID, got, want[a.ID])
		}
	}
}

// Desks have to stay inside the walkable floor however many agents there are,
// or somebody ends up seated in a wall.
func TestAssignDesksStayInTheRoom(t *testing.T) {
	for n := 1; n <= 16; n++ {
		list := make([]Agent, n+1)
		list[0] = Agent{ID: "anton", Role: RoleCoordinator}
		for i := 1; i <= n; i++ {
			list[i] = Agent{Role: RoleSpecialist}
		}
		AssignDesks(list)

		for _, a := range list {
			if a.Desk.X < 140 || a.Desk.X > 860 {
				t.Errorf("n=%d: desk x %v outside the room", n, a.Desk.X)
			}
			if a.Desk.SeatY < 90 || a.Desk.SeatY > 650 {
				t.Errorf("n=%d: seat y %v outside the floor", n, a.Desk.SeatY)
			}
		}
	}
}
