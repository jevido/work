package workbench

import (
	"context"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/ops"
)

// cardTitled finds a card on the board by its title.
func cardTitled(w *Workbench, title string) (board.Card, bool) {
	for _, c := range w.Board() {
		if c.Title == title {
			return c, true
		}
	}
	return board.Card{}, false
}

// The source: a task somebody ordered the outline into shows up as work on the
// board, without a run having invented it.
func TestPlanTasksFeedTheBoard(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "idea-1", Fields: map[string]any{FieldText: "the sync layer"}},
		{Kind: ops.KindExtractToTask, Node: "idea-1", Task: "task-1", Fields: map[string]any{FieldText: "write the outbox"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	card, ok := cardTitled(w, "write the outbox")
	if !ok {
		t.Fatalf("the plan's task is not on the board: %+v", w.Board())
	}
	if card.Status != board.StatusTodo {
		t.Errorf("adopted card status = %q, want %q", card.Status, board.StatusTodo)
	}
	if card.RunID != "" || card.AgentID != "" {
		t.Errorf("adopted card = %+v, want no run and nobody assigned yet", card)
	}
	if got := w.taskForCard(card.ID); got != "task-1" {
		t.Errorf("card %s links to %q, want task-1", card.ID, got)
	}

	// Adopting twice is what every later edit does, and it must not produce a
	// second card for the same task.
	w.adoptTasks()
	if n := len(w.Board()); n != 1 {
		t.Fatalf("board holds %d cards after a second adoption, want 1", n)
	}
}

// The plan owns the words: renaming a task renames its card, because that is
// the sentence somebody wrote in planning.
func TestRenamingATaskRenamesItsCard(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "task-1", Fields: map[string]any{FieldType: TypeTask, FieldText: "first wording"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}
	if _, ok := cardTitled(w, "first wording"); !ok {
		t.Fatalf("task was not adopted: %+v", w.Board())
	}

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindSetFields, Node: "task-1", Fields: map[string]any{FieldText: "better wording"}},
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if _, ok := cardTitled(w, "better wording"); !ok {
		t.Fatalf("the card kept the old wording: %+v", w.Board())
	}
	if n := len(w.Board()); n != 1 {
		t.Fatalf("board holds %d cards after a rename, want 1", n)
	}
}

// The outlet: the run owns the progress. A card that moves writes its column
// onto the task it came from, so planning shows what is actually underway.
func TestBoardProgressWritesBackToThePlan(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "task-1", Fields: map[string]any{FieldType: TypeTask, FieldText: "write the outbox"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}
	card, ok := cardTitled(w, "write the outbox")
	if !ok {
		t.Fatal("task was not adopted")
	}

	s := w.sync.Load()
	w.moveCard(card.ID, board.StatusDoing, "")
	if got := fieldString(mustNode(t, s, "task-1"), FieldStatus); got != TaskDoing {
		t.Errorf("task status = %q after the card started, want %q", got, TaskDoing)
	}

	w.moveCard(card.ID, board.StatusBlocked, "jeff is in position.go")
	task := mustNode(t, s, "task-1")
	if got := fieldString(task, FieldStatus); got != TaskDoing {
		t.Errorf("blocked card put the task at %q, want %q", got, TaskDoing)
	}
	if got := fieldString(task, FieldNote); got != "jeff is in position.go" {
		t.Errorf("the reason it is stuck did not reach the plan: note = %q", got)
	}

	w.moveCard(card.ID, board.StatusDone, "")
	if got := fieldString(mustNode(t, s, "task-1"), FieldStatus); got != TaskDone {
		t.Errorf("task status = %q after the card finished, want %q", got, TaskDone)
	}

	// The card node carries the link, so a viewer can pair the two halves
	// without holding this machine's board IDs.
	cardState := mustNode(t, s, cardNode(s.actor, card.ID))
	if got := fieldString(cardState, ops.FieldTaskID); got != "task-1" {
		t.Errorf("card node taskId = %q, want task-1", got)
	}
}

// A colleague closing a task in planning closes the card here. The reverse --
// a task pulled back to todo under a card that is running -- is deliberately
// not followed, because the run is the thing that knows.
func TestATaskClosedElsewhereClosesItsCard(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "task-1", Fields: map[string]any{FieldType: TypeTask, FieldText: "write the outbox"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}
	card, _ := cardTitled(w, "write the outbox")
	w.moveCard(card.ID, board.StatusDoing, "")

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindSetFields, Node: "task-1", Fields: map[string]any{FieldStatus: TaskDone}},
	}); err != nil {
		t.Fatalf("close the task: %v", err)
	}

	got, ok := cardTitled(w, "write the outbox")
	if !ok {
		t.Fatal("the card went away")
	}
	if got.Status != board.StatusDone {
		t.Fatalf("card status = %q after the task was closed in planning, want %q", got.Status, board.StatusDone)
	}
}

// Clearing the conversation is "start again from the tasks", not "forget what
// we agreed to do". The plan outlives a conversation because it is in the log.
func TestClearConversationKeepsThePlan(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "task-1", Fields: map[string]any{FieldType: TypeTask, FieldText: "write the outbox"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}
	// Plus a card a run invented, which is the half that should not survive.
	w.addCard("r1", "anton", "ephemeral")

	w.ClearConversation()

	if _, ok := cardTitled(w, "write the outbox"); !ok {
		t.Errorf("the plan's task did not come back: %+v", w.Board())
	}
	if _, ok := cardTitled(w, "ephemeral"); ok {
		t.Error("a run-invented card survived clearing the conversation")
	}
}

// The local-only promise. A tab with no folder on this machine cannot run
// anything, so a card for its work would be a card nobody can start.
func TestAnUnboundTabDoesNotFeedTheBoard(t *testing.T) {
	stateHome(t)
	client := newFakeOps()

	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	w.UseOps(client)
	if _, err := w.JoinWorkspace(context.Background(), "https://example.invalid", "wk_test"); err != nil {
		t.Fatalf("join: %v", err)
	}
	tab, err := w.NewTab("unbound")
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}

	// No BindTab and no ActivateTab: the tab exists in the workspace and has
	// nowhere to run here, which is how a colleague's tab arrives.
	if _, err := w.ApplyEdits(tab.ID, []Edit{
		{Kind: ops.KindCreateNode, Node: "task-1", Fields: map[string]any{FieldType: TypeTask, FieldText: "not mine to run"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}

	if n := len(w.Board()); n != 0 {
		t.Fatalf("board holds %d cards for a tab with no folder here, want 0", n)
	}
}

// With zero workspaces joined, nothing in this file may do anything at all.
// Every path here is reached from the board, which every run publishes.
func TestNoWorkspaceMeansNoTaskAdoption(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())

	w.addCard("r1", "anton", "ordinary local work")
	w.adoptTasks()
	w.shareDiscoveries("T1", []Discovery{{Kind: DiscoveryIdea, Text: "found something"}})
	w.ClearConversation()

	if n := len(w.Board()); n != 0 {
		t.Fatalf("board holds %d cards after clearing, want 0", n)
	}
	if w.taskForCard("T1") != "" {
		t.Error("a card was linked to a task with no workspace joined")
	}
}

// The other outlet: what a run found out lands next to the idea the task came
// from, which is where the next planning pass will see it.
func TestDiscoveriesLandBesideTheSourceIdea(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: ops.KindCreateNode, Node: "idea-1", Fields: map[string]any{FieldText: "one queue for both"}},
		{Kind: ops.KindExtractToTask, Node: "idea-1", Task: "task-1", Fields: map[string]any{FieldText: "share the queue"}},
	}); err != nil {
		t.Fatalf("apply edits: %v", err)
	}
	card, ok := cardTitled(w, "share the queue")
	if !ok {
		t.Fatal("task was not adopted")
	}

	w.shareDiscoveries(card.ID, []Discovery{
		{Kind: DiscoveryDeadEnd, Text: "one queue cannot serve both, the locks invert"},
		{Kind: DiscoverySplit, Text: "the reader wants its own path"},
	})

	s := w.sync.Load()
	var found []ops.Node
	for _, node := range s.liveNodes() {
		if fieldString(node, FieldDiscovery) != "" {
			found = append(found, node)
		}
	}
	if len(found) != 2 {
		t.Fatalf("%d discoveries reached the outline, want 2", len(found))
	}
	for _, node := range found {
		if node.Parent != "idea-1" {
			t.Errorf("discovery %s hangs off %q, want the source idea idea-1", node.ID, node.Parent)
		}
		if fieldString(node, FieldType) != TypeIdea {
			t.Errorf("discovery %s is not an outline line", node.ID)
		}
		if fieldString(node, FieldDiscoveredIn) != "task-1" {
			t.Errorf("discovery %s does not say which task found it", node.ID)
		}
	}

	// A step that is handed back after a block reports what it found twice.
	w.shareDiscoveries(card.ID, []Discovery{
		{Kind: DiscoveryDeadEnd, Text: "one queue cannot serve both, the locks invert"},
	})
	repeat := 0
	for _, node := range s.liveNodes() {
		if fieldString(node, FieldDiscovery) != "" {
			repeat++
		}
	}
	if repeat != 2 {
		t.Fatalf("%d discoveries after a repeat, want the duplicate dropped", repeat)
	}
}

// A task nobody extracted, and a run with no task at all, put their findings
// at the top level of the tab rather than nowhere.
func TestDiscoveriesWithNoSourceIdeaLandOnTheTab(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())

	card := w.addCard("r1", "anton", "just a run")
	w.shareDiscoveries(card, []Discovery{{Kind: DiscoveryIdea, Text: "worth measuring on its own"}})

	s := w.sync.Load()
	var placed bool
	for _, node := range s.liveNodes() {
		if fieldString(node, FieldDiscovery) == "" {
			continue
		}
		placed = true
		if node.Parent != tab {
			t.Errorf("discovery hangs off %q, want the tab %q", node.Parent, tab)
		}
		if got := fieldString(node, FieldDiscoveredIn); got != "" {
			t.Errorf("discoveredIn = %q, want empty for a run with no task", got)
		}
	}
	if !placed {
		t.Fatal("the discovery did not reach the outline")
	}
}

// A run can find more than anyone wants to read. The cap is on the run's side
// of the wire because an op that reaches the outbox is durable and shared.
func TestDiscoveriesAreBounded(t *testing.T) {
	w, _ := joinedWorkbench(t, newFakeOps())

	card := w.addCard("r1", "anton", "a run")
	found := make([]Discovery, maxDiscoveries+5)
	for i := range found {
		found[i] = Discovery{Kind: DiscoveryIdea, Text: "finding " + string(rune('a'+i))}
	}
	w.shareDiscoveries(card, found)

	n := 0
	for _, node := range w.sync.Load().liveNodes() {
		if fieldString(node, FieldDiscovery) != "" {
			n++
		}
	}
	if n != maxDiscoveries {
		t.Fatalf("%d discoveries written, want the cap of %d", n, maxDiscoveries)
	}
}

func TestDiscoveriesParsedFromAReply(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   []Discovery
	}{
		{
			name:   "nothing marked",
			output: "I changed three files and the tests pass.",
		},
		{
			name:   "labelled",
			output: "Done.\n\nDISCOVERY: dead-end: one queue cannot serve both\n",
			want:   []Discovery{{Kind: DiscoveryDeadEnd, Text: "one queue cannot serve both"}},
		},
		{
			name:   "unlabelled reads as an idea",
			output: "DISCOVERY: the encode cost is worth measuring",
			want:   []Discovery{{Kind: DiscoveryIdea, Text: "the encode cost is worth measuring"}},
		},
		{
			name:   "an unknown label keeps the sentence whole",
			output: "DISCOVERY: regression: the retry path is wrong",
			want:   []Discovery{{Kind: DiscoveryIdea, Text: "regression: the retry path is wrong"}},
		},
		{
			name:   "markdown around it",
			output: "- **DISCOVERY:** split: the reader wants its own path\n",
			want:   []Discovery{{Kind: DiscoverySplit, Text: "the reader wants its own path"}},
		},
		{
			name: "several, anywhere in the reply",
			output: "DISCOVERY: split: a\nI then did some work.\n" +
				"DISCOVERY: idea: b\nAnd finished.\n",
			want: []Discovery{
				{Kind: DiscoverySplit, Text: "a"},
				{Kind: DiscoveryIdea, Text: "b"},
			},
		},
		{
			name:   "a marker with nothing after it",
			output: "DISCOVERY:\nDISCOVERY: split:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := discoveries(tc.output)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d discoveries %+v, want %d", len(got), got, len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("discovery %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func mustNode(t *testing.T, s *Sync, id string) ops.Node {
	t.Helper()
	node, ok := s.node(id)
	if !ok {
		t.Fatalf("no node %s in the merged document", id)
	}
	return node
}

// plan puts tasks on the active tab's plan, in the order given.
func plan(t *testing.T, w *Workbench, tab string, entries ...[2]string) {
	t.Helper()
	var edits []Edit
	for i, entry := range entries {
		edits = append(edits, Edit{
			Kind:     "extract-to-task",
			Node:     "idea" + string(rune('a'+i)),
			Task:     entry[0],
			Position: string(rune('a' + i)),
			Fields:   map[string]any{FieldType: TypeTask, FieldText: entry[0], FieldStatus: entry[1]},
		})
	}
	if _, err := w.ApplyEdits(tab, edits); err != nil {
		t.Fatalf("plan: %v", err)
	}
}

func TestNextTask(t *testing.T) {
	t.Run("an empty plan offers nothing", func(t *testing.T) {
		w, _ := joinedWorkbench(t, newFakeOps())
		if got, ok := w.NextTask(); ok {
			t.Errorf("offered %+v from an empty plan", got)
		}
	})

	t.Run("the first one still to do", func(t *testing.T) {
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"first", TaskTodo}, [2]string{"second", TaskTodo})

		got, ok := w.NextTask()
		if !ok {
			t.Fatal("nothing offered from a plan with two todos")
		}
		if got.Text != "first" {
			t.Errorf("offered %q, want the first in plan order", got.Text)
		}
	})

	t.Run("a done task is passed over", func(t *testing.T) {
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"finished", TaskDone}, [2]string{"waiting", TaskTodo})

		got, ok := w.NextTask()
		if !ok {
			t.Fatal("nothing offered")
		}
		if got.Text != "waiting" {
			t.Errorf("offered %q, want the one after the finished task", got.Text)
		}
	})

	t.Run("one already started is next, not skipped", func(t *testing.T) {
		// Somebody began it and stopped. Offering the one after it would
		// quietly skip work that is half finished.
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"half done", TaskTodo}, [2]string{"not started", TaskTodo})
		// Set afterwards rather than at creation. A status written in the same
		// batch as the task is overwritten by the board mirror adopting the new
		// card -- which is a real thing and task 03's to deal with.
		if _, err := w.ApplyEdits(tab, []Edit{
			{Kind: "set-fields", Node: "half done", Fields: map[string]any{FieldStatus: TaskDoing}},
		}); err != nil {
			t.Fatalf("set doing: %v", err)
		}

		got, ok := w.NextTask()
		if !ok {
			t.Fatal("nothing offered")
		}
		if got.Text != "half done" {
			t.Errorf("offered %q, want the one already in progress", got.Text)
		}
		if got.Status != TaskDoing {
			t.Errorf("status = %q", got.Status)
		}
	})

	t.Run("everything finished offers nothing", func(t *testing.T) {
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"one", TaskDone}, [2]string{"two", TaskDone})
		if got, ok := w.NextTask(); ok {
			t.Errorf("offered %+v from a finished plan", got)
		}
	})

	t.Run("the idea it came from travels with it", func(t *testing.T) {
		w, tab := joinedWorkbench(t, newFakeOps())
		if _, err := w.ApplyEdits(tab, []Edit{
			{Kind: "create-node", Node: "theidea", Fields: map[string]any{FieldType: TypeIdea, FieldText: "the reason this exists"}},
			{Kind: "extract-to-task", Node: "theidea", Task: "t1", Position: "m",
				Fields: map[string]any{FieldType: TypeTask, FieldText: "do the thing", FieldStatus: TaskTodo}},
		}); err != nil {
			t.Fatalf("plan: %v", err)
		}

		got, ok := w.NextTask()
		if !ok {
			t.Fatal("nothing offered")
		}
		if got.From != "theidea" {
			t.Errorf("from = %q", got.From)
		}
		// The words, not only the id: a task without the reason it exists is a
		// line somebody has to go and look up.
		if got.FromText != "the reason this exists" {
			t.Errorf("fromText = %q", got.FromText)
		}
	})

	t.Run("a task with no words is not offered", func(t *testing.T) {
		// Somebody started typing one and left. It cannot be run and it cannot
		// be shown.
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"", TaskTodo})
		if _, ok := w.NextTask(); ok {
			t.Error("an empty task was offered")
		}
	})
}

// No workspace, no next task. The folder guard beside it is the same condition
// adoptTasks uses -- a task that cannot be run in a folder is not one to offer
// -- and ActivateTab already refuses a tab with no folder, so the unbound case
// cannot be reached from the outside at all.
func TestNextTaskNeedsAWorkspace(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	if got, ok := w.NextTask(); ok {
		t.Errorf("offered %+v with no workspace joined", got)
	}
}

func TestStartNextTaskRefusalsSayWhich(t *testing.T) {
	// Three ways of having nothing to start, and they are not the same news.
	t.Run("no workspace", func(t *testing.T) {
		stateHome(t)
		w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
		_, err := w.StartNextTask()
		if err == nil || !strings.Contains(err.Error(), "no workspace") {
			t.Errorf("error = %v, want one naming the missing workspace", err)
		}
	})

	t.Run("nothing left", func(t *testing.T) {
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"finished", TaskDone})
		_, err := w.StartNextTask()
		if err == nil || !strings.Contains(err.Error(), "nothing left") {
			t.Errorf("error = %v, want one saying the plan is finished", err)
		}
	})
}

func TestTaskPromptCarriesTheIdea(t *testing.T) {
	// A run that sees only the task has lost the reason it is being done.
	with := taskPrompt(PlanTask{Text: "Mount the static handler", FromText: "Serve the viewer"})
	if !strings.Contains(with, "Mount the static handler") {
		t.Error("the prompt does not carry the task")
	}
	if !strings.Contains(with, "Serve the viewer") {
		t.Error("the prompt does not carry the idea it came from")
	}

	// And a task with no link still reads as a request rather than as a task
	// with a missing half.
	without := taskPrompt(PlanTask{Text: "Just do this"})
	if strings.Contains(without, "came out of an idea") {
		t.Errorf("a task with no origin claims one: %q", without)
	}
}

// The two mirrors meeting. Both already refuse to write a value that is already
// there, so this is not about a loop -- it is about a status surviving the
// first time each side sees the other.
func TestStatusSurvivesBothMirrors(t *testing.T) {
	t.Run("a task that is underway gets a card that is underway", func(t *testing.T) {
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"started elsewhere", TaskTodo})
		if _, err := w.ApplyEdits(tab, []Edit{
			{Kind: "set-fields", Node: "started elsewhere", Fields: map[string]any{FieldStatus: TaskDoing}},
		}); err != nil {
			t.Fatal(err)
		}

		card, ok := cardTitled(w, "started elsewhere")
		if !ok {
			t.Fatal("the task never reached the board")
		}
		if card.Status != board.StatusDoing {
			t.Errorf("card status = %q, want doing", card.Status)
		}

		// And the plan still says so, rather than having had todo written back
		// over it by the card it just created.
		for _, task := range tabTasks(w.WorkspaceDocument(), tab) {
			if task.ID != "started elsewhere" {
				continue
			}
			if got := fieldString(task, FieldStatus); got != TaskDoing {
				t.Errorf("the plan says %q after the board saw it, want doing", got)
			}
		}
	})

	t.Run("a task moved back to todo moves its card back", func(t *testing.T) {
		// The direction that used to be one-way: only done was mirrored, so a
		// correction in the plan left the card finished and the next pass wrote
		// done back over the correction.
		w, tab := joinedWorkbench(t, newFakeOps())
		plan(t, w, tab, [2]string{"finished too soon", TaskTodo})
		if _, err := w.ApplyEdits(tab, []Edit{
			{Kind: "set-fields", Node: "finished too soon", Fields: map[string]any{FieldStatus: TaskDone}},
		}); err != nil {
			t.Fatal(err)
		}
		if card, _ := cardTitled(w, "finished too soon"); card.Status != board.StatusDone {
			t.Fatalf("card did not follow the task to done: %q", card.Status)
		}

		if _, err := w.ApplyEdits(tab, []Edit{
			{Kind: "set-fields", Node: "finished too soon", Fields: map[string]any{FieldStatus: TaskTodo}},
		}); err != nil {
			t.Fatal(err)
		}
		if card, _ := cardTitled(w, "finished too soon"); card.Status != board.StatusTodo {
			t.Errorf("card status = %q after the plan moved it back, want todo", card.Status)
		}
		for _, task := range tabTasks(w.WorkspaceDocument(), tab) {
			if task.ID == "finished too soon" && fieldString(task, FieldStatus) != TaskTodo {
				t.Errorf("the plan was overwritten back to %q", fieldString(task, FieldStatus))
			}
		}
	})
}

// Blocked has no opposite. The board has four states and the plan has three, so
// a round trip must not invent one the plan cannot hold.
func TestBlockedRoundTrips(t *testing.T) {
	if got := taskStatus(board.StatusBlocked); got != TaskDoing {
		t.Errorf("a blocked card is %q on the plan, want doing", got)
	}
	if got := cardStatus(TaskDoing); got != board.StatusDoing {
		t.Errorf("doing came back as %q", got)
	}
	for _, status := range []string{TaskTodo, TaskDoing, TaskDone} {
		if round := taskStatus(cardStatus(status)); round != status {
			t.Errorf("%q round-tripped to %q", status, round)
		}
	}
}
