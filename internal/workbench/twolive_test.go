package workbench

import (
	"encoding/json"
	"net/http"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/ops"
)

// Two desktops on one workspace, through a real server.
//
// Every other test in this phase drives one replica against a harness standing
// in for the other. That is the right way to test each piece and it cannot test
// the thing the phase is for: two machines disagreeing over a network and
// arriving at the same document with both people told what happened.
//
// The specific risk is order. Ops reach each side in a different sequence
// depending on who had network when, and every rule here is about which stamp
// beats which -- so a bug does not look like an error, it looks like two people
// staring at two different outlines. Which is why every case ends by comparing
// the merged documents rather than by looking at one.

// pair stands two workbenches up on one workspace and returns them with the tab
// they share and a reader for each one's conflicts.
func pair(t *testing.T, name string) (a, b *Workbench, tab string, heardA, heardB *conflicts) {
	t.Helper()
	serverURL, signupToken := liveServer(t)
	created := liveCreate(t, serverURL, signupToken, name)

	heardA, heardB = &conflicts{}, &conflicts{}
	a = liveWorkbenchHearing(t, t.TempDir(), t.TempDir(), heardA)
	b = liveWorkbenchHearing(t, t.TempDir(), t.TempDir(), heardB)

	for _, w := range []*Workbench{a, b} {
		if _, err := w.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
			t.Fatalf("join: %v", err)
		}
	}

	created2, err := a.NewTab("shared")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab = created2.ID
	drain(t, a)
	drain(t, b)
	return a, b, tab, heardA, heardB
}

// conflicts collects what a replica was told, which is the half of this phase
// that is about a person rather than about a document.
type conflicts struct {
	seen []ConflictEvent
}

func (c *conflicts) emit(name string, data any) {
	if name != EventWorkspaceConflict {
		return
	}
	if conflict, ok := data.(ConflictEvent); ok {
		c.seen = append(c.seen, conflict)
	}
}

func (c *conflicts) about(node string) []ConflictEvent {
	var out []ConflictEvent
	for _, conflict := range c.seen {
		if conflict.Node == node {
			out = append(out, conflict)
		}
	}
	return out
}

func liveWorkbenchHearing(t *testing.T, configHome, projectDir string, heard *conflicts) *Workbench {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	w := New(agents.Default(), claude.NewRunner(""), heard.emit, projectDir)
	w.UseOps(&HTTPClient{})
	return w
}

// converged fails unless both replicas hold exactly the same document.
//
// Byte for byte rather than "both look right": convergence is the property, and
// eyeballing two outlines is how a divergence survives a test suite.
func converged(t *testing.T, a, b *Workbench) {
	t.Helper()
	drain(t, a)
	drain(t, b)
	drain(t, a)

	left, err := json.Marshal(a.WorkspaceDocument())
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b.WorkspaceDocument())
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) {
		t.Fatalf("the two replicas hold different documents:\n A: %s\n B: %s", left, right)
	}
}

func lineText(t *testing.T, w *Workbench, node string) string {
	t.Helper()
	for n := range docNodes(w.WorkspaceDocument()) {
		if n.ID == node {
			return fieldString(n, FieldText)
		}
	}
	return ""
}

// Both rename one line. One wins, the same one on both, and the loser is told
// with the text it lost. The winner is told nothing -- a note for a write that
// took would be a note on every keystroke anybody makes.
func TestLiveTwoRenamesOneWins(t *testing.T) {
	a, b, tab, heardA, heardB := pair(t, "live/two-renames")

	doc, err := a.ApplyEdits(tab, []Edit{{
		Kind: "create-node", Node: "shared1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "original"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = doc
	converged(t, a, b)

	// Neither has seen the other's yet: this is two people typing at once.
	if _, err := a.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "shared1", Fields: map[string]any{FieldText: "A's version"},
	}}); err != nil {
		t.Fatalf("A rename: %v", err)
	}
	if _, err := b.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "shared1", Fields: map[string]any{FieldText: "B's version"},
	}}); err != nil {
		t.Fatalf("B rename: %v", err)
	}

	converged(t, a, b)

	winner := lineText(t, a, "shared1")
	if winner != "A's version" && winner != "B's version" {
		t.Fatalf("the line says %q, which is neither version", winner)
	}

	loser, told := heardA, "A's version"
	if winner == "A's version" {
		loser, told = heardB, "B's version"
	}
	notes := loser.about("shared1")
	if len(notes) == 0 {
		t.Fatalf("the replica that lost was told nothing")
	}
	if notes[0].Yours != told {
		t.Errorf("the note says %q was lost, want %q", notes[0].Yours, told)
	}
	// And nothing anywhere names anybody. The payload has no room for an
	// actor, and this is where that would show up if one were ever added.
	for _, note := range append(append([]ConflictEvent{}, heardA.seen...), heardB.seen...) {
		encoded, err := json.Marshal(note)
		if err != nil {
			t.Fatal(err)
		}
		for _, actor := range []string{a.Workspace().ID} {
			if actor != "" && containsSub(string(encoded), actor) {
				t.Errorf("a note carries an identifier: %s", encoded)
			}
		}
	}
}

// The loser puts their version back, and the disagreement runs the other way.
//
// This is the symmetry the whole design rests on: an undo is an ordinary edit
// with a fresh clock, so it beats what beat it, the other side receives it, and
// they are told in turn. Nobody's edit is privileged, including the one made by
// the person who noticed.
func TestLiveUndoIsJustAnotherEdit(t *testing.T) {
	a, b, tab, heardA, heardB := pair(t, "live/undo-symmetry")

	if _, err := a.ApplyEdits(tab, []Edit{{
		Kind: "create-node", Node: "undo1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "original"},
	}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	converged(t, a, b)

	if _, err := a.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "undo1", Fields: map[string]any{FieldText: "A's version"},
	}}); err != nil {
		t.Fatalf("A rename: %v", err)
	}
	if _, err := b.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "undo1", Fields: map[string]any{FieldText: "B's version"},
	}}); err != nil {
		t.Fatalf("B rename: %v", err)
	}
	converged(t, a, b)

	// Whoever lost puts theirs back, which is exactly a set-fields with the
	// text the note carried.
	loser, winnerHeard, restoring := a, heardB, "A's version"
	if lineText(t, a, "undo1") == "A's version" {
		loser, winnerHeard, restoring = b, heardA, "B's version"
	}
	before := len(winnerHeard.about("undo1"))

	if _, err := loser.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "undo1", Fields: map[string]any{FieldText: restoring},
	}}); err != nil {
		t.Fatalf("undo: %v", err)
	}
	converged(t, a, b)

	if got := lineText(t, a, "undo1"); got != restoring {
		t.Errorf("the undo did not win: the line says %q, want %q", got, restoring)
	}
	if got := lineText(t, b, "undo1"); got != restoring {
		t.Errorf("the two replicas disagree after an undo: %q", got)
	}
	// And the other side is told, because from where they are sitting this is
	// the same thing that happened to the first person.
	if len(winnerHeard.about("undo1")) <= before {
		t.Error("the side whose text was undone was told nothing")
	}
}

// A rename and a move at once. Neither is told, and both changes are in the
// document. This is the case the field-level merge exists for and the one most
// likely to regress into a false note.
func TestLiveRenameAndMoveBothSurvive(t *testing.T) {
	a, b, tab, heardA, heardB := pair(t, "live/rename-and-move")

	if _, err := a.ApplyEdits(tab, []Edit{
		{Kind: "create-node", Node: "parent1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "a parent"}},
		{Kind: "create-node", Node: "child1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "a child"}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	converged(t, a, b)

	if _, err := a.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "child1", Fields: map[string]any{FieldText: "renamed by A"},
	}}); err != nil {
		t.Fatalf("A rename: %v", err)
	}
	if _, err := b.ApplyEdits(tab, []Edit{{
		Kind: "move-node", Node: "child1", Parent: "parent1", Position: "m",
	}}); err != nil {
		t.Fatalf("B move: %v", err)
	}

	converged(t, a, b)

	if got := lineText(t, a, "child1"); got != "renamed by A" {
		t.Errorf("the rename did not survive the move: %q", got)
	}
	for _, heard := range []*conflicts{heardA, heardB} {
		if notes := heard.about("child1"); len(notes) > 0 {
			t.Errorf("a rename and a move were reported as a conflict: %+v", notes)
		}
	}
}

// One deletes while the other edits. The line is gone on both -- a delete wins
// in either order -- and the editor is told rather than watching it vanish.
func TestLiveDeleteBeatsAnEdit(t *testing.T) {
	a, b, tab, heardA, _ := pair(t, "live/delete-beats-edit")

	if _, err := b.ApplyEdits(tab, []Edit{{
		Kind: "create-node", Node: "doomed1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "not long for this world"},
	}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	converged(t, a, b)

	if _, err := b.ApplyEdits(tab, []Edit{{Kind: "delete-node", Node: "doomed1"}}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	drain(t, b)

	// A edits it after the delete has landed on the server but before A has
	// pulled it, which is the ordinary way this happens.
	_, _ = a.ApplyEdits(tab, []Edit{{
		Kind: "set-fields", Node: "doomed1", Fields: map[string]any{FieldText: "typed into a doomed line"},
	}})

	converged(t, a, b)

	if got := lineText(t, a, "doomed1"); got != "" {
		t.Errorf("the deleted line is still in the tree as %q", got)
	}
	notes := heardA.about("doomed1")
	if len(notes) == 0 {
		t.Fatal("the editor was told nothing about the line being deleted")
	}
	if !notes[len(notes)-1].Deleted {
		t.Errorf("the note does not say the line was deleted: %+v", notes[len(notes)-1])
	}
	if notes[len(notes)-1].Yours == "" {
		t.Error("the note does not carry what was typed, so the work is not recoverable by hand")
	}
}

// Ten edits made with the network cut, replayed when it comes back. Everything
// arrives, and the badge's count is what actually goes.
func TestLiveOfflineTenEditsAllArrive(t *testing.T) {
	a, b, tab, _, _ := pair(t, "live/offline-ten")

	gate := newGate()
	a.UseOps(&HTTPClient{HTTP: &http.Client{Timeout: syncTimeout, Transport: gate}})

	gate.open.Store(false)
	for i := range 10 {
		if _, err := a.ApplyEdits(tab, []Edit{{
			Kind:   "create-node",
			Node:   "offline" + string(rune('a'+i)),
			Fields: map[string]any{FieldType: TypeIdea, FieldText: "written with no network"},
		}}); err != nil {
			t.Fatalf("offline edit %d: %v", i, err)
		}
	}

	if got := a.SyncStatus().Pending; got < 10 {
		t.Fatalf("pending = %d, want at least the ten that were made", got)
	}

	gate.open.Store(true)
	converged(t, a, b)

	if got := a.SyncStatus().Pending; got != 0 {
		t.Errorf("pending = %d after reconnecting, want 0", got)
	}
	for i := range 10 {
		id := "offline" + string(rune('a'+i))
		if lineText(t, b, id) != "written with no network" {
			t.Errorf("%s never reached the other replica", id)
		}
	}
}

// docNodes walks every live node in a document, so a test can find one by id
// without caring where it sits.
func docNodes(doc Document) func(func(ops.Node) bool) {
	return func(yield func(ops.Node) bool) {
		var walk func(nodes []ops.TreeNode) bool
		walk = func(nodes []ops.TreeNode) bool {
			for _, n := range nodes {
				if !yield(n.Node) {
					return false
				}
				if !walk(n.Children) {
					return false
				}
			}
			return true
		}
		if !walk(doc.Tree) {
			return
		}
		for _, n := range doc.Detached {
			if !yield(n) {
				return
			}
		}
	}
}

// The two mirrors do not set each other off.
//
// Both directions already refuse to write a value that is already there, so
// this is not expected to fail -- it is here because a loop between them would
// not look like a crash. It would look like the log growing by two ops a second
// while the board sits still, and nothing else in the suite would notice.
func TestLiveTheMirrorsDoNotEcho(t *testing.T) {
	a, b, tab, _, _ := pair(t, "live/no-echo")

	if _, err := a.ApplyEdits(tab, []Edit{
		{Kind: "create-node", Node: "echo1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "an idea"}},
		{Kind: "extract-to-task", Node: "echo1", Task: "echot1", Position: "m",
			Fields: map[string]any{FieldType: TypeTask, FieldText: "a task to finish"}},
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	converged(t, a, b)

	settled := drain(t, a).Head

	if _, err := a.ApplyEdits(tab, []Edit{
		{Kind: "set-fields", Node: "echot1", Fields: map[string]any{FieldStatus: TaskDone}},
	}); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	converged(t, a, b)
	afterDone := drain(t, a).Head

	// A handful, not a stream. The exact number depends on what the board
	// mirrors back alongside it, and the point is that it is bounded.
	if grew := afterDone - settled; grew > 4 {
		t.Errorf("marking one task done wrote %d ops", grew)
	}

	// And then it stops. Three more cycles with nobody touching anything must
	// write nothing at all -- this is the assertion a loop fails.
	for range 3 {
		drain(t, a)
		drain(t, b)
	}
	if quiet := drain(t, a).Head; quiet != afterDone {
		t.Errorf("the log grew by %d with nobody editing: the two mirrors are feeding each other", quiet-afterDone)
	}

	// Both sides agree about the task, which is the outcome all this is for.
	converged(t, a, b)
	for _, w := range []*Workbench{a, b} {
		for _, task := range tabTasks(w.WorkspaceDocument(), tab) {
			if task.ID == "echot1" && fieldString(task, FieldStatus) != TaskDone {
				t.Errorf("a replica says the task is %q", fieldString(task, FieldStatus))
			}
		}
	}
}
