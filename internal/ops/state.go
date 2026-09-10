package ops

import (
	"bytes"
	"cmp"
	"encoding/json"
	"iter"
	"maps"
	"slices"
)

// Node is one node as it stands after a merge. It is a snapshot: changing it
// changes nothing in the [State] it came from.
type Node struct {
	ID string `json:"id"`
	// Parent is empty for a root.
	Parent string `json:"parent,omitempty"`
	// Position sorts the node among its siblings.
	Position string `json:"position,omitempty"`
	// Fields are the winning value for each field written to this node.
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
	// Deleted marks a tombstone. A tombstone keeps its fields, so a viewer can
	// say what was deleted rather than that something was; it is simply not in
	// the tree any more.
	Deleted bool `json:"deleted,omitempty"`
}

// TreeNode is a node together with the nodes under it, in position order.
type TreeNode struct {
	Node
	Children []TreeNode `json:"children,omitempty"`
}

// value is one field's winning content and the stamp that won it.
type value struct {
	data  json.RawMessage
	stamp Stamp
}

// placement is where a node sits: under which parent, and where among its
// siblings.
//
// Parent and position share one stamp rather than having one each, because
// moving a node is a single act. With separate stamps, two concurrent moves
// could merge into a placement that neither replica asked for — one op's parent
// with the other's position — putting the node somewhere nobody moved it to.
type placement struct {
	parent   string
	position string
	stamp    Stamp
}

// node is the merge bookkeeping for one node: every slot carries the stamp that
// last won it, which is what makes the merge order-independent.
type node struct {
	at      placement
	fields  map[string]value
	deleted bool
}

// State is the result of merging a set of ops. Replaying the same ops in any
// order produces an equal State, so it does not matter whether ops arrive from
// the server's log, from a local edit, or from both at once.
//
// The zero State is ready to use. It is not safe for concurrent use; a caller
// that merges and reads from more than one goroutine wraps it.
type State struct {
	nodes map[string]*node
	// seen is every op ID applied, which is what makes Apply idempotent. It
	// grows with the log and is never pruned: an op ID that fell out would let
	// a replay through, and the whole point is that a replay cannot get through.
	seen  map[string]struct{}
	clock uint64
}

// Clock is the highest clock this State has seen.
//
// A replica issuing a new op takes a clock strictly greater than this, and
// strictly greater than any clock it has already issued in the same batch —
// two ops sharing a clock and an actor are indistinguishable to the merge, and
// which of them wins a contested field is then arbitrary.
func (s *State) Clock() uint64 { return s.clock }

// Len is how many nodes the State knows about, tombstones included.
func (s *State) Len() int { return len(s.nodes) }

// Applied reports whether an op with this ID has already been merged.
func (s *State) Applied(opID string) bool {
	_, ok := s.seen[opID]
	return ok
}

// Apply merges one op.
//
// It reports whether the op was new. An op that was already applied returns
// false and touches nothing. A new op returns true even if it changed no value
// — a set-fields that lost every field to a higher stamp, or any write to a
// tombstone, is still an op this State has now accounted for and must not apply
// again.
//
// An invalid op returns an error and changes nothing at all: validation runs
// before the first write, so there is no half-applied op to undo.
func (s *State) Apply(op Op) (bool, error) {
	if err := op.Validate(); err != nil {
		return false, err
	}
	if s.Applied(op.ID) {
		return false, nil
	}
	if s.seen == nil {
		s.seen = make(map[string]struct{})
		s.nodes = make(map[string]*node)
	}
	s.seen[op.ID] = struct{}{}
	s.clock = max(s.clock, op.Clock)

	stamp := op.Stamp()
	switch op.Kind {
	case KindCreateNode:
		target := s.touch(op.Node)
		target.moveTo(op.Parent, op.Position, stamp)
		target.setFields(op.Fields, stamp)

	case KindSetFields:
		s.touch(op.Node).setFields(op.Fields, stamp)

	case KindDeleteNode:
		// Tombstoning a node nobody has mentioned yet is deliberate: it is how a
		// delete that overtakes the create it refers to still wins when the
		// create catches up.
		s.touch(op.Node).deleted = true

	case KindMoveNode:
		s.touch(op.Node).moveTo(op.Parent, op.Position, stamp)

	case KindExtractToTask:
		task := s.touch(op.Task)
		task.moveTo(op.Parent, op.Position, stamp)
		task.setFields(op.Fields, stamp)
		task.setField(FieldExtractedFrom, stringValue(op.Node), stamp)
		// Extracting from a node that has since been deleted still produces the
		// task and still links back to it, so the tombstone records where its
		// content went.
		s.touch(op.Node).setField(FieldTaskID, stringValue(op.Task), stamp)
	}
	return true, nil
}

// ApplyAll merges ops in the order given and reports how many were new.
//
// It stops at the first invalid op and returns the error along with the count
// applied before it. Nothing needs unwinding: ops are order-independent and
// idempotent, so a caller that fixes the bad op and replays the whole batch
// gets the same State as one that never saw it.
func (s *State) ApplyAll(ops []Op) (int, error) {
	applied := 0
	for _, op := range ops {
		fresh, err := s.Apply(op)
		if err != nil {
			return applied, err
		}
		if fresh {
			applied++
		}
	}
	return applied, nil
}

// touch returns the bookkeeping for a node, creating it if this is the first op
// to mention it.
//
// Every kind creates on demand, including the ones that only edit. Ops arrive
// out of causal order — an edit can overtake the create it belongs to — and the
// alternative is dropping the edit, which loses data permanently for a message
// that is merely early. A node known only from edits has the zero placement,
// which loses to the create when it lands.
func (s *State) touch(id string) *node {
	if existing, ok := s.nodes[id]; ok {
		return existing
	}
	created := &node{fields: make(map[string]value)}
	s.nodes[id] = created
	return created
}

// moveTo places a node, if this stamp beats the one that put it where it is.
func (n *node) moveTo(parent, position string, stamp Stamp) {
	if !stamp.After(n.at.stamp) {
		return
	}
	n.at = placement{parent: parent, position: position, stamp: stamp}
}

func (n *node) setFields(fields map[string]json.RawMessage, stamp Stamp) {
	for name, data := range fields {
		n.setField(name, data, stamp)
	}
}

// setField writes one field, if this stamp beats the one already there.
//
// Deletion is not consulted. It is tempting to have a tombstone swallow later
// writes — it reads like what "delete beats an edit" means — but it does not
// converge: the tombstone would then keep whichever fields happened to arrive
// before the delete did, which is different on every replica. Rule two is about
// the node's existence, and existence is what the tombstone decides.
func (n *node) setField(name string, data json.RawMessage, stamp Stamp) {
	if !stamp.After(n.fields[name].stamp) {
		return
	}
	// The caller's map outlives this call and the op it came from may be
	// reused, so the bytes are copied rather than aliased.
	n.fields[name] = value{data: bytes.Clone(data), stamp: stamp}
}

// snapshot copies a node out of the merge bookkeeping, leaving the stamps
// behind.
func (n *node) snapshot(id string) Node {
	out := Node{
		ID:       id,
		Parent:   n.at.parent,
		Position: n.at.position,
		Deleted:  n.deleted,
	}
	if len(n.fields) > 0 {
		out.Fields = make(map[string]json.RawMessage, len(n.fields))
		for name, field := range n.fields {
			out.Fields[name] = bytes.Clone(field.data)
		}
	}
	return out
}

// Node returns one node, tombstones included.
func (s *State) Node(id string) (Node, bool) {
	found, ok := s.nodes[id]
	if !ok {
		return Node{}, false
	}
	return found.snapshot(id), true
}

// All iterates every node the State knows about, tombstones included, ordered
// by ID. The order is by ID rather than by placement so that it is stable
// whatever the tree looks like — for a tree, use [State.Tree].
func (s *State) All() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		for _, id := range slices.Sorted(maps.Keys(s.nodes)) {
			if !yield(s.nodes[id].snapshot(id)) {
				return
			}
		}
	}
}

// Tree returns the live nodes reachable from the roots, in position order.
//
// Tombstones are not in it, and neither is anything underneath one: a child
// created under a node that was concurrently deleted does not drag its parent
// back into the tree. Those nodes are still in the State — see [State.Detached].
func (s *State) Tree() []TreeNode {
	children := s.liveChildren()

	// Every Children slice is allocated at its final length before anything is
	// appended to it, so no append ever reallocates and the pointers this walk
	// keeps into parent slices stay valid for the whole descent.
	roots := make([]TreeNode, 0, len(children[""]))
	type frame struct {
		ids  []string
		into *[]TreeNode
		next int
	}
	stack := []frame{{ids: children[""], into: &roots}}
	// The walk is explicit rather than recursive because depth is bounded only
	// by what a client sent: a chain a hundred thousand nodes long is a strange
	// document, not a stack overflow.
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next == len(top.ids) {
			stack = stack[:len(stack)-1]
			continue
		}
		id := top.ids[top.next]
		top.next++

		*top.into = append(*top.into, TreeNode{Node: s.nodes[id].snapshot(id)})
		placed := &(*top.into)[len(*top.into)-1]
		if kids := children[id]; len(kids) > 0 {
			placed.Children = make([]TreeNode, 0, len(kids))
			stack = append(stack, frame{ids: kids, into: &placed.Children})
		}
	}
	return roots
}

// Detached returns the live nodes that [State.Tree] cannot reach, ordered by ID.
//
// A node ends up here for one of three reasons, and the distinction does not
// matter to a caller: its parent is a tombstone or sits under one, its parent is
// a node no op has ever mentioned, or concurrent moves put it in a cycle. All
// three are what an eventually-consistent tree looks like mid-convergence, and
// the last one can outlive convergence, so the nodes are surfaced rather than
// silently dropped. A viewer that ignores this is showing an incomplete
// workspace; one that shows it as a second list is showing all of it.
func (s *State) Detached() []Node {
	children := s.liveChildren()

	reachable := make(map[string]bool, len(s.nodes))
	queue := slices.Clone(children[""])
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		queue = append(queue, children[id]...)
	}

	var detached []Node
	for _, id := range slices.Sorted(maps.Keys(s.nodes)) {
		if n := s.nodes[id]; !n.deleted && !reachable[id] {
			detached = append(detached, n.snapshot(id))
		}
	}
	return detached
}

// liveChildren indexes the non-tombstoned nodes by parent, each list in the
// order they are rendered in. A node whose parent is a tombstone still appears
// under it here; the walk simply never reaches that parent.
func (s *State) liveChildren() map[string][]string {
	children := make(map[string][]string, len(s.nodes))
	for id, n := range s.nodes {
		if n.deleted {
			continue
		}
		children[n.at.parent] = append(children[n.at.parent], id)
	}
	for _, siblings := range children {
		s.sortSiblings(siblings)
	}
	return children
}

// sortSiblings orders nodes by position, then by ID.
//
// The ID tiebreak is not cosmetic: two replicas can pick the same position for
// two different nodes, and without a deterministic second key they would render
// in map order — which is to say, differently on every machine and on every run.
func (s *State) sortSiblings(ids []string) {
	slices.SortFunc(ids, func(a, b string) int {
		if c := cmp.Compare(s.nodes[a].at.position, s.nodes[b].at.position); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
}
