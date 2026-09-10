package ops

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fields turns a literal into the field map an op carries, so a test reads as
// the edit it is rather than as a pile of json.RawMessage.
func fields(t *testing.T, kv map[string]any) map[string]json.RawMessage {
	t.Helper()
	if kv == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(kv))
	for name, value := range kv {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encoding field %q: %v", name, err)
		}
		out[name] = encoded
	}
	return out
}

// merge applies ops to a fresh State and fails the test if any is rejected.
func merge(t *testing.T, list ...Op) *State {
	t.Helper()
	var state State
	if _, err := state.ApplyAll(list); err != nil {
		t.Fatalf("applying ops: %v", err)
	}
	return &state
}

// digest renders everything a caller can observe about a State, so two States
// can be compared for equality without exporting the merge bookkeeping. It is
// the assertion that order-independence tests are made of.
func digest(t *testing.T, s *State) string {
	t.Helper()
	var all []Node
	for node := range s.All() {
		all = append(all, node)
	}
	encoded, err := json.Marshal(struct {
		Clock    uint64     `json:"clock"`
		Nodes    []Node     `json:"nodes"`
		Tree     []TreeNode `json:"tree"`
		Detached []Node     `json:"detached"`
	}{s.Clock(), all, s.Tree(), s.Detached()})
	if err != nil {
		t.Fatalf("encoding digest: %v", err)
	}
	return string(encoded)
}

// field reads one field of one node as a JSON string, and fails if it is not
// there.
func field(t *testing.T, s *State, nodeID, name string) string {
	t.Helper()
	node, ok := s.Node(nodeID)
	if !ok {
		t.Fatalf("node %s is not in the state", nodeID)
	}
	value, ok := node.Fields[name]
	if !ok {
		t.Fatalf("node %s has no field %q, only %v", nodeID, name, slices.Sorted(mapKeys(node.Fields)))
	}
	return string(value)
}

func mapKeys(m map[string]json.RawMessage) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// permutations yields every ordering of a slice. The op sets under test are
// small on purpose: order-independence is a claim about every ordering, so the
// test checks every ordering rather than a sample of them.
func permutations(list []Op, yield func([]Op)) {
	if len(list) <= 1 {
		yield(slices.Clone(list))
		return
	}
	for i := range list {
		rest := slices.Clone(list)
		head := rest[i]
		rest = slices.Delete(rest, i, i+1)
		permutations(rest, func(tail []Op) {
			yield(append([]Op{head}, tail...))
		})
	}
}

// TestMergeIsOrderIndependent is the property the whole package exists for: the
// same ops in any order give the same state. Everything else here is a
// worked example of one of its consequences.
func TestMergeIsOrderIndependent(t *testing.T) {
	sets := map[string][]Op{
		"a tree built out of order": {
			{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "root", Position: "m"},
			{ID: "2", Kind: KindCreateNode, Actor: "a", Clock: 2, Node: "kid", Parent: "root", Position: "m"},
			{ID: "3", Kind: KindSetFields, Actor: "b", Clock: 3, Node: "kid", Fields: fields(t, map[string]any{"title": "one"})},
			{ID: "4", Kind: KindSetFields, Actor: "c", Clock: 3, Node: "kid", Fields: fields(t, map[string]any{"title": "two"})},
			{ID: "5", Kind: KindMoveNode, Actor: "b", Clock: 5, Node: "kid", Position: "a"},
		},
		"two replicas racing on one node": {
			{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Position: "m"},
			{ID: "2", Kind: KindSetFields, Actor: "a", Clock: 4, Node: "n", Fields: fields(t, map[string]any{"title": "a", "done": false})},
			{ID: "3", Kind: KindSetFields, Actor: "b", Clock: 4, Node: "n", Fields: fields(t, map[string]any{"title": "b", "note": "hi"})},
			{ID: "4", Kind: KindSetFields, Actor: "b", Clock: 9, Node: "n", Fields: fields(t, map[string]any{"done": true})},
		},
		"a delete racing edits and a child": {
			{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Position: "m"},
			{ID: "2", Kind: KindSetFields, Actor: "a", Clock: 2, Node: "n", Fields: fields(t, map[string]any{"title": "before"})},
			{ID: "3", Kind: KindDeleteNode, Actor: "b", Clock: 3, Node: "n"},
			{ID: "4", Kind: KindSetFields, Actor: "c", Clock: 99, Node: "n", Fields: fields(t, map[string]any{"title": "after"})},
			{ID: "5", Kind: KindCreateNode, Actor: "c", Clock: 100, Node: "kid", Parent: "n", Position: "m"},
		},
		"an extraction and the deletes around it": {
			{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "src", Position: "m"},
			{ID: "2", Kind: KindExtractToTask, Actor: "a", Clock: 2, Node: "src", Task: "task", Position: "z", Fields: fields(t, map[string]any{"title": "extracted"})},
			{ID: "3", Kind: KindSetFields, Actor: "b", Clock: 2, Node: "task", Fields: fields(t, map[string]any{"title": "renamed"})},
			{ID: "4", Kind: KindDeleteNode, Actor: "b", Clock: 4, Node: "src"},
		},
	}

	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			want := digest(t, merge(t, set...))
			orderings := 0
			permutations(set, func(order []Op) {
				orderings++
				if got := digest(t, merge(t, order...)); got != want {
					t.Fatalf("order %s gives a different state\n got: %s\nwant: %s", ids(order), got, want)
				}
			})
			if orderings < 2 {
				t.Fatalf("only %d ordering was checked", orderings)
			}
		})
	}
}

func ids(list []Op) string {
	out := make([]string, len(list))
	for i, op := range list {
		out[i] = op.ID
	}
	return "[" + strings.Join(out, " ") + "]"
}

// TestFieldsAreLastWriteWinsPerField covers rule one: a node is not a single
// value, so two replicas editing different parts of it both keep their edit.
func TestFieldsAreLastWriteWinsPerField(t *testing.T) {
	t.Run("different fields both survive", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n"},
			Op{ID: "2", Kind: KindSetFields, Actor: "a", Clock: 5, Node: "n", Fields: fields(t, map[string]any{"title": "mine"})},
			Op{ID: "3", Kind: KindSetFields, Actor: "b", Clock: 5, Node: "n", Fields: fields(t, map[string]any{"status": "doing"})},
		)
		if got := field(t, state, "n", "title"); got != `"mine"` {
			t.Errorf("title = %s, want \"mine\"", got)
		}
		if got := field(t, state, "n", "status"); got != `"doing"` {
			t.Errorf("status = %s, want \"doing\"", got)
		}
	})

	t.Run("the higher clock takes the field", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindSetFields, Actor: "z", Clock: 9, Node: "n", Fields: fields(t, map[string]any{"title": "late"})},
			Op{ID: "2", Kind: KindSetFields, Actor: "a", Clock: 2, Node: "n", Fields: fields(t, map[string]any{"title": "early"})},
		)
		if got := field(t, state, "n", "title"); got != `"late"` {
			t.Errorf("title = %s, want \"late\": the higher clock wins whatever the actor is", got)
		}
	})

	t.Run("an equal clock is broken by the actor", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindSetFields, Actor: "alice", Clock: 7, Node: "n", Fields: fields(t, map[string]any{"title": "alice"})},
			Op{ID: "2", Kind: KindSetFields, Actor: "bob", Clock: 7, Node: "n", Fields: fields(t, map[string]any{"title": "bob"})},
		)
		if got := field(t, state, "n", "title"); got != `"bob"` {
			t.Errorf("title = %s, want \"bob\": the higher actor breaks an equal clock", got)
		}
	})

	t.Run("a move does not clobber a field, and a field does not clobber a move", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Parent: "p", Position: "m"},
			Op{ID: "2", Kind: KindSetFields, Actor: "a", Clock: 8, Node: "n", Fields: fields(t, map[string]any{"title": "kept"})},
			Op{ID: "3", Kind: KindMoveNode, Actor: "b", Clock: 9, Node: "n", Parent: "q", Position: "z"},
		)
		node, _ := state.Node("n")
		if node.Parent != "q" || node.Position != "z" {
			t.Errorf("placement = %s/%s, want q/z", node.Parent, node.Position)
		}
		if got := field(t, state, "n", "title"); got != `"kept"` {
			t.Errorf("title = %s, want \"kept\": a move carries no fields and must not erase them", got)
		}
	})
}

// TestDeleteBeatsConcurrentEdit covers rule two, and covers it in both orders,
// because a tombstone that only wins when it happens to arrive last is not a
// tombstone.
func TestDeleteBeatsConcurrentEdit(t *testing.T) {
	edit := Op{ID: "edit", Kind: KindSetFields, Actor: "b", Clock: 500, Node: "n", Fields: fields(t, map[string]any{"title": "edited"})}
	del := Op{ID: "del", Kind: KindDeleteNode, Actor: "a", Clock: 2, Node: "n"}
	create := Op{ID: "create", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Fields: fields(t, map[string]any{"title": "original"})}

	orders := map[string][]Op{
		"delete last":                   {create, edit, del},
		"delete first":                  {create, del, edit},
		"delete before the node exists": {del, create, edit},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			state := merge(t, order...)
			node, ok := state.Node("n")
			if !ok {
				t.Fatal("the node is gone entirely; a tombstone is still a node")
			}
			if !node.Deleted {
				t.Errorf("node is not deleted: an edit with clock %d must not beat a delete with clock %d", edit.Clock, del.Clock)
			}
			if len(state.Tree()) != 0 {
				t.Errorf("tree = %v, want nothing: a tombstone is not in the tree", state.Tree())
			}
		})
	}

	t.Run("nothing resurrects a node", func(t *testing.T) {
		// Every kind that writes to a node, all with clocks far beyond the
		// delete's, and none of them puts it back.
		state := merge(t,
			Op{ID: "1", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "n"},
			Op{ID: "2", Kind: KindCreateNode, Actor: "b", Clock: 900, Node: "n", Position: "m", Fields: fields(t, map[string]any{"title": "back?"})},
			Op{ID: "3", Kind: KindSetFields, Actor: "b", Clock: 901, Node: "n", Fields: fields(t, map[string]any{"title": "please?"})},
			Op{ID: "4", Kind: KindMoveNode, Actor: "b", Clock: 902, Node: "n", Parent: ""},
			Op{ID: "5", Kind: KindExtractToTask, Actor: "b", Clock: 903, Node: "other", Task: "n"},
		)
		node, _ := state.Node("n")
		if !node.Deleted {
			t.Fatal("the node came back")
		}
		if len(state.Tree()) != 1 || state.Tree()[0].ID != "other" {
			t.Errorf("tree = %v, want only the node that was never deleted", state.Tree())
		}
	})

	t.Run("a tombstone keeps its content, whichever order it arrived in", func(t *testing.T) {
		// The fields of a tombstone are still last-write-wins. Swallowing writes
		// that arrive after the delete would look tidier and would not converge:
		// the survivors would be whichever ones happened to be delivered first.
		create := Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Fields: fields(t, map[string]any{"title": "early"})}
		del := Op{ID: "2", Kind: KindDeleteNode, Actor: "a", Clock: 2, Node: "n"}
		late := Op{ID: "3", Kind: KindSetFields, Actor: "b", Clock: 3, Node: "n", Fields: fields(t, map[string]any{"title": "late"})}

		for _, order := range [][]Op{{create, del, late}, {create, late, del}, {late, del, create}} {
			state := merge(t, order...)
			if got := field(t, state, "n", "title"); got != `"late"` {
				t.Errorf("order %s: title = %s, want \"late\"", ids(order), got)
			}
			if node, _ := state.Node("n"); !node.Deleted {
				t.Errorf("order %s: the node is not deleted", ids(order))
			}
		}
	})
}

// TestApplyIsIdempotent covers rule three: a client that lost the response to a
// request and replays its outbox must not double-apply anything.
func TestApplyIsIdempotent(t *testing.T) {
	op := Op{ID: "same", Kind: KindSetFields, Actor: "a", Clock: 3, Node: "n", Fields: fields(t, map[string]any{"title": "once"})}

	var state State
	applied, err := state.Apply(op)
	if err != nil || !applied {
		t.Fatalf("first Apply = %v, %v; want true, nil", applied, err)
	}
	before := digest(t, &state)

	applied, err = state.Apply(op)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if applied {
		t.Error("second Apply reported the op as new")
	}
	if got := digest(t, &state); got != before {
		t.Errorf("state changed on replay\n got: %s\nwant: %s", got, before)
	}

	t.Run("an ID is honoured over the content behind it", func(t *testing.T) {
		// The server rejects a reused ID carrying different content, because
		// that is a client bug. The merge cannot: it has already forgotten what
		// the op said. So it holds the line it can hold — the ID is applied
		// once — and this test pins that down rather than leaving it to be
		// discovered.
		impostor := op
		impostor.Fields = fields(t, map[string]any{"title": "twice"})
		if applied, err := state.Apply(impostor); err != nil || applied {
			t.Fatalf("Apply of a reused ID = %v, %v; want false, nil", applied, err)
		}
		if got := field(t, &state, "n", "title"); got != `"once"` {
			t.Errorf("title = %s, want \"once\"", got)
		}
	})

	t.Run("ApplyAll counts only what was new", func(t *testing.T) {
		list := []Op{op, op, {ID: "other", Kind: KindDeleteNode, Actor: "a", Clock: 4, Node: "n"}}
		var fresh State
		count, err := fresh.ApplyAll(list)
		if err != nil {
			t.Fatalf("ApplyAll: %v", err)
		}
		if count != 2 {
			t.Errorf("ApplyAll = %d, want 2", count)
		}
	})
}

func TestExtractToTask(t *testing.T) {
	state := merge(t,
		Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "src", Position: "m", Fields: fields(t, map[string]any{"title": "a paragraph"})},
		Op{ID: "2", Kind: KindExtractToTask, Actor: "a", Clock: 2, Node: "src", Task: "task", Position: "z", Fields: fields(t, map[string]any{"title": "do the thing", "status": "todo"})},
	)

	if got := field(t, state, "src", FieldTaskID); got != `"task"` {
		t.Errorf("%s on the source = %s, want \"task\"", FieldTaskID, got)
	}
	if got := field(t, state, "task", FieldExtractedFrom); got != `"src"` {
		t.Errorf("%s on the task = %s, want \"src\"", FieldExtractedFrom, got)
	}
	if got := field(t, state, "task", "status"); got != `"todo"` {
		t.Errorf("status = %s, want \"todo\"", got)
	}
	if got := field(t, state, "src", "title"); got != `"a paragraph"` {
		t.Errorf("the source title = %s, want it untouched by the extraction", got)
	}

	t.Run("the task still appears when the source is already gone", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "src"},
			Op{ID: "2", Kind: KindExtractToTask, Actor: "b", Clock: 2, Node: "src", Task: "task", Fields: fields(t, map[string]any{"title": "still wanted"})},
		)
		if got := field(t, state, "task", "title"); got != `"still wanted"` {
			t.Errorf("task title = %s, want the task created anyway", got)
		}
		if got := field(t, state, "src", FieldTaskID); got != `"task"` {
			t.Errorf("%s on the deleted source = %s, want the tombstone to record where its content went", FieldTaskID, got)
		}
	})
}

func TestTreeOrdersByPositionThenID(t *testing.T) {
	state := merge(t,
		Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "root", Position: "m"},
		Op{ID: "2", Kind: KindCreateNode, Actor: "a", Clock: 2, Node: "b", Parent: "root", Position: "n"},
		Op{ID: "3", Kind: KindCreateNode, Actor: "a", Clock: 3, Node: "a", Parent: "root", Position: "n"},
		Op{ID: "4", Kind: KindCreateNode, Actor: "a", Clock: 4, Node: "first", Parent: "root", Position: "c"},
	)

	tree := state.Tree()
	if len(tree) != 1 || tree[0].ID != "root" {
		t.Fatalf("roots = %v, want one root", tree)
	}
	got := make([]string, len(tree[0].Children))
	for i, child := range tree[0].Children {
		got[i] = child.ID
	}
	want := []string{"first", "a", "b"}
	if !slices.Equal(got, want) {
		t.Errorf("children = %v, want %v: position first, then ID for the tie", got, want)
	}
}

func TestDetachedNodesAreSurfacedNotDropped(t *testing.T) {
	t.Run("under a tombstone", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "parent", Position: "m"},
			Op{ID: "2", Kind: KindCreateNode, Actor: "b", Clock: 2, Node: "kid", Parent: "parent", Position: "m"},
			Op{ID: "3", Kind: KindDeleteNode, Actor: "a", Clock: 3, Node: "parent"},
		)
		if len(state.Tree()) != 0 {
			t.Errorf("tree = %v, want nothing", state.Tree())
		}
		detached := state.Detached()
		if len(detached) != 1 || detached[0].ID != "kid" {
			t.Errorf("detached = %v, want just the kid", detached)
		}
	})

	t.Run("parent nobody has mentioned", func(t *testing.T) {
		state := merge(t,
			Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "kid", Parent: "never-seen", Position: "m"},
		)
		if len(state.Tree()) != 0 {
			t.Errorf("tree = %v, want nothing", state.Tree())
		}
		if detached := state.Detached(); len(detached) != 1 || detached[0].ID != "kid" {
			t.Errorf("detached = %v, want the kid", detached)
		}
	})

	t.Run("a cycle two concurrent moves made", func(t *testing.T) {
		// Neither replica did anything invalid on its own: one put a under b,
		// the other put b under a. Together they made a loop, which is what
		// Detached is for.
		state := merge(t,
			Op{ID: "1", Kind: KindCreateNode, Actor: "x", Clock: 1, Node: "a", Position: "m"},
			Op{ID: "2", Kind: KindCreateNode, Actor: "x", Clock: 2, Node: "b", Position: "m"},
			Op{ID: "3", Kind: KindMoveNode, Actor: "x", Clock: 5, Node: "a", Parent: "b"},
			Op{ID: "4", Kind: KindMoveNode, Actor: "y", Clock: 5, Node: "b", Parent: "a"},
		)
		if len(state.Tree()) != 0 {
			t.Errorf("tree = %v, want nothing: everything is in the loop", state.Tree())
		}
		detached := state.Detached()
		if len(detached) != 2 {
			t.Fatalf("detached = %v, want both nodes", detached)
		}
		if detached[0].ID != "a" || detached[1].ID != "b" {
			t.Errorf("detached = %v, want a then b, ordered by ID", detached)
		}
	})
}

// TestDeepTreeDoesNotOverflow pins the iterative walk down. A recursive one
// passes every other test in this file and dies here.
func TestDeepTreeDoesNotOverflow(t *testing.T) {
	const depth = 200_000
	list := make([]Op, 0, depth)
	for i := range depth {
		op := Op{
			ID:    fmt.Sprintf("op%d", i),
			Kind:  KindCreateNode,
			Actor: "a",
			Clock: uint64(i) + 1,
			Node:  fmt.Sprintf("n%d", i),
		}
		if i > 0 {
			op.Parent = fmt.Sprintf("n%d", i-1)
		}
		list = append(list, op)
	}
	state := merge(t, list...)

	levels := 0
	for node := state.Tree(); len(node) > 0; node = node[0].Children {
		levels++
	}
	if levels != depth {
		t.Errorf("walked %d levels, want %d", levels, depth)
	}
	if got := state.Detached(); len(got) != 0 {
		t.Errorf("detached = %d nodes, want none", len(got))
	}
}

func TestClockTracksTheHighestSeen(t *testing.T) {
	state := merge(t,
		Op{ID: "1", Kind: KindDeleteNode, Actor: "a", Clock: 4, Node: "n"},
		Op{ID: "2", Kind: KindDeleteNode, Actor: "a", Clock: 41, Node: "m"},
		Op{ID: "3", Kind: KindDeleteNode, Actor: "a", Clock: 7, Node: "o"},
	)
	if got := state.Clock(); got != 41 {
		t.Errorf("Clock() = %d, want 41", got)
	}
}

func TestZeroStateIsUsable(t *testing.T) {
	var state State
	if state.Len() != 0 || state.Clock() != 0 || state.Applied("anything") {
		t.Fatal("the zero State is not empty")
	}
	if state.Tree() != nil && len(state.Tree()) != 0 {
		t.Error("the zero State has a tree")
	}
	if _, ok := state.Node("n"); ok {
		t.Error("the zero State has a node")
	}
	if _, err := state.Apply(Op{ID: "1", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "n"}); err != nil {
		t.Errorf("Apply on a zero State: %v", err)
	}
}

func TestSnapshotsDoNotAliasTheState(t *testing.T) {
	source := fields(t, map[string]any{"title": "original"})
	state := merge(t, Op{ID: "1", Kind: KindSetFields, Actor: "a", Clock: 1, Node: "n", Fields: source})

	// The op's own buffer is reused by whoever decoded it.
	copy(source["title"], `"clobbered"`)
	if got := field(t, state, "n", "title"); got != `"original"` {
		t.Errorf("title = %s: the state aliased the op's bytes", got)
	}

	node, _ := state.Node("n")
	copy(node.Fields["title"], `"clobbered"`)
	if got := field(t, state, "n", "title"); got != `"original"` {
		t.Errorf("title = %s: a snapshot aliased the state's bytes", got)
	}
}
