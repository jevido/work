package ops

import "encoding/json"

// Outcome is what merging one op actually did to the document.
//
// It exists because the merge used to lose silently. [node.setField] and
// [node.moveTo] both return without a word when the stamp they carry loses,
// which is correct — that is what last-write-wins means — and leaves nothing
// anywhere able to say a write was overwritten. A person who renames a line and
// watches somebody else's text appear instead has been given no account of it,
// and no way back.
//
// So the merge reports, and the report carries the value that was there before.
// That detail is the whole usefulness of it: "you lost" is a notification, and
// "you lost, and here is what you wrote" is something a person can undo.
//
// # No actor, deliberately
//
// A [Stamp] carries an actor because the merge needs a tiebreak when two ops
// share a clock, and that is the only reason it exists. Nothing here passes it
// outward. A report that named the actor would turn a merge detail into an
// attribution channel, and the first UI written against it would put a name on
// a note — in a workspace whose design is that nobody can be named. This is the
// cheapest place to make that impossible, so it is made impossible here.
//
// A caller that needs to know whether a lost write was *its own* knows that
// already: it is the replica that issued the op, and it knows which ops it
// issued.
type Outcome struct {
	// OpID is the op this describes.
	OpID string
	// Fresh is false when the op had already been applied, in which case
	// nothing else here is set. An idempotent replay is not a conflict, and
	// reporting one as a loss would produce a note on every retry.
	Fresh bool
	// Fields is every field this op wrote or tried to write, in name order so
	// that the same op reported twice reads the same way.
	Fields []FieldChange
	// Moves is every placement this op set or tried to set.
	Moves []MoveChange
	// Tombstones are the nodes this op wrote to that are deleted.
	//
	// The write still happened — a tombstone keeps its fields, so a viewer can
	// say what was deleted rather than that something was — but it will not
	// appear in the tree, and to whoever wrote it that is indistinguishable
	// from the edit vanishing.
	Tombstones []string
}

// Changed reports whether anything in this outcome is worth telling a person
// about: a write that did not take, or one that landed on a tombstone.
//
// A write that took and replaced nothing is the ordinary case and the reason
// this exists as a method rather than as a check at every call site.
func (o Outcome) Changed() bool {
	if len(o.Tombstones) > 0 {
		return true
	}
	for _, f := range o.Fields {
		if !f.Took || len(f.Was) > 0 {
			return true
		}
	}
	for _, m := range o.Moves {
		if !m.Took {
			return true
		}
	}
	return false
}

// FieldChange is one field an op wrote or tried to write.
type FieldChange struct {
	// Node is the node the field belongs to. Not always the op's own Node:
	// extract-to-task writes to the task it creates and to the idea it came
	// from, and the two are different nodes.
	Node string
	// Field is the field name.
	Field string
	// Took is whether this op's value is the one in the document now.
	//
	// False means a higher stamp was already there: this op lost, and [Was] is
	// the value that stayed. True with a non-empty [Was] means the opposite --
	// this op won, and Was is what it displaced.
	//
	// The two cases are the same conflict seen from either end, which is why
	// they are one field rather than two kinds of report. Whether a replica
	// merges its own op before or after the one that beats it depends entirely
	// on who had network when.
	Took bool
	// Was is the value that was in this field before this op was applied, or
	// nil if the field had never been written.
	//
	// This is what an undo restores and what a note quotes. It is a copy: the
	// State it came from may be written to again without changing it.
	Was json.RawMessage
}

// MoveChange is one placement an op set or tried to set.
//
// Parent and position are reported together because they are stamped together.
// A move that wins takes both halves and a move that loses takes neither; there
// is no outcome where a node ends up under one replica's parent at another's
// position, and a report that suggested there were would be describing a
// document that cannot exist.
type MoveChange struct {
	Node string
	// Took is whether this op's placement is the one in force.
	Took bool
	// WasParent and WasPosition are where the node sat before. Both empty for a
	// node nothing had placed yet.
	WasParent   string
	WasPosition string
}
