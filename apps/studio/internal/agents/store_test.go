package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A fresh root gets the two guaranteed agents and the starter colleagues that
// ship beside them.
//
// The two are guaranteed for two different reasons -- Scan cannot assemble a
// team without a coordinator, and the orientation and isolation modes would
// otherwise be led by him. The other three are a starting point rather than
// part of the product: they are written once, into a root that has never had an
// agents folder, and never again. Deleting one is how somebody says they did
// not want it.
func TestEnsureSeedsTheBuiltInTeam(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	for _, folder := range []string{CoordinatorFolder, ThinkerFolder} {
		dir := filepath.Join(root, AgentsDirName, folder)
		if _, err := os.Stat(filepath.Join(dir, PersonalityFileName)); err != nil {
			t.Errorf("%s has no %s: %v", folder, PersonalityFileName, err)
		}
		if info, err := os.Stat(filepath.Join(dir, SkillsDirName)); err != nil || !info.IsDir() {
			t.Errorf("%s has no %s directory: %v", folder, SkillsDirName, err)
		}
	}

	// The personality file is the person's document, not a copy of the system
	// prompt. Seeding it from the prompt sent Claude its own instructions twice
	// on every turn, for the life of the folder.
	seeded, err := os.ReadFile(
		filepath.Join(root, AgentsDirName, CoordinatorFolder, PersonalityFileName))
	if err != nil {
		t.Fatal(err)
	}
	built, _ := Default().Get(CoordinatorFolder)
	if strings.Contains(string(seeded), built.SystemPrompt) {
		t.Error("the starter personality is a copy of the system prompt")
	}

	entries, err := os.ReadDir(filepath.Join(root, AgentsDirName))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// A fresh root holds the two guaranteed agents, the starter colleagues
	// that ship with them, and the template to copy for the next one.
	want := []string{TemplateFolderName, CoordinatorFolder, ThinkerFolder, "chris", "dennis", "jeff"}
	if len(names) != len(want) {
		t.Errorf("seeded %v, want %v", names, want)
	}
	for _, folder := range want {
		if !slices.Contains(names, folder) {
			t.Errorf("seeded %v, with no %s in it", names, folder)
		}
	}
	list, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(list) != len(want)-1 {
		t.Fatalf("scanned %d agents, want %d -- everything but the template",
			len(list), len(want)-1)
	}
	// Order matters: the office draws the roster in the order Scan returns it,
	// and the coordinator sits at the front of the room.
	if list[0].ID != CoordinatorFolder {
		t.Errorf("scanned %s first, want %s", list[0].ID, CoordinatorFolder)
	}
	if !slices.ContainsFunc(list, func(a Agent) bool { return a.ID == ThinkerFolder }) {
		t.Errorf("scanned %+v, with no %s in it", list, ThinkerFolder)
	}

	// A fresh root can route, which is a different property from having two
	// folders in it: the routing schema needs at least one non-coordinator to
	// enumerate, and until now a fresh root had none.
	fresh := NewRegistry(list...)
	if lead, ok := fresh.For("orientation"); !ok || lead.ID != ThinkerFolder {
		t.Errorf("For(orientation) = %+v, %v; want %s", lead, ok, ThinkerFolder)
	}
	if lead, ok := fresh.For("implementation"); !ok || lead.ID != CoordinatorFolder {
		t.Errorf("For(work) = %+v, %v; want %s", lead, ok, CoordinatorFolder)
	}
}

// The template is documentation of the layout as it currently is, so unlike a
// personality it is Work's file rather than the user's: restored when deleted
// and refreshed when stale. Anyone who wanted to keep an edit has copied the
// folder, which is what it is for.
func TestEnsureWritesACopyableTemplate(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	dir := filepath.Join(root, AgentsDirName, TemplateFolderName)
	path := filepath.Join(dir, PersonalityFileName)

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no template personality: %v", err)
	}
	// The one thing someone copying it has to be told, since it is all Anton
	// reads when he routes.
	if !strings.Contains(string(body), "first line of prose") {
		t.Error("template does not say what the first prose line is for")
	}
	// An unedited copy still gets routed, so its blurb has to say that it is
	// unedited rather than leak a line of these instructions into the roster.
	if got := summarise(string(body)); !strings.Contains(got, "Unedited template") {
		t.Errorf("blurb of an unedited copy = %q", got)
	}
	if info, err := os.Stat(filepath.Join(dir, SkillsDirName)); err != nil || !info.IsDir() {
		t.Errorf("no %s directory in the template: %v", SkillsDirName, err)
	}

	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if body, err = os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if string(body) == "stale" {
		t.Error("a stale template survived Ensure")
	}
}

// Copying the template is the documented way to add an agent, so the copy has
// to come back from Scan as an ordinary, routable specialist.
func TestScanAcceptsACopiedTemplate(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	dir := filepath.Join(root, AgentsDirName)
	if err := os.MkdirAll(filepath.Join(dir, "ada", SkillsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Ada\n\nOwns compilers and correctness proofs.\n"
	if err := os.WriteFile(filepath.Join(dir, "ada", PersonalityFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var ada Agent
	for _, a := range list {
		if a.ID == "ada" {
			ada = a
		}
	}
	if ada.ID == "" {
		t.Fatal("a copied template folder did not become an agent")
	}
	if ada.Role != RoleSpecialist {
		t.Errorf("Role = %q, want %q", ada.Role, RoleSpecialist)
	}
	// With no built-in skillset the blurb is the personality's first prose
	// line, which is the whole reason the template makes a point of it.
	if ada.Blurb() != "Owns compilers and correctness proofs." {
		t.Errorf("Blurb = %q", ada.Blurb())
	}
	if ada.Colour == "" {
		t.Error("no fallback colour")
	}
}

// A one-agent office is what a roster somebody has edited down to looks like,
// so Anton alone must be a scannable, seatable team rather than an edge case --
// and the modes he does not own have to fall back to him rather than stop.
func TestScanCoordinatorAlone(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// Deleted rather than never seeded: Ensure puts the built-in team back on
	// every call, so this is the state of a root somebody has pruned and then
	// reopened, which is the one that has to keep working. The starters go too
	// -- they are a starting point, and this is somebody who cleared it.
	for _, folder := range []string{ThinkerFolder, "chris", "dennis", "jeff"} {
		if err := os.RemoveAll(filepath.Join(root, AgentsDirName, folder)); err != nil {
			t.Fatal(err)
		}
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

	pruned := NewRegistry(list...)
	for _, mode := range []string{"orientation", "isolation", "implementation"} {
		lead, ok := pruned.For(mode)
		if !ok || lead.ID != CoordinatorFolder {
			t.Errorf("For(%q) = %+v, %v; want the coordinator to stand in", mode, lead, ok)
		}
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
	// The two built-ins, the three starters, and the hand-made one.
	if len(list) != 6 {
		t.Fatalf("got %d agents, want 6 (two built-ins, three starters, one hand-made)", len(list))
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

// Anton's role, colour, planning model and skillset are Work's rather than the
// user's: Scan cannot assemble a team without a coordinator. Deleting his
// folder and making it again therefore has to give the coordinator back, not a
// fresh specialist who happens to be called Anton.
func TestScanRestoresTheCoordinator(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	dir := filepath.Join(root, AgentsDirName, CoordinatorFolder)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	anton := byID[CoordinatorFolder]
	if anton.Role != RoleCoordinator {
		t.Errorf("Role = %q, want %q", anton.Role, RoleCoordinator)
	}
	if anton.Colour != "#f2b544" {
		t.Errorf("Colour = %q", anton.Colour)
	}
	if len(anton.Skillset) == 0 || anton.SystemPrompt == "" {
		t.Error("built-in skillset or system prompt lost")
	}
	if anton.Dir == "" {
		t.Error("Dir not set, so PERSONALITY.md would never be read")
	}
	if anton.PlanModel != "sonnet" {
		t.Error("coordinator lost its planning model")
	}
}

// Without a coordinator there is nobody to hand a task to, so a scan that
// finds none has to fail rather than return a team that cannot work.
func TestScanRequiresCoordinator(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, AgentsDirName, "ada"), 0o755); err != nil {
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

// The specialist rows have to keep the positions the office was drawn for, and
// the coordinator has to keep the one the task board behind their desk was
// sized to. Default() is one agent now, so the three-desk arrangement is
// spelled out here rather than taken from the shipped team.
func TestAssignDesksMatchesTheDrawnOffice(t *testing.T) {
	list := []Agent{
		{ID: "anton", Role: RoleCoordinator},
		{ID: "first", Role: RoleSpecialist},
		{ID: "second", Role: RoleSpecialist},
	}
	AssignDesks(list)

	want := map[string]Desk{
		"anton":  {X: 500, Y: 172, SeatX: 500, SeatY: 240},
		"first":  {X: 320, Y: 430, SeatX: 320, SeatY: 498},
		"second": {X: 680, Y: 430, SeatX: 680, SeatY: 498},
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

// The stand-in team is used before the user has picked a config root, so it
// has to sit where the office is drawn rather than at a position copied into
// the built-in definition and left behind.
func TestDefaultTeamIsPlacedInTheOffice(t *testing.T) {
	placed := Default()
	anton, ok := placed.Coordinator()
	if !ok {
		t.Fatal("the default team has no coordinator")
	}

	list := []Agent{{ID: "anton", Role: RoleCoordinator}}
	AssignDesks(list)
	if anton.Desk != list[0].Desk {
		t.Fatalf("Default() desk = %+v, AssignDesks = %+v", anton.Desk, list[0].Desk)
	}
}

// A starter colleague is a starting point, not part of the product: deleting
// one is how somebody says they did not want it, and the next start must not
// hand it back. The two guaranteed agents behave the opposite way, and that
// difference is the whole reason the seeding is split in two.
func TestStartersAreNotRestoredAfterDeletion(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	chris := filepath.Join(root, AgentsDirName, "chris")
	if _, err := os.Stat(chris); err != nil {
		t.Fatalf("a fresh root has no starter to delete: %v", err)
	}
	if err := os.RemoveAll(chris); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, AgentsDirName, ThinkerFolder)); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if _, err := os.Stat(chris); err == nil {
		t.Error("a deleted starter came back")
	}
	if _, err := os.Stat(filepath.Join(root, AgentsDirName, ThinkerFolder)); err != nil {
		t.Errorf("the guaranteed thinker did not come back: %v", err)
	}
}

// An edited starter is the user's document from the moment it is on disk.
func TestStartersAreNeverRewritten(t *testing.T) {
	root := t.TempDir()
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	path := filepath.Join(root, AgentsDirName, "dennis", PersonalityFileName)
	if err := os.WriteFile(path, []byte("# Dennis\n\nMine now.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Dennis\n\nMine now.\n" {
		t.Errorf("an edited starter was rewritten:\n%s", body)
	}
}
