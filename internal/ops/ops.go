// Package ops is the vocabulary of change shared by the desktop app and the
// sync server: what an edit is, and what happens when two of them collide.
//
// Work is edited in more than one place — the desktop app you are sitting in
// front of, and whatever else has your keys — and those places are not always
// online at the same time. So an edit is not "write this value"; it is an
// operation with enough information attached to be merged with operations it
// never saw. Replay the same set of ops in any order, on any machine, and you
// get the same result.
//
// There are three rules, and everything here exists to make them true:
//
//  1. Fields are last-write-wins, one field at a time. Two replicas editing
//     different fields of a node both keep their edit; two editing the same
//     field are decided by [Stamp].
//  2. Delete beats a concurrent edit, in either order. Deleting is one-way: no
//     clock value undoes it, and a node that has been deleted anywhere is
//     deleted everywhere, whichever of the two arrived first.
//  3. Ops are idempotent by ID. Applying one twice changes nothing the second
//     time, so a client replaying its outbox after a lost response is safe.
//
// The package deliberately knows nothing about transport or storage. The
// server stores ops and hands them back in order; the order it hands them back
// in is arrival order and has no bearing on the merge. That separation is the
// point: there is one merge implementation and both sides run it.
package ops

import (
	"cmp"
	"encoding/json"
	"fmt"
)

// Kind is what an op does.
type Kind string

const (
	// KindCreateNode creates a node with a placement and initial fields.
	KindCreateNode Kind = "create-node"
	// KindSetFields merges field values into a node, one field at a time.
	KindSetFields Kind = "set-fields"
	// KindDeleteNode tombstones a node, permanently.
	KindDeleteNode Kind = "delete-node"
	// KindMoveNode re-parents a node and repositions it among its siblings.
	KindMoveNode Kind = "move-node"
	// KindExtractToTask creates a task node out of an existing node and links
	// the two together.
	KindExtractToTask Kind = "extract-to-task"
)

// Valid reports whether a kind is one this package can apply. Ops arrive from
// the network and from older versions of the app, so a kind is never trusted.
func (k Kind) Valid() bool {
	switch k {
	case KindCreateNode, KindSetFields, KindDeleteNode, KindMoveNode, KindExtractToTask:
		return true
	}
	return false
}

// Fields written by [KindExtractToTask]. They are ordinary last-write-wins
// fields with a conventional name — nothing in the merge treats them specially,
// and a client may set them itself. They exist so the two halves of an
// extraction can find each other without a second kind of edge in the model.
const (
	// FieldTaskID names, on the source node, the task extracted from it.
	FieldTaskID = "taskId"
	// FieldExtractedFrom names, on the task node, where it came from.
	FieldExtractedFrom = "extractedFrom"
)

// Limits on the strings an op carries. They are here rather than in the server
// so that a desktop replica rejects an op it could not have sent, and so both
// sides agree on what is too big without coordinating.
const (
	// MaxIDLen caps an op, node, actor or task identifier.
	MaxIDLen = 128
	// MaxPositionLen caps a sort key.
	MaxPositionLen = 256
	// MaxFieldNameLen caps a field name.
	MaxFieldNameLen = 128
	// MaxFields caps how many fields one op may carry.
	MaxFields = 256
)

// Stamp decides which of two writes to the same slot wins.
//
// Clock is a Lamport counter: a replica issues each op with a clock strictly
// greater than every clock it has seen or issued, so an op that could have been
// aware of another always outranks it. Actor breaks the tie when two replicas
// pick the same clock, which is what "concurrent" looks like from the inside.
// Any total order would do for the tiebreak as long as everyone uses the same
// one; comparing the actor string is the cheapest one that needs no agreement.
//
// The zero Stamp loses to every real one, because [Op.Validate] rejects a clock
// of zero. That is what lets an empty slot be the zero value rather than a
// special case.
type Stamp struct {
	Clock uint64 `json:"clock"`
	Actor string `json:"actor"`
}

// Compare orders two stamps, returning -1, 0 or +1.
func (s Stamp) Compare(other Stamp) int {
	if c := cmp.Compare(s.Clock, other.Clock); c != 0 {
		return c
	}
	return cmp.Compare(s.Actor, other.Actor)
}

// After reports whether s wins against other. A stamp does not win against
// itself, so re-applying an op is never a write.
func (s Stamp) After(other Stamp) bool { return s.Compare(other) > 0 }

// Op is one edit, self-contained enough to merge with edits it never saw.
//
// It is a single struct with a Kind rather than an interface with a type per
// kind, because an op spends most of its life as JSON — in a request body, in a
// Postgres column, in a viewer written in another language. One struct
// round-trips through all of that with no custom marshalling, and the fields a
// kind does not use are simply absent. Which fields belong to which kind is
// enforced by [Op.Validate] rather than by the type system.
type Op struct {
	// ID is unique across the workspace and is the idempotency key. A ULID or
	// UUID; the package does not care which, only that it is unique.
	ID string `json:"id"`
	// Kind is what the op does.
	Kind Kind `json:"kind"`
	// Actor is the replica that wrote the op, and the tiebreak in Stamp.
	Actor string `json:"actor"`
	// Clock is the Lamport counter, always at least 1.
	Clock uint64 `json:"clock"`
	// Node is the node the op targets. For KindExtractToTask it is the source
	// node, not the task being created.
	Node string `json:"node"`

	// Fields carries field values for the kinds that write them. Values are
	// arbitrary JSON; this package never looks inside one.
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
	// Parent is the node to place under. Empty means a root, so an absent
	// parent and a move to the top level are the same thing.
	Parent string `json:"parent,omitempty"`
	// Position sorts a node among its siblings and is compared as a string, so
	// a client can insert between two neighbours without renumbering anyone.
	Position string `json:"position,omitempty"`
	// Task is the node ID of the task being created, for KindExtractToTask.
	Task string `json:"task,omitempty"`
}

// Stamp is the op's position in the merge order.
func (o Op) Stamp() Stamp { return Stamp{Clock: o.Clock, Actor: o.Actor} }

// Validate reports whether the op is one this package can apply.
//
// Every op is checked, including ones read back from the server's own log: the
// log is append-only and outlives any single version of this code, so an op
// written by a future release can arrive here. Rejecting it is right, and
// silently half-applying it is not.
func (o Op) Validate() error {
	if err := checkID("id", o.ID); err != nil {
		return err
	}
	if !o.Kind.Valid() {
		return fmt.Errorf("op %s: unknown kind %q", o.ID, o.Kind)
	}
	if err := checkID("actor", o.Actor); err != nil {
		return fmt.Errorf("op %s: %w", o.ID, err)
	}
	// A zero clock would tie with an untouched slot and make the winner depend
	// on the actor string alone, so it is not a valid clock.
	if o.Clock == 0 {
		return fmt.Errorf("op %s: clock is zero", o.ID)
	}
	if err := checkID("node", o.Node); err != nil {
		return fmt.Errorf("op %s: %w", o.ID, err)
	}
	if len(o.Position) > MaxPositionLen {
		return fmt.Errorf("op %s: position is %d bytes, over the %d limit", o.ID, len(o.Position), MaxPositionLen)
	}
	if err := o.checkFields(); err != nil {
		return fmt.Errorf("op %s: %w", o.ID, err)
	}

	switch o.Kind {
	case KindSetFields:
		// A set-fields with nothing to set is a client bug that would otherwise
		// take up a sequence number and do nothing.
		if len(o.Fields) == 0 {
			return fmt.Errorf("op %s: set-fields with no fields", o.ID)
		}
	case KindCreateNode, KindMoveNode:
		if o.Parent == o.Node {
			return fmt.Errorf("op %s: node %s is its own parent", o.ID, o.Node)
		}
	case KindExtractToTask:
		if err := checkID("task", o.Task); err != nil {
			return fmt.Errorf("op %s: %w", o.ID, err)
		}
		if o.Task == o.Node {
			return fmt.Errorf("op %s: node %s is extracted from itself", o.ID, o.Node)
		}
		if o.Parent == o.Task {
			return fmt.Errorf("op %s: task %s is its own parent", o.ID, o.Task)
		}
	}
	if o.Parent != "" && len(o.Parent) > MaxIDLen {
		return fmt.Errorf("op %s: parent is %d bytes, over the %d limit", o.ID, len(o.Parent), MaxIDLen)
	}
	return nil
}

func (o Op) checkFields() error {
	if len(o.Fields) > MaxFields {
		return fmt.Errorf("%d fields, over the %d limit", len(o.Fields), MaxFields)
	}
	for name, value := range o.Fields {
		switch {
		case name == "":
			return fmt.Errorf("field name is empty")
		case len(name) > MaxFieldNameLen:
			return fmt.Errorf("field %q name is %d bytes, over the %d limit", name, len(name), MaxFieldNameLen)
		case !json.Valid(value):
			return fmt.Errorf("field %q value is not JSON", name)
		}
	}
	return nil
}

func checkID(what, id string) error {
	if id == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if len(id) > MaxIDLen {
		return fmt.Errorf("%s is %d bytes, over the %d limit", what, len(id), MaxIDLen)
	}
	return nil
}

// stringValue renders a node ID as a JSON string, for the fields
// KindExtractToTask writes. Marshalling a string is the one case encoding/json
// cannot fail at, so the error is dropped rather than plumbed through a merge
// that has nothing to do with it.
func stringValue(s string) json.RawMessage {
	encoded, _ := json.Marshal(s)
	return encoded
}
