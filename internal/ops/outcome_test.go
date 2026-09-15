package ops

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// set is a set-fields op, which is what every conflict worth reporting is made
// of.
func set(t *testing.T, id, actor string, clock uint64, node string, kv map[string]any) Op {
	t.Helper()
	return Op{ID: id, Kind: KindSetFields, Actor: actor, Clock: clock, Node: node, Fields: fields(t, kv)}
}

// mergeAll applies ops to a fresh State and returns the outcomes.
func mergeAll(t *testing.T, list ...Op) ([]Outcome, *State) {
	t.Helper()
	var state State
	out, err := state.MergeAll(list)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	return out, &state
}

// field finds one field change in an outcome.
func changed(t *testing.T, o Outcome, node, name string) FieldChange {
	t.Helper()
	for _, f := range o.Fields {
		if f.Node == node && f.Field == name {
			return f
		}
	}
	t.Fatalf("no change reported for %s.%s; got %+v", node, name, o.Fields)
	return FieldChange{}
}

func text(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decoding %q: %v", raw, err)
	}
	return s
}

// Two writes to one field, in both orders. The loser is reported either way,
// and the value it lost is in the report -- which is the part an undo needs and
// the part a note quotes.
func TestTwoWritesToOneField(t *testing.T) {
	mine := set(t, "o1", "a", 5, "n1", map[string]any{"text": "mine"})
	theirs := set(t, "o2", "b", 9, "n1", map[string]any{"text": "theirs"})

	t.Run("mine first, theirs beats it", func(t *testing.T) {
		out, state := mergeAll(t, mine, theirs)

		first := changed(t, out[0], "n1", "text")
		if !first.Took || first.Was != nil {
			t.Errorf("the first write should take and displace nothing: %+v", first)
		}

		second := changed(t, out[1], "n1", "text")
		if !second.Took {
			t.Error("the higher clock did not take")
		}
		// Took, and displaced something: this is the loss seen from the
		// winner's side, and it is how a replica learns its own text is gone.
		if got := text(t, second.Was); got != "mine" {
			t.Errorf("Was = %q, want the text it displaced", got)
		}
		if !out[1].Changed() {
			t.Error("displacing a value is not reported as worth saying")
		}
		if got := text(t, nodeFields(t, state, "n1")["text"]); got != "theirs" {
			t.Errorf("document holds %q", got)
		}
	})

	t.Run("theirs first, mine loses", func(t *testing.T) {
		out, state := mergeAll(t, theirs, mine)

		second := changed(t, out[1], "n1", "text")
		if second.Took {
			t.Error("the lower clock took")
		}
		// Lost, and here is what stayed: the same conflict from the other end.
		if got := text(t, second.Was); got != "theirs" {
			t.Errorf("Was = %q, want the value that stayed", got)
		}
		if !out[1].Changed() {
			t.Error("a write that did not take is not reported as worth saying")
		}
		if got := text(t, nodeFields(t, state, "n1")["text"]); got != "theirs" {
			t.Errorf("document holds %q", got)
		}
	})
}

// The case the whole field-level design exists for. Two people touching one
// node but different fields is not a conflict, and a report that fired here
// would make every note in the app noise.
func TestDifferentFieldsDoNotCollide(t *testing.T) {
	mine := set(t, "o1", "a", 5, "n1", map[string]any{"text": "mine"})
	theirs := set(t, "o2", "b", 9, "n1", map[string]any{"status": "done"})

	for _, order := range [][]Op{{mine, theirs}, {theirs, mine}} {
		out, _ := mergeAll(t, order...)
		for i, o := range out {
			if o.Changed() {
				t.Errorf("op %d reported something: %+v", i, o)
			}
		}
	}
}

// A move and an edit at once. Where a node sits and what it says are separate
// slots, so neither loses.
func TestMoveAndEditDoNotCollide(t *testing.T) {
	edit := set(t, "o1", "a", 5, "n1", map[string]any{"text": "mine"})
	move := Op{ID: "o2", Kind: KindMoveNode, Actor: "b", Clock: 9, Node: "n1", Parent: "root", Position: "m"}

	for _, order := range [][]Op{{edit, move}, {move, edit}} {
		out, _ := mergeAll(t, order...)
		for i, o := range out {
			if o.Changed() {
				t.Errorf("op %d reported something: %+v", i, o)
			}
		}
	}
}

// Two moves do collide: parent and position are one slot between them, so one
// move wins whole and the other loses whole.
func TestTwoMoves(t *testing.T) {
	mine := Op{ID: "o1", Kind: KindMoveNode, Actor: "a", Clock: 5, Node: "n1", Parent: "p1", Position: "m"}
	theirs := Op{ID: "o2", Kind: KindMoveNode, Actor: "b", Clock: 9, Node: "n1", Parent: "p2", Position: "n"}

	out, _ := mergeAll(t, theirs, mine)
	if len(out[1].Moves) != 1 {
		t.Fatalf("moves = %+v", out[1].Moves)
	}
	lost := out[1].Moves[0]
	if lost.Took {
		t.Error("the lower clock's move took")
	}
	// Both halves of where it was, because both halves are what it lost.
	if lost.WasParent != "p2" || lost.WasPosition != "n" {
		t.Errorf("was at %s/%s, want p2/n", lost.WasParent, lost.WasPosition)
	}
	if !out[1].Changed() {
		t.Error("a move that did not take is not reported as worth saying")
	}
}

// A write to a tombstone, in both orders. The write happens -- a tombstone keeps
// its fields -- and to whoever wrote it, an edit that will never appear in the
// tree is indistinguishable from the edit vanishing.
func TestWritingToATombstone(t *testing.T) {
	edit := set(t, "o1", "a", 5, "n1", map[string]any{"text": "mine"})
	del := Op{ID: "o2", Kind: KindDeleteNode, Actor: "b", Clock: 9, Node: "n1"}

	t.Run("the delete arrives first", func(t *testing.T) {
		out, _ := mergeAll(t, del, edit)
		if !slices.Contains(out[1].Tombstones, "n1") {
			t.Errorf("tombstones = %v, want n1", out[1].Tombstones)
		}
		if !out[1].Changed() {
			t.Error("writing to a tombstone is not reported as worth saying")
		}
	})

	t.Run("the delete arrives second", func(t *testing.T) {
		// The edit is reported as ordinary, because when it landed the node was
		// alive. The person finds out from the delete's own arrival, which is a
		// different path -- see the phase's delete task.
		out, _ := mergeAll(t, edit, del)
		if len(out[0].Tombstones) != 0 {
			t.Errorf("the edit was reported against a tombstone that did not exist yet: %v", out[0].Tombstones)
		}
		if out[0].Changed() {
			t.Error("an edit to a live node reported something")
		}
	})

	t.Run("a delete is not itself a loss", func(t *testing.T) {
		out, _ := mergeAll(t, del, Op{ID: "o3", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "n1"})
		if out[1].Changed() {
			t.Error("deleting what is already deleted reported a conflict")
		}
	})
}

// A replay is not a conflict. Without this, every retry after a timeout would
// produce a note -- and retries after timeouts are the normal case, which is
// what idempotence is for.
func TestReplayIsNotALoss(t *testing.T) {
	op := set(t, "o1", "a", 5, "n1", map[string]any{"text": "mine"})

	out, _ := mergeAll(t, op, op)
	if !out[0].Fresh {
		t.Error("the first application was not fresh")
	}
	if out[1].Fresh {
		t.Error("the replay was fresh")
	}
	if out[1].Changed() {
		t.Errorf("the replay reported %+v", out[1])
	}
	if len(out[1].Fields) != 0 || len(out[1].Moves) != 0 {
		t.Errorf("the replay reported changes: %+v", out[1])
	}
}

// extract-to-task writes to two nodes, and the report says which is which.
func TestExtractReportsBothNodes(t *testing.T) {
	out, _ := mergeAll(t,
		Op{ID: "o1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n1", Fields: fields(t, map[string]any{"text": "an idea"})},
		Op{ID: "o2", Kind: KindExtractToTask, Actor: "a", Clock: 2, Node: "n1", Task: "t1", Position: "m",
			Fields: fields(t, map[string]any{"text": "a task"})},
	)

	// The link back, on the task.
	if got := text(t, changed(t, out[1], "t1", FieldExtractedFrom).Was); got != "" {
		t.Errorf("extractedFrom displaced %q on a new task", got)
	}
	// And the link forward, on the idea it came from -- a different node.
	link := changed(t, out[1], "n1", FieldTaskID)
	if !link.Took {
		t.Error("the idea was not linked to its task")
	}
}

// The report is deterministic. A map's iteration order is random, so without
// sorting, the same op would read differently twice and every test of it would
// be flaky.
func TestFieldOrderIsStable(t *testing.T) {
	op := set(t, "o1", "a", 5, "n1", map[string]any{"z": 1, "a": 2, "m": 3})
	for range 20 {
		out, _ := mergeAll(t, op)
		var names []string
		for _, f := range out[0].Fields {
			names = append(names, f.Field)
		}
		if !slices.IsSorted(names) {
			t.Fatalf("fields came out %v", names)
		}
	}
}

// Was is a copy. A caller holding a report while the State is merged into again
// must not watch the value it was given change underneath it.
func TestWasIsACopy(t *testing.T) {
	var state State
	if _, err := state.Merge(set(t, "o1", "a", 5, "n1", map[string]any{"text": "first"})); err != nil {
		t.Fatal(err)
	}
	out, err := state.Merge(set(t, "o2", "b", 9, "n1", map[string]any{"text": "second"}))
	if err != nil {
		t.Fatal(err)
	}
	was := changed(t, out, "n1", "text").Was

	if _, err := state.Merge(set(t, "o3", "c", 20, "n1", map[string]any{"text": "third"})); err != nil {
		t.Fatal(err)
	}
	if got := text(t, was); got != "first" {
		t.Errorf("Was changed to %q after a later merge", got)
	}
}

// Apply still answers the short question, and answers it the same way.
func TestApplyStillReportsFreshness(t *testing.T) {
	var state State
	op := set(t, "o1", "a", 5, "n1", map[string]any{"text": "mine"})

	fresh, err := state.Apply(op)
	if err != nil || !fresh {
		t.Fatalf("Apply = %v, %v", fresh, err)
	}
	again, err := state.Apply(op)
	if err != nil || again {
		t.Fatalf("a replay reported %v, %v", again, err)
	}
}

// Nothing in a report names an actor. The merge needs one as a tiebreak and
// that is the only reason it exists; a report that passed it outward would be
// an attribution channel in a workspace whose design is that there is none.
func TestNoActorReachesTheReport(t *testing.T) {
	out, _ := mergeAll(t,
		set(t, "o1", "alice-machine", 5, "n1", map[string]any{"text": "mine"}),
		set(t, "o2", "bob-machine", 9, "n1", map[string]any{"text": "theirs"}),
	)

	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("encoding the outcomes: %v", err)
	}
	body := string(encoded)
	for _, actor := range []string{"alice-machine", "bob-machine"} {
		if strings.Contains(body, actor) {
			t.Errorf("an actor reached the report: %s", body)
		}
	}
}

// nodeFields reads a node's fields out of a State, failing if it is not there.
func nodeFields(t *testing.T, s *State, id string) map[string]json.RawMessage {
	t.Helper()
	n, ok := s.Node(id)
	if !ok {
		t.Fatalf("no node %s", id)
	}
	return n.Fields
}
