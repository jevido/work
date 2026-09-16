package workbench

import (
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/config"
	"dev.jevido/work/internal/ops"
)

// localWorkbench is a workbench with a workspace that has no server, built
// the way setup builds one.
func localWorkbench(t *testing.T, client OpsClient) (*Workbench, string) {
	t.Helper()
	stateHome(t)

	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	if client != nil {
		w.UseOps(client)
	}
	view, err := w.CreateLocalWorkspace("planning")
	if err != nil {
		t.Fatalf("create local workspace: %v", err)
	}
	if view == nil || len(view.Tabs) != 1 {
		t.Fatalf("view = %+v, want one tab", view)
	}
	return w, view.Tabs[0].ID
}

// The point of the whole thing: a machine that has never seen a server can do
// everything a tab is for. Before this, every one of these answered "no
// workspace joined", which is what made a fresh install look broken.
func TestLocalWorkspaceHasWorkingTabs(t *testing.T) {
	w, first := localWorkbench(t, nil)

	// The tab setup makes is bound to the folder Work started in, so agents
	// have somewhere to run without anybody being asked a second question.
	view := w.Workspace()
	if !view.Tabs[0].Bound {
		t.Error("the tab setup made has no folder")
	}
	if view.ActiveTab != first {
		t.Errorf("activeTab = %q, want %q", view.ActiveTab, first)
	}

	second, err := w.NewTab("review")
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	dir := t.TempDir()
	if err := w.BindTab(second.ID, dir); err != nil {
		t.Fatalf("bind tab: %v", err)
	}
	if err := w.ActivateTab(second.ID); err != nil {
		t.Fatalf("activate tab: %v", err)
	}

	if _, err := w.ApplyEdits(second.ID, []Edit{{
		Kind:   ops.KindCreateNode,
		Node:   "idea-1",
		Fields: map[string]any{FieldText: "make the encode cheaper"},
	}}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	if err := w.CloseTab(first); err != nil {
		t.Fatalf("close tab: %v", err)
	}
	if got := len(w.Workspace().Tabs); got != 1 {
		t.Errorf("%d tabs after closing one of two", got)
	}
}

// A local workspace must not look like a machine that cannot reach its
// server: "offline" invites a retry, and there is nothing to retry.
func TestLocalWorkspaceReportsLocal(t *testing.T) {
	w, _ := localWorkbench(t, nil)

	status := w.SyncStatus()
	if !status.Joined {
		t.Error("joined is false with a workspace open")
	}
	if !status.Local {
		t.Error("local is false for a workspace with no server")
	}
	if status.State != SyncLocal {
		t.Errorf("state = %q, want %q", status.State, SyncLocal)
	}
}

// Nothing drains the outbox without a server, so nothing may be put in it:
// ops that queued forever would climb towards the cap and then start being
// dropped, in front of a user with nothing to send.
func TestLocalWorkspaceQueuesNothing(t *testing.T) {
	w, tab := localWorkbench(t, nil)

	for i := range 5 {
		if _, err := w.ApplyEdits(tab, []Edit{{
			Kind:   ops.KindCreateNode,
			Node:   "idea-" + string(rune('a'+i)),
			Fields: map[string]any{FieldText: "thinking"},
		}}); err != nil {
			t.Fatalf("apply edits: %v", err)
		}
	}

	if got := w.SyncStatus().Pending; got != 0 {
		t.Errorf("pending = %d, want 0 with no server to send to", got)
	}
	if got := w.SyncStatus().Dropped; got != 0 {
		t.Errorf("dropped = %d, want 0", got)
	}
}

// The journal is the only durable record a local workspace has, so a restart
// has to rebuild the document from it alone.
func TestLocalWorkspaceSurvivesRestart(t *testing.T) {
	w, tab := localWorkbench(t, nil)

	if _, err := w.ApplyEdits(tab, []Edit{{
		Kind:   ops.KindCreateNode,
		Node:   "idea-1",
		Fields: map[string]any{FieldText: "survives"},
	}}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.Workspace == nil {
		t.Fatal("the local workspace was not written to the config")
	}
	if saved.Workspace.ServerURL != "" || saved.Workspace.WriteKey != "" {
		t.Errorf("workspace = %+v, want no server and no key", saved.Workspace)
	}

	// A second workbench over the same config home, which is what the next
	// launch is. No transport at all: a local workspace must not need one.
	next := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	if err := next.UseWorkspace(saved.Workspace); err != nil {
		t.Fatalf("restore local workspace: %v", err)
	}
	if _, ok := next.sync.Load().node("idea-1"); !ok {
		t.Error("idea-1 was not rebuilt from the journal")
	}
}
