package workbench

import (
	"encoding/json"
	"testing"

	"dev.jevido/work/internal/ops"
)

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func setText(t *testing.T, id, actor string, clock uint64, node, text string) ops.Op {
	t.Helper()
	return ops.Op{
		ID: id, Kind: ops.KindSetFields, Actor: actor, Clock: clock, Node: node,
		Fields: map[string]json.RawMessage{FieldText: raw(t, text)},
	}
}

// The two ends of one disagreement, which is the whole point of the write log:
// whether this replica notices a loss at merge time or later depends only on
// who had network when, and it has to notice either way.
func TestWriteLogNoticesBothEnds(t *testing.T) {
	t.Run("ours arrives second and loses", func(t *testing.T) {
		var state ops.State
		var log writeLog

		// Theirs first, from the server.
		theirs, err := state.MergeAll([]ops.Op{setText(t, "o1", "them", 9, "n1", "theirs")})
		if err != nil {
			t.Fatal(err)
		}
		if got := log.displaced(theirs); len(got) != 0 {
			t.Errorf("a field we never wrote was reported: %+v", got)
		}

		// Now ours, with a lower clock.
		batch := []ops.Op{setText(t, "o2", "us", 5, "n1", "ours")}
		mine, err := state.MergeAll(batch)
		if err != nil {
			t.Fatal(err)
		}
		found := log.track(mine, func(node, field string) json.RawMessage {
			return wanted(batch, node, field)
		})
		if len(found) != 1 {
			t.Fatalf("conflicts = %+v, want one", found)
		}
		if found[0].Yours != "ours" || found[0].Now != "theirs" {
			t.Errorf("reported %+v, want yours=ours now=theirs", found[0])
		}
	})

	t.Run("ours arrives first and is displaced later", func(t *testing.T) {
		var state ops.State
		var log writeLog

		batch := []ops.Op{setText(t, "o1", "us", 5, "n1", "ours")}
		mine, err := state.MergeAll(batch)
		if err != nil {
			t.Fatal(err)
		}
		if found := log.track(mine, func(node, field string) json.RawMessage {
			return wanted(batch, node, field)
		}); len(found) != 0 {
			t.Fatalf("a write that took was reported as a loss: %+v", found)
		}

		theirs, err := state.MergeAll([]ops.Op{setText(t, "o2", "them", 9, "n1", "theirs")})
		if err != nil {
			t.Fatal(err)
		}
		found := log.displaced(theirs)
		if len(found) != 1 {
			t.Fatalf("conflicts = %+v, want one", found)
		}
		if found[0].Yours != "ours" {
			t.Errorf("reported %+v, want the text we lost", found[0])
		}
	})
}

// A field this replica never touched being overwritten is the document moving,
// not a loss. Without this the app would show a note every time anybody typed.
func TestWriteLogIgnoresOtherPeoplesFields(t *testing.T) {
	var state ops.State
	var log writeLog

	first, _ := state.MergeAll([]ops.Op{setText(t, "o1", "them", 5, "n1", "theirs")})
	second, _ := state.MergeAll([]ops.Op{setText(t, "o2", "other", 9, "n1", "somebody else's")})

	if got := log.displaced(first); len(got) != 0 {
		t.Errorf("reported %+v", got)
	}
	if got := log.displaced(second); len(got) != 0 {
		t.Errorf("reported %+v", got)
	}
}

// One disagreement, one note. A field is forgotten the moment its loss is
// reported, so a third write does not produce a second note about the same
// text this replica no longer has any claim on.
func TestOneConflictIsReportedOnce(t *testing.T) {
	var state ops.State
	var log writeLog

	batch := []ops.Op{setText(t, "o1", "us", 5, "n1", "ours")}
	mine, _ := state.MergeAll(batch)
	log.track(mine, func(node, field string) json.RawMessage { return wanted(batch, node, field) })

	theirs, _ := state.MergeAll([]ops.Op{setText(t, "o2", "them", 9, "n1", "theirs")})
	if got := log.displaced(theirs); len(got) != 1 {
		t.Fatalf("first report = %+v", got)
	}

	later, _ := state.MergeAll([]ops.Op{setText(t, "o3", "them", 20, "n1", "theirs again")})
	if got := log.displaced(later); len(got) != 0 {
		t.Errorf("reported a second time: %+v", got)
	}
}

// Different fields of one node are not a conflict, which is the case the
// field-level merge exists for.
func TestDifferentFieldsAreNotAConflict(t *testing.T) {
	var state ops.State
	var log writeLog

	batch := []ops.Op{setText(t, "o1", "us", 5, "n1", "ours")}
	mine, _ := state.MergeAll(batch)
	log.track(mine, func(node, field string) json.RawMessage { return wanted(batch, node, field) })

	status := ops.Op{
		ID: "o2", Kind: ops.KindSetFields, Actor: "them", Clock: 9, Node: "n1",
		Fields: map[string]json.RawMessage{FieldStatus: raw(t, "done")},
	}
	theirs, _ := state.MergeAll([]ops.Op{status})
	if got := log.displaced(theirs); len(got) != 0 {
		t.Errorf("reported %+v", got)
	}
}

// A replay is not a conflict. Retries after a timeout are the normal case, and
// a note on each would make the feature unusable.
func TestReplayIsNotAConflict(t *testing.T) {
	var state ops.State
	var log writeLog

	batch := []ops.Op{setText(t, "o1", "us", 5, "n1", "ours")}
	mine, _ := state.MergeAll(batch)
	log.track(mine, func(node, field string) json.RawMessage { return wanted(batch, node, field) })

	again, _ := state.MergeAll(batch)
	if got := log.displaced(again); len(got) != 0 {
		t.Errorf("a replay reported %+v", got)
	}
	if got := log.track(again, func(node, field string) json.RawMessage {
		return wanted(batch, node, field)
	}); len(got) != 0 {
		t.Errorf("a replay reported %+v", got)
	}
}

// The log is bounded. A note about a line edited four hundred edits ago is not
// a conflict, it is archaeology, and an unbounded map here would grow for the
// life of the process.
func TestWriteLogIsBounded(t *testing.T) {
	var log writeLog
	for i := range maxTrackedWrites + 50 {
		log.remember("n"+string(rune('a'+i%26))+string(rune('a'+i/26)), FieldText, raw(t, i))
	}
	if len(log.values) > maxTrackedWrites {
		t.Errorf("holding %d writes, over the %d bound", len(log.values), maxTrackedWrites)
	}
	if len(log.order) != len(log.values) {
		t.Errorf("order has %d and values %d; they have drifted", len(log.order), len(log.values))
	}
}

// Nothing in a conflict names anybody. The transport compares actors to tell
// its own ops from everyone else's and the merge uses one as a tiebreak;
// neither reason reaches a note.
func TestConflictNamesNobody(t *testing.T) {
	var state ops.State
	var log writeLog

	batch := []ops.Op{setText(t, "o1", "this-machine", 5, "n1", "ours")}
	mine, _ := state.MergeAll(batch)
	log.track(mine, func(node, field string) json.RawMessage { return wanted(batch, node, field) })

	theirs, _ := state.MergeAll([]ops.Op{setText(t, "o2", "their-machine", 9, "n1", "theirs")})
	found := log.displaced(theirs)
	if len(found) != 1 {
		t.Fatal("no conflict to check")
	}

	encoded, err := json.Marshal(found[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"this-machine", "their-machine"} {
		if bytesContain(encoded, actor) {
			t.Errorf("an actor reached the note: %s", encoded)
		}
	}
}

func bytesContain(haystack []byte, needle string) bool {
	return len(needle) > 0 && json.Valid(haystack) && containsSub(string(haystack), needle)
}

func containsSub(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Both directions of a delete beating an edit. They are different code paths --
// one is the merge, one is the server's refusal on the next push -- and they
// have to read as the same sentence to the person.
func TestADeleteIsReported(t *testing.T) {
	t.Run("the delete arrived first, so our edit lands on a tombstone", func(t *testing.T) {
		var state ops.State
		var log writeLog

		if _, err := state.MergeAll([]ops.Op{
			{ID: "d1", Kind: ops.KindDeleteNode, Actor: "them", Clock: 9, Node: "n1"},
		}); err != nil {
			t.Fatal(err)
		}

		batch := []ops.Op{setText(t, "o1", "us", 10, "n1", "typed into a doomed line")}
		mine, err := state.MergeAll(batch)
		if err != nil {
			t.Fatal(err)
		}
		found := log.track(mine, func(node, field string) json.RawMessage {
			return wanted(batch, node, field)
		})

		var deleted *ConflictEvent
		for i := range found {
			if found[i].Deleted {
				deleted = &found[i]
			}
		}
		if deleted == nil {
			t.Fatalf("no delete reported; got %+v", found)
		}
		if deleted.Node != "n1" {
			t.Errorf("node = %q", deleted.Node)
		}
		// The text, because the line is not coming back and the work in it
		// should be recoverable by hand.
		if deleted.Yours != "typed into a doomed line" {
			t.Errorf("yours = %q, want the text that was typed", deleted.Yours)
		}
		// No undo is offered for a delete, so there is nothing to put back and
		// nothing claiming there is.
		if deleted.Now != "" {
			t.Errorf("now = %q, want empty for a deleted line", deleted.Now)
		}
	})

	t.Run("a live node is not reported", func(t *testing.T) {
		var state ops.State
		var log writeLog
		batch := []ops.Op{setText(t, "o1", "us", 5, "n1", "ours")}
		mine, _ := state.MergeAll(batch)
		found := log.track(mine, func(node, field string) json.RawMessage {
			return wanted(batch, node, field)
		})
		for _, c := range found {
			if c.Deleted {
				t.Errorf("an edit to a live node was reported as deleted: %+v", c)
			}
		}
	})
}

// A 409 is refused now and refused forever, so the loop must stop asking --
// otherwise the rest of the outbox waits behind an op that can never land, and
// the workspace silently stops syncing.
func TestADeleteConflictIsFatal(t *testing.T) {
	conflict := &APIError{Status: 409, Code: "node_deleted", Message: "op o1 targets node n1, which was deleted at seq 12"}
	if !conflict.Fatal() {
		t.Error("a node_deleted refusal is retried forever")
	}
	if !conflict.Deleted() {
		t.Error("a node_deleted refusal is not recognised as one")
	}

	// A 409 that is not this is still fatal -- a conflict the server names is
	// not a thing a retry fixes -- but it is not a delete to report.
	other := &APIError{Status: 409, Code: "something_else"}
	if other.Deleted() {
		t.Error("an unrelated conflict was taken for a delete")
	}

	// And an ordinary failure still retries, because most of them are a
	// network that came back.
	for _, status := range []int{500, 502, 503, 429} {
		if (&APIError{Status: status}).Fatal() {
			t.Errorf("status %d is treated as permanent", status)
		}
	}
}
