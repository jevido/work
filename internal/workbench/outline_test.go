package workbench

import (
	"context"
	"fmt"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/ops"
)

// joinedWorkbench is a workbench with a workspace joined against a fake
// server, one tab, bound to a project folder and active.
//
// The folder is deliberately not the workbench's own start directory: an
// unbound tab reports that directory, which is how tabs with nowhere to run
// are detected, so a test that shared them would pass for the wrong reason.
func joinedWorkbench(t *testing.T, client *fakeOps) (*Workbench, string) {
	t.Helper()
	stateHome(t)

	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	w.UseOps(client)
	if _, err := w.JoinWorkspace(context.Background(), "https://example.invalid", "wk_test"); err != nil {
		t.Fatalf("join: %v", err)
	}

	tab, err := w.NewTab("planning")
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	if err := w.BindTab(tab.ID, t.TempDir()); err != nil {
		t.Fatalf("bind tab: %v", err)
	}
	if err := w.ActivateTab(tab.ID); err != nil {
		t.Fatalf("activate tab: %v", err)
	}
	return w, tab.ID
}

// An outline line with no parent belongs at the top level of its own tab, not
// at the root of a document every tab shares. Getting this wrong would put one
// tab's thinking in every other tab's outline.
func TestApplyEditsPlacesOutlineUnderItsTab(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	doc, err := w.ApplyEdits(tab, []Edit{{
		Kind:   ops.KindCreateNode,
		Node:   "idea-1",
		Fields: map[string]any{FieldText: "make the encode cheaper"},
	}})
	if err != nil {
		t.Fatalf("apply edits: %v", err)
	}
	if doc.Cursor != 0 {
		t.Fatalf("cursor moved to %d on a local edit; only a pull advances it", doc.Cursor)
	}

	s := w.sync.Load()
	node, ok := s.node("idea-1")
	if !ok {
		t.Fatal("the edit is not in the merged document")
	}
	if node.Parent != tab {
		t.Errorf("parent = %q, want the tab %q", node.Parent, tab)
	}
	if node.Position == "" {
		t.Error("no position was minted, so siblings have no order")
	}
	if got := fieldString(node, FieldText); got != "make the encode cheaper" {
		t.Errorf("text = %q", got)
	}

	// Durable before visible: the op is in the outbox whether or not anything
	// has reached the server.
	if pending, _ := s.q.depth(); pending == 0 {
		t.Error("the edit is in the document but not in the outbox")
	}
}

// The tab strip and the board are the workbench's own nodes, written from
// state it persists in the config file. A webview reaching them would have the
// two disagreeing with no way to tell which is right.
func TestApplyEditsRefusesTheWorkbenchsOwnNodes(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	// A card, so there is one to try.
	w.board.Add("r1", "anton", "something")
	w.publishBoard()
	card := cardNode(w.sync.Load().actor, "T1")
	if _, ok := w.sync.Load().node(card); !ok {
		t.Fatalf("test setup: no card node %s", card)
	}

	for _, tc := range []struct {
		name string
		edit Edit
	}{
		{"the tab itself", Edit{Kind: ops.KindSetFields, Node: tab, Fields: map[string]any{FieldText: "hijacked"}}},
		{"a board card", Edit{Kind: ops.KindDeleteNode, Node: card}},
		{"a card as a parent", Edit{Kind: ops.KindCreateNode, Node: "x", Parent: card}},
	} {
		if _, err := w.ApplyEdits(tab, []Edit{tc.edit}); err == nil {
			t.Errorf("%s: edit was accepted, want refused", tc.name)
		}
	}
}

// A tab this machine does not have is not a subtree it may write to: the tab
// id is the only thing scoping an edit, so an unchecked one writes anywhere.
func TestApplyEditsRefusesAnUnknownTab(t *testing.T) {
	w, _ := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits("not-a-tab", []Edit{{Kind: ops.KindCreateNode}}); err == nil {
		t.Fatal("an edit to an unknown tab was accepted")
	}
}

// With no workspace joined there is no shared document, and saying so is the
// same answer every other workspace call gives. It must not be a crash and it
// must not be silence.
func TestApplyEditsWithoutAWorkspace(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())

	_, err := w.ApplyEdits("local", []Edit{{Kind: ops.KindCreateNode, Fields: map[string]any{FieldText: "x"}}})
	if err == nil {
		t.Fatal("ApplyEdits succeeded with no workspace joined")
	}
}

// Extracting a line is one op rather than a create plus two field writes, so
// the two ends of the link cannot exist apart -- and the task it produces is
// typed as a task by this layer rather than on the caller's word.
func TestApplyEditsExtractToTaskTypesTheTask(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "idea-1", Fields: map[string]any{FieldText: "cheaper encode"}},
		{Kind: ops.KindExtractToTask, Node: "idea-1", Task: "task-1", Fields: map[string]any{FieldText: "cheaper encode"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	s := w.sync.Load()
	task, ok := s.node("task-1")
	if !ok {
		t.Fatal("the task was not created")
	}
	if !isTask(task) {
		t.Errorf("task node type = %q, want %q", fieldString(task, FieldType), TypeTask)
	}
	if got := fieldString(task, ops.FieldExtractedFrom); got != "idea-1" {
		t.Errorf("extractedFrom = %q, want idea-1", got)
	}
	idea, _ := s.node("idea-1")
	if got := fieldString(idea, ops.FieldTaskID); got != "task-1" {
		t.Errorf("taskId on the idea = %q, want task-1", got)
	}
}

// Two edits in one call that write the same field have to be decided by the
// order they were written in. Sharing a clock would make them concurrent with
// each other, and which one won would depend on nothing.
func TestApplyEditsOrdersOneBatch(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "idea-1", Fields: map[string]any{FieldText: "first"}},
		{Kind: ops.KindSetFields, Node: "idea-1", Fields: map[string]any{FieldText: "second"}},
		{Kind: ops.KindSetFields, Node: "idea-1", Fields: map[string]any{FieldText: "third"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	node, _ := w.sync.Load().node("idea-1")
	if got := fieldString(node, FieldText); got != "third" {
		t.Fatalf("text = %q, want the last edit in the batch to win", got)
	}
}

// A call larger than one disk write is split, not refused.
//
// This used to assert the opposite: over editBatch edits came back as an
// error, and every caller had to know the number and chunk against it.
// Approving one restructuring of a board is hundreds of edits in a single
// gesture, and the frontend chunking to a constant copied from this file was
// the number being in two places -- where the copy that drifts silently drops
// whatever the caller thought it had saved.
func TestApplyEditsSplitsABigCallRatherThanRefusingIt(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	const count = editBatch*2 + 7
	edits := make([]Edit, count)
	for i := range edits {
		edits[i] = Edit{
			Kind:   ops.KindCreateNode,
			Parent: tab,
			Fields: map[string]any{FieldText: fmt.Sprintf("line %d", i)},
		}
	}

	doc, err := w.ApplyEdits(tab, edits)
	if err != nil {
		t.Fatalf("a call of %d edits was refused: %v", count, err)
	}

	var lines int
	for _, root := range doc.Tree {
		if root.ID == tab {
			lines = len(root.Children)
		}
	}
	if lines != count {
		t.Errorf("the tab has %d lines, want all %d", lines, count)
	}
}

// One bad edit anywhere refuses the whole call, however large. Everything is
// planned before anything is written, so a call is not half applied because
// the mistake was at the end of it.
func TestABadEditRefusesTheWholeCall(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	edits := make([]Edit, editBatch+2)
	for i := range edits {
		edits[i] = Edit{
			Kind:   ops.KindCreateNode,
			Parent: tab,
			Fields: map[string]any{FieldText: "fine"},
		}
	}
	// A set-fields naming no node, past the first batch boundary.
	edits[editBatch+1] = Edit{Kind: ops.KindSetFields, Fields: map[string]any{FieldText: "x"}}

	if _, err := w.ApplyEdits(tab, edits); err == nil {
		t.Fatal("a call with a bad edit in it was accepted")
	}
	doc := w.WorkspaceDocument()
	for _, root := range doc.Tree {
		if root.ID == tab && len(root.Children) != 0 {
			t.Errorf("the tab has %d lines, want none written", len(root.Children))
		}
	}
}

// A task nested under an idea is still a task. A plan that dropped one because
// somebody indented the line it came from would be a plan that lies about what
// is left to do.
func TestTabTasksReadsTheWholeSubtree(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "idea-1", Fields: map[string]any{FieldText: "parent idea"}},
		{Kind: ops.KindCreateNode, Node: "task-top", Fields: map[string]any{FieldType: TypeTask, FieldText: "top level"}},
		{Kind: ops.KindCreateNode, Node: "task-deep", Parent: "idea-1", Fields: map[string]any{FieldType: TypeTask, FieldText: "nested"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	tasks := tabTasks(w.sync.Load().document(), tab)
	found := map[string]bool{}
	for _, task := range tasks {
		found[task.ID] = true
	}
	if !found["task-top"] || !found["task-deep"] {
		t.Fatalf("tabTasks returned %v, want both the top-level and the nested task", found)
	}
	if found["idea-1"] {
		t.Error("an idea was read as a task")
	}
}

// The board has four columns and the plan has three. Blocked is started and
// not finished, which is what doing means; calling it todo would say nobody
// had picked it up.
func TestTaskStatusMapsTheBoardsFourColumns(t *testing.T) {
	for _, tc := range []struct {
		card board.Status
		want string
	}{
		{board.StatusTodo, TaskTodo},
		{board.StatusDoing, TaskDoing},
		{board.StatusDone, TaskDone},
		{board.StatusBlocked, TaskDoing},
		{board.Status("from-a-newer-release"), TaskDoing},
	} {
		if got := taskStatus(tc.card); got != tc.want {
			t.Errorf("taskStatus(%q) = %q, want %q", tc.card, got, tc.want)
		}
	}
}
