package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
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

	w.rememberSession("anton", "session-anton")
	w.rememberSession("temp", "session-temp")

	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReloadAgents(); err != nil {
		t.Fatalf("ReloadAgents after removal: %v", err)
	}

	if got := w.sessionFor("anton"); got != "session-anton" {
		t.Errorf("surviving agent lost their session: %q", got)
	}
	if got := w.sessionFor("temp"); got != "" {
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
func TestSystemPromptWithoutAFolder(t *testing.T) {
	w := newTestWorkbench(t)
	jeff, ok := w.registry.Get("jeff")
	if !ok {
		t.Fatal("no jeff")
	}
	if got := w.systemPrompt(jeff); got != jeff.SystemPrompt {
		t.Errorf("got %q, want the built-in prompt", got)
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
