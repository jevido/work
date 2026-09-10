package ops

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpValidate(t *testing.T) {
	// valid is a well-formed op of every kind, so each case below can spoil one
	// thing and leave everything else right.
	valid := map[Kind]Op{
		KindCreateNode:    {ID: "o", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Parent: "p", Position: "m"},
		KindSetFields:     {ID: "o", Kind: KindSetFields, Actor: "a", Clock: 1, Node: "n", Fields: map[string]json.RawMessage{"title": json.RawMessage(`"x"`)}},
		KindDeleteNode:    {ID: "o", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "n"},
		KindMoveNode:      {ID: "o", Kind: KindMoveNode, Actor: "a", Clock: 1, Node: "n", Parent: "p", Position: "m"},
		KindExtractToTask: {ID: "o", Kind: KindExtractToTask, Actor: "a", Clock: 1, Node: "n", Task: "t", Position: "m"},
	}
	for kind, op := range valid {
		if err := op.Validate(); err != nil {
			t.Fatalf("the %s example is not valid: %v", kind, err)
		}
	}

	spoil := func(kind Kind, change func(*Op)) Op {
		op := valid[kind]
		change(&op)
		return op
	}
	long := strings.Repeat("x", MaxIDLen+1)

	tests := []struct {
		name string
		op   Op
		want string // a fragment of the message, so the reason is asserted too
	}{
		{"no id", spoil(KindSetFields, func(o *Op) { o.ID = "" }), "id is empty"},
		{"oversized id", spoil(KindSetFields, func(o *Op) { o.ID = long }), "id is 129 bytes"},
		{"unknown kind", spoil(KindSetFields, func(o *Op) { o.Kind = "rename-everything" }), "unknown kind"},
		{"empty kind", spoil(KindSetFields, func(o *Op) { o.Kind = "" }), "unknown kind"},
		{"no actor", spoil(KindSetFields, func(o *Op) { o.Actor = "" }), "actor is empty"},
		{"zero clock", spoil(KindSetFields, func(o *Op) { o.Clock = 0 }), "clock is zero"},
		{"no node", spoil(KindSetFields, func(o *Op) { o.Node = "" }), "node is empty"},
		{"oversized position", spoil(KindMoveNode, func(o *Op) { o.Position = strings.Repeat("p", MaxPositionLen+1) }), "position is"},
		{"oversized parent", spoil(KindMoveNode, func(o *Op) { o.Parent = long }), "parent is"},

		{"set-fields with nothing to set", spoil(KindSetFields, func(o *Op) { o.Fields = nil }), "no fields"},
		{"empty field name", spoil(KindSetFields, func(o *Op) {
			o.Fields = map[string]json.RawMessage{"": json.RawMessage(`1`)}
		}), "field name is empty"},
		{"oversized field name", spoil(KindSetFields, func(o *Op) {
			o.Fields = map[string]json.RawMessage{strings.Repeat("f", MaxFieldNameLen+1): json.RawMessage(`1`)}
		}), "over the 128 limit"},
		{"field value is not JSON", spoil(KindSetFields, func(o *Op) {
			o.Fields = map[string]json.RawMessage{"title": json.RawMessage(`{oops`)}
		}), "not JSON"},
		{"too many fields", spoil(KindSetFields, func(o *Op) {
			o.Fields = make(map[string]json.RawMessage, MaxFields+1)
			for i := range MaxFields + 1 {
				o.Fields[string(rune('a'+i%26))+string(rune('a'+i/26))] = json.RawMessage(`1`)
			}
		}), "over the 256 limit"},

		{"created under itself", spoil(KindCreateNode, func(o *Op) { o.Parent = o.Node }), "its own parent"},
		{"moved under itself", spoil(KindMoveNode, func(o *Op) { o.Parent = o.Node }), "its own parent"},
		{"extracted with no task", spoil(KindExtractToTask, func(o *Op) { o.Task = "" }), "task is empty"},
		{"extracted from itself", spoil(KindExtractToTask, func(o *Op) { o.Task = o.Node }), "extracted from itself"},
		{"task under itself", spoil(KindExtractToTask, func(o *Op) { o.Parent = o.Task }), "its own parent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestInvalidOpChangesNothing is the other half of validation: rejecting an op
// is only useful if the rejection leaves no trace.
func TestInvalidOpChangesNothing(t *testing.T) {
	state := merge(t, Op{ID: "1", Kind: KindCreateNode, Actor: "a", Clock: 1, Node: "n", Fields: fields(t, map[string]any{"title": "kept"})})
	before := digest(t, state)

	bad := Op{ID: "2", Kind: "who-knows", Actor: "a", Clock: 2, Node: "n", Fields: fields(t, map[string]any{"title": "clobbered"})}
	if applied, err := state.Apply(bad); err == nil || applied {
		t.Fatalf("Apply of an invalid op = %v, %v; want false and an error", applied, err)
	}
	if state.Applied("2") {
		t.Error("the invalid op was recorded as applied, so a corrected retry would be ignored")
	}
	if got := digest(t, state); got != before {
		t.Errorf("state changed\n got: %s\nwant: %s", got, before)
	}
}

func TestApplyAllStopsAtTheFirstBadOp(t *testing.T) {
	var state State
	count, err := state.ApplyAll([]Op{
		{ID: "1", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "n"},
		{ID: "2", Kind: KindSetFields, Actor: "a", Clock: 0, Node: "n", Fields: fields(t, map[string]any{"x": 1})},
		{ID: "3", Kind: KindDeleteNode, Actor: "a", Clock: 3, Node: "m"},
	})
	if err == nil {
		t.Fatal("ApplyAll accepted an op with a zero clock")
	}
	if count != 1 {
		t.Errorf("ApplyAll = %d, want 1: the op before the bad one still applied", count)
	}
	if state.Applied("3") {
		t.Error("an op after the bad one was applied")
	}
}

func TestStampOrder(t *testing.T) {
	tests := []struct {
		name string
		a, b Stamp
		want int
	}{
		{"higher clock wins whatever the actor", Stamp{9, "a"}, Stamp{2, "z"}, +1},
		{"lower clock loses whatever the actor", Stamp{2, "z"}, Stamp{9, "a"}, -1},
		{"equal clock falls to the actor", Stamp{4, "bob"}, Stamp{4, "alice"}, +1},
		{"identical stamps tie", Stamp{4, "a"}, Stamp{4, "a"}, 0},
		{"any real stamp beats the zero one", Stamp{1, ""}, Stamp{}, +1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Compare(tt.b); got != tt.want {
				t.Errorf("Compare() = %d, want %d", got, tt.want)
			}
			if got := tt.a.After(tt.b); got != (tt.want > 0) {
				t.Errorf("After() = %v, want %v", got, tt.want > 0)
			}
		})
	}
}

// TestOpRoundTripsThroughJSON matters more than it looks: an op is stored as
// JSON and read back by a different build of this program, and by a viewer
// written in another language, so the wire shape is the contract.
func TestOpRoundTripsThroughJSON(t *testing.T) {
	original := Op{
		ID:     "01JBQ8Z3K4M5N6P7Q8R9S0T1V2",
		Kind:   KindExtractToTask,
		Actor:  "desktop-6f2a",
		Clock:  91,
		Node:   "n_7f3c",
		Task:   "n_task9",
		Parent: "n_root",
		// A position that would break if anyone treated it as a number.
		Position: "a0V",
		Fields: map[string]json.RawMessage{
			"title":  json.RawMessage(`"Ship the sync server"`),
			"done":   json.RawMessage(`false`),
			"weight": json.RawMessage(`3.5`),
			"tags":   json.RawMessage(`["a","b"]`),
			"empty":  json.RawMessage(`null`),
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var decoded Op
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("the decoded op is not valid: %v", err)
	}

	// Both sides are merged and compared through the observable state rather
	// than field by field, because that is what a consumer actually depends on.
	if got, want := digest(t, merge(t, decoded)), digest(t, merge(t, original)); got != want {
		t.Errorf("the round trip changed the op\n got: %s\nwant: %s", got, want)
	}

	t.Run("the kinds a viewer switches on are spelled as documented", func(t *testing.T) {
		for kind, want := range map[Kind]string{
			KindCreateNode:    "create-node",
			KindSetFields:     "set-fields",
			KindDeleteNode:    "delete-node",
			KindMoveNode:      "move-node",
			KindExtractToTask: "extract-to-task",
		} {
			if string(kind) != want {
				t.Errorf("kind = %q, want %q", kind, want)
			}
		}
	})

	t.Run("unused fields are absent, not empty", func(t *testing.T) {
		encoded, err := json.Marshal(Op{ID: "o", Kind: KindDeleteNode, Actor: "a", Clock: 1, Node: "n"})
		if err != nil {
			t.Fatalf("encoding: %v", err)
		}
		for _, absent := range []string{"fields", "parent", "position", "task"} {
			if strings.Contains(string(encoded), `"`+absent+`"`) {
				t.Errorf("a delete-node carries %q: %s", absent, encoded)
			}
		}
	})
}
