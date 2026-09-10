package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/config"
)

// newTestWorkbench builds a workbench that never runs anything: these tests
// only exercise the registry and its bookkeeping.
func newTestWorkbench(t *testing.T) *Workbench {
	t.Helper()
	return New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
}

// Reloading before a folder has been chosen is the first-run state, and has to
// say so rather than quietly doing nothing.
func TestReloadAgentsWithoutConfigFolder(t *testing.T) {
	w := newTestWorkbench(t)
	if w.ConfigRoot() != "" {
		t.Fatalf("ConfigRoot = %q, want empty", w.ConfigRoot())
	}
	if _, err := w.ReloadAgents(); err == nil {
		t.Fatal("want an error with no config folder")
	}
}

// The whole point of the reload: an agent folder added in an editor becomes a
// colleague without a restart, and one that is deleted stops being one.
func TestReloadAgentsPicksUpFolderChanges(t *testing.T) {
	w := newTestWorkbench(t)
	root := t.TempDir()

	list, err := w.UseConfigRoot(root)
	if err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d agents, want only the coordinator", len(list))
	}
	if w.ConfigRoot() != root {
		t.Errorf("ConfigRoot = %q, want %q", w.ConfigRoot(), root)
	}

	added := filepath.Join(root, agents.AgentsDirName, "scribe")
	if err := os.Mkdir(added, 0o755); err != nil {
		t.Fatal(err)
	}
	list, err = w.ReloadAgents()
	if err != nil {
		t.Fatalf("ReloadAgents: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d agents after adding a folder, want 2", len(list))
	}

	// A brand new agent must be idle and dispatchable, not missing from the
	// state map and therefore stuck in the zero state.
	var scribe *AgentStatus
	for i := range list {
		if list[i].ID == "scribe" {
			scribe = &list[i]
		}
	}
	if scribe == nil {
		t.Fatal("added folder is not in the roster")
	}
	if scribe.State != StateIdle {
		t.Errorf("State = %q, want %q", scribe.State, StateIdle)
	}
	if _, ok := w.registry.Get("scribe"); !ok {
		t.Error("added agent is not in the registry, so nothing can be routed to them")
	}

	if err := os.RemoveAll(added); err != nil {
		t.Fatal(err)
	}
	if list, err = w.ReloadAgents(); err != nil {
		t.Fatalf("ReloadAgents after removal: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d agents after removing a folder, want 1", len(list))
	}
}

// Bookkeeping is reconciled, not reset: a surviving agent keeps the session
// they have been talking in, a departed one leaves nothing behind.
func TestReloadAgentsReconcilesSessions(t *testing.T) {
	w := newTestWorkbench(t)
	root := t.TempDir()
	if _, err := w.UseConfigRoot(root); err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}

	gone := filepath.Join(root, agents.AgentsDirName, "temp")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReloadAgents(); err != nil {
		t.Fatalf("ReloadAgents: %v", err)
	}

	w.rememberSession(runSession("anton"), "session-anton")
	w.rememberSession(runSession("temp"), "session-temp")

	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReloadAgents(); err != nil {
		t.Fatalf("ReloadAgents after removal: %v", err)
	}

	if got := w.sessionFor(runSession("anton")); got != "session-anton" {
		t.Errorf("surviving agent lost their session: %q", got)
	}
	if got := w.sessionFor(runSession("temp")); got != "" {
		t.Errorf("departed agent kept a session: %q", got)
	}
}

// PERSONALITY.md is appended to the agent's own prompt, and read at dispatch
// so an edit lands on the next task rather than the next restart.
func TestSystemPromptReadsPersonalityFresh(t *testing.T) {
	w := newTestWorkbench(t)
	root := t.TempDir()
	if _, err := w.UseConfigRoot(root); err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}

	anton, ok := w.registry.Get(agents.CoordinatorFolder)
	if !ok {
		t.Fatal("no coordinator")
	}
	path := filepath.Join(anton.Dir, agents.PersonalityFileName)

	if err := os.WriteFile(path, []byte("Answer only in haiku."), 0o644); err != nil {
		t.Fatal(err)
	}
	got := w.systemPrompt(anton)
	if !strings.Contains(got, anton.SystemPrompt) {
		t.Error("built-in prompt was dropped")
	}
	if !strings.Contains(got, "Answer only in haiku.") {
		t.Errorf("personality was not injected: %q", got)
	}

	if err := os.WriteFile(path, []byte("Answer only in limerick."), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := w.systemPrompt(anton); !strings.Contains(got, "limerick") {
		t.Errorf("personality was cached: %q", got)
	}
}

// An agent with no folder behind them still runs on their built-in prompt.
// Registering one directly is the point: it is the state of any agent Work
// knows about but the config root has not been scanned for yet.
func TestSystemPromptWithoutAFolder(t *testing.T) {
	w := newTestWorkbench(t)
	a := agents.Agent{
		ID:           "ada",
		Name:         "Ada",
		Role:         agents.RoleSpecialist,
		SystemPrompt: "You are Ada. You own compilers.",
	}
	w.registry.Replace([]agents.Agent{a})

	got, ok := w.registry.Get("ada")
	if !ok {
		t.Fatal("agent not registered")
	}
	if prompt := w.systemPrompt(got); prompt != a.SystemPrompt {
		t.Errorf("got %q, want the built-in prompt", prompt)
	}
}

// Right after setup the office holds only Anton, so a task must still run --
// on the coordinator himself -- instead of failing for want of specialists.
func TestExecuteRoutedWithNoSpecialists(t *testing.T) {
	w := newTestWorkbench(t)
	root := t.TempDir()
	list, err := w.UseConfigRoot(root)
	if err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d agents, want only the coordinator", len(list))
	}
	if _, err := planSchema(w.registry); err == nil {
		t.Fatal("planSchema should refuse a roster with no specialists")
	}
	if got := specialistIDs(w.registry); len(got) != 0 {
		t.Fatalf("specialistIDs = %v, want none", got)
	}
}

// isolateConfig points config.Load and config.Save at a temporary home, so a
// test that changes the permission mode does not rewrite the developer's own
// config file.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// Both are set because os.UserConfigDir consults XDG_CONFIG_HOME on Linux
	// and derives from HOME everywhere else.
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

// A fresh workbench runs in a mode that lets agents act. Inheriting the user's
// own Claude configuration was the old behaviour, and headless it meant an
// agent that could read a project and change nothing in it.
func TestDefaultPermissionModeCanAct(t *testing.T) {
	w := newTestWorkbench(t)
	if got := w.PermissionMode(); got != claude.DefaultPermission {
		t.Errorf("PermissionMode = %q, want %q", got, claude.DefaultPermission)
	}
	if got := w.PermissionMode(); got == "" {
		t.Error("empty mode inherits the user's own config, which cannot act")
	}
}

// The mode is what a dispatch runs under, and it survives being changed.
func TestSetPermissionMode(t *testing.T) {
	isolateConfig(t)
	w := newTestWorkbench(t)

	if _, err := w.SetPermissionMode(string(claude.PermissionRead)); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	if got := w.PermissionMode(); got != claude.PermissionRead {
		t.Errorf("PermissionMode = %q, want %q", got, claude.PermissionRead)
	}
	// Persisted, so the next window opens in the mode this one was left in.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.PermissionMode != string(claude.PermissionRead) {
		t.Errorf("saved mode = %q, want %q", cfg.PermissionMode, claude.PermissionRead)
	}

	// A mode nothing accepts changes nothing.
	if _, err := w.SetPermissionMode("dontAsk"); err == nil {
		t.Error("want an error for a mode Work does not offer")
	}
	if got := w.PermissionMode(); got != claude.PermissionRead {
		t.Errorf("mode changed on a rejected value: %q", got)
	}
}

// Choosing a config folder must not drop the permission mode: both live in the
// same file, and Save writes the whole of it.
func TestSetConfigRootKeepsPermissionMode(t *testing.T) {
	isolateConfig(t)
	w := newTestWorkbench(t)
	if _, err := w.SetPermissionMode(string(claude.PermissionAll)); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}

	root := t.TempDir()
	if err := agents.Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := w.SetConfigRoot(root); err != nil {
		t.Fatalf("SetConfigRoot: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Root != root {
		t.Errorf("saved root = %q, want %q", cfg.Root, root)
	}
	if cfg.PermissionMode != string(claude.PermissionAll) {
		t.Errorf("saved mode = %q, want it kept", cfg.PermissionMode)
	}
}

// An agent whose own definition sets a mode keeps it: the toggle is the answer
// for everybody who has not been narrowed by hand.
func TestPermissionForAgentOverride(t *testing.T) {
	w := newTestWorkbench(t)
	w.UsePermissionMode(claude.PermissionAll)

	if got := w.permissionFor(agents.Agent{ID: "anton"}); got != string(claude.PermissionAll) {
		t.Errorf("permissionFor = %q, want the workbench mode", got)
	}
	narrowed := agents.Agent{ID: "chris", PermissionMode: string(claude.PermissionRead)}
	if got := w.permissionFor(narrowed); got != string(claude.PermissionRead) {
		t.Errorf("permissionFor = %q, want the agent's own mode", got)
	}
}

// TestSystemPromptNamesTheTeamToTheCoordinator is the regression the office
// display exposed: Anton was told who works here only on the routing turn, so
// asked who was on the team on any other turn -- answering a task himself,
// synthesising, or on the side channel -- he answered from nothing and
// contradicted the desks the user was looking at.
func TestSystemPromptNamesTheTeamToTheCoordinator(t *testing.T) {
	w := newTestWorkbench(t)
	root := t.TempDir()
	// MkdirAll rather than Mkdir: agents/ does not exist until Ensure runs,
	// and these folders are here before it does.
	for _, name := range []string{"chris", "dennis", "jeff"} {
		if err := os.MkdirAll(
			filepath.Join(root, agents.AgentsDirName, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.UseConfigRoot(root); err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}

	lead, ok := w.registry.Coordinator()
	if !ok {
		t.Fatal("no coordinator")
	}
	got := w.systemPrompt(lead)
	for _, want := range []string{"Chris", "Dennis", "Jeff"} {
		if !strings.Contains(got, want) {
			t.Errorf("the coordinator is not told about %s:\n%s", want, got)
		}
	}

	// A specialist carries no roster: they are not asked to route, and every
	// turn would pay for the list in tokens.
	other, ok := w.registry.Get("chris")
	if !ok {
		t.Fatal("chris is not in the registry")
	}
	if strings.Contains(w.systemPrompt(other), "Your specialists") {
		t.Error("a specialist is billed for the roster")
	}
}
