package workbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dev.jevido/work/packages/ops"
)

// The orientation and isolation half of the workspace document.
//
// document.go holds the other half: a tab is a root node and this machine's
// board cards are children of it. This file adds the two views a person
// actually types into -- the outline they think in, and the tasks they order
// it into -- and they live in the same tree, under the same tab node, because
// there is one log and one merge.
//
// The field names are conventions rather than protocol. services/sync/README.md is
// deliberately silent on what a field means, so these are the app's agreement
// with itself, and they are the same names the frontend and the web viewer
// already read: see frontend/src/lib/workspace/model.ts and web/src/lib/doc.ts.
// Changing one here changes it in three places, which is the price of one
// document that three programs can read.
//
// Where outline nodes hang is the one thing this settles that was open before.
// The frontend's local outline used "" -- a real root -- because a local
// document has no tab in it. A shared one does: a workspace has several tabs
// and their outlines are not each other's. So an outline root is a child of
// its tab node, and [Workbench.ApplyEdits] rewrites a caller's "" to the tab
// it was given rather than making every caller remember.
const (
	// FieldType is "task" for a line on the plan, and absent or "idea" for a
	// line of the outline. One tree holds both and this field is the only
	// thing that says which view a node belongs to.
	FieldType = "type"
	// TypeIdea and TypeTask are the values FieldType takes. An absent field
	// reads as TypeIdea: it is what every node written before the isolation
	// view existed says, and an outline line is the common case.
	TypeIdea = "idea"
	TypeTask = "task"
	// TypeEdge is a link between two nodes that are not parent and child.
	//
	// A node rather than a sixth op kind, and that is the whole design. An
	// edge is created with create-node and removed with delete-node, both of
	// which already merge correctly, survive being made offline, report a
	// conflict when they lose, and are understood by every client and by the
	// server. A new kind would have to earn all of that again, and an older
	// client meeting one has no defensible answer -- drop it and diverge
	// silently, or refuse it and wedge the queue. A node with a type nobody
	// recognises is a node nobody draws, which every client already handles.
	TypeEdge = "edge"
	// TypeRegion is a named set of nodes.
	//
	// Membership is a field on each member rather than a list on the region;
	// see FieldRegion for why.
	TypeRegion = "region"

	// TypeGuideline is something this workspace is trying to be: "improves
	// performance", "improves onboarding". Per workspace, because what counts
	// as progress is not the same on two projects.
	//
	// TypeParty is somebody waiting on a card: a person, a team, a customer.
	// It is content and not attribution -- a line of text somebody wrote, the
	// same as any other -- and nothing about it records who wrote it. A
	// workspace where nobody can be named can still say out loud that Sales is
	// waiting on something.
	TypeGuideline = "guideline"
	TypeParty     = "party"

	// TypeGuided joins a card to a guideline, and TypeInterest joins a card to
	// a party. FieldFrom is the card, FieldTo is the word.
	//
	// A node each rather than a field on the card, and the difference is the
	// same one FieldRegion turns on -- except the other way up. A card is in
	// one region, so a field is right for that. A card can meet four guidelines
	// and have three parties waiting on it, and a *set* in a field is one
	// last-write-wins slot: two people adding two different guidelines to one
	// card at the same moment would keep one and lose the other, silently. Two
	// nodes do not collide at all.
	TypeGuided   = "guided"
	TypeInterest = "interest"

	// FieldText is the line itself.
	FieldText = "text"
	// FieldFrom and FieldTo are the two ends of a [TypeEdge] node.
	//
	// An edge whose end has been deleted is dangling rather than broken: it
	// still says what it linked, which is more than the node it pointed at can
	// say for itself.
	FieldFrom = "from"
	FieldTo   = "to"

	// FieldRegion names the [TypeRegion] a node belongs to.
	//
	// On the member, not a list on the region, and the difference matters. A
	// list is one last-write-wins slot: two people adding different lines to
	// one region at the same moment would keep one and lose the other, with
	// nothing to show for it. A field per member is exactly the shape the
	// field-level merge exists for -- two people writing two different nodes
	// do not collide at all.
	FieldRegion = "region"

	// FieldIcon is the glyph on a cluster head, by name.
	//
	// A name from a closed set the frontend knows how to draw, not a
	// character: an emoji is a font lookup, and the font that answers it
	// differs per machine, so one workspace would look like two. A name this
	// release has never heard of is a glyph it leaves off, and the line itself
	// is untouched -- the same additive rule FieldDiscovery follows.
	FieldIcon = "icon"

	// FieldDetail is what a card says when there is room to say it.
	//
	// The board draws FieldText and nothing else: a note is read at a glance,
	// and a paragraph in a box is a paragraph nobody reads. This is the rest,
	// shown when a card is opened, and newlines survive in it -- which they
	// deliberately do not in FieldText.
	FieldDetail = "detail"

	// FieldCollapsed is whether an outline node's children are folded away.
	// It is in the document rather than a per-viewer preference because a
	// folded branch is how somebody says "this part is settled".
	FieldCollapsed = "collapsed"
)

// Task statuses. They are the isolation view's three columns, which are not the
// board's four: a board card can be blocked, a plan line cannot. See
// taskStatus for what happens to the fourth.
const (
	TaskTodo  = "todo"
	TaskDoing = "doing"
	TaskDone  = "done"
)

// Fields a run writes when it finds something the plan did not know.
//
// These are additive conventions: a viewer that has never heard of them shows
// the node as the ordinary outline line it is, which is the whole point of
// putting a discovery in the outline rather than in a report nobody reads.
const (
	// FieldDiscovery marks an idea node a run produced rather than a person,
	// and says which kind: see DiscoverySplit and friends.
	FieldDiscovery = "discovery"
	// FieldDiscoveredIn names the task node the run was working on when it
	// found this. It is not ops.FieldExtractedFrom, which is reserved for the
	// other direction -- idea to task -- and would invert the link a viewer
	// reads with sourceIdOf.
	FieldDiscoveredIn = "discoveredIn"
)

// editBatch is how many ops reach the disk under one fsync.
//
// Not a limit on what a caller may ask for. It used to be: a call over it was
// refused whole, on the reasoning that an unbounded batch is an unbounded
// write with the caller waiting on it. The reasoning was right and the refusal
// was the wrong answer to it -- the size of one disk write is this side's
// business, and making it the caller's meant every caller had to know the
// number and chunk against it. Approving one restructuring of a board is
// hundreds of edits in one gesture, and there is no honest number a *caller*
// could pick.
//
// So the write is bounded and the call is not: ApplyEdits validates everything
// it was given, then applies it a batch at a time.
const editBatch = 256

// Edit is one change to the outline, as the frontend describes it.
//
// It is an ops.Op with the three parts only this machine may mint left off.
// The frontend used to mint them itself, with its own actor and its own
// Lamport clock, which made two writers out of one machine: two clocks that
// never saw each other decide a contested field arbitrarily. So the actor and
// the clock come from the one Sync that owns this workspace, the op ID comes
// from mintID, and what crosses the bridge is intent.
//
// Fields are Go's `any` rather than json.RawMessage because this arrives from
// a webview as ordinary JSON; they are re-encoded on the way into an op.
type Edit struct {
	// Kind is the op kind: create-node, set-fields, delete-node, move-node or
	// extract-to-task. The same vocabulary as ops.Kind, deliberately, so there
	// is not a second set of names for the same five things.
	Kind ops.Kind `json:"kind"`
	// Node is the node to change, or the node to create. Empty on a create
	// mints one, which is what a caller with nothing to reconcile wants.
	Node string `json:"node,omitempty"`
	// Parent is what to hang it under. Empty means the tab itself -- the top
	// level of that tab's outline -- not the root of the whole document.
	Parent string `json:"parent,omitempty"`
	// Position is the sort key among its siblings. Empty appends, which is
	// what a new line at the end of an outline is. internal/workbench's
	// position.go and the frontend's position.ts are the same algorithm, so a
	// key either end mints is one the other can insert next to.
	Position string `json:"position,omitempty"`
	// Task is the task node to create, for extract-to-task. Empty mints one.
	Task string `json:"task,omitempty"`
	// Fields are the field values to write.
	Fields map[string]any `json:"fields,omitempty"`
}

// ApplyEdits writes outline and isolation edits into the workspace document.
//
// This is the call that was missing, and its absence is why the outline was a
// local document in the webview with no way out of it. With it, an idea typed
// in one tab is an op in the same queue, journal and log as everything else:
// it is on disk before this returns, it survives the webview's site data being
// cleared, and it reaches the server whenever there is one to reach.
//
// tabID is the tab being edited, which is not necessarily the tab agents run
// in -- looking at one tab while a run works in another is normal, and see
// ActivateTab for why the two are separate.
//
// The returned Document is the merged result, so a caller gets the tree back
// from the same call rather than having to ask for it and race its own edit.
func (w *Workbench) ApplyEdits(tabID string, edits []Edit) (Document, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return Document{}, errors.New("workbench: empty tab id")
	}
	if len(edits) == 0 {
		return w.WorkspaceDocument(), nil
	}

	s := w.sync.Load()
	if s == nil {
		// Not an error worth dressing up: a machine that has joined nothing
		// has no shared document to write to, and the caller is expected to
		// keep its own. ErrNoTransport is the same answer every other
		// workspace call gives.
		return Document{}, ErrNoTransport
	}

	w.wsMu.Lock()
	_, known := w.ws.Tab(tabID)
	w.wsMu.Unlock()
	if !known {
		return Document{}, fmt.Errorf("workbench: unknown tab %q", tabID)
	}

	// Everything is planned before anything is written, so one bad edit at the
	// end refuses the call rather than leaving the first half of it applied.
	// That is what the old size limit got right, and it is kept.
	planned, err := editOps(s, tabID, edits)
	if err != nil {
		return Document{}, err
	}

	// Then written a batch at a time. Each one is durable before the next
	// starts, which is the property the fsync is there for.
	//
	// A failure part way through leaves the batches before it applied. That is
	// a real change from refusing the whole call, and it is the better of the
	// two: these ops are already validated, so the ways left to fail are the
	// disk and the queue -- and on a full disk, keeping the first two hundred
	// edits somebody made is worth more than discarding them to be tidy. The
	// error says what happened; the document that comes back is what is true.
	for start := 0; start < len(planned); start += editBatch {
		end := min(start+editBatch, len(planned))
		if err := s.apply(planned[start:end]); err != nil {
			return Document{}, fmt.Errorf("workbench: applying edits %d-%d of %d: %w",
				start, end, len(planned), err)
		}
	}

	// Tasks may have appeared or been retired, so the board is reconciled
	// before the document goes back: the caller that just wrote a task should
	// see the card for it on the same turn.
	w.adoptTasks()
	return s.document(), nil
}

// editOps turns described edits into ops, minting what only this replica may
// mint and refusing what the outline has no business touching.
//
// Clocks are reserved once for the whole batch, in order, so two edits in one
// call that write the same field are decided by the order the caller wrote
// them rather than arbitrarily. See Sync.reserveClocks.
func editOps(s *Sync, tab string, edits []Edit) ([]ops.Op, error) {
	// Nodes this batch creates. Validation has to see them: a caller may
	// create a line and set a field on it in one call, and s.node cannot know
	// about the first edit while the second is being checked.
	fresh := make(map[string]struct{}, len(edits))

	planned := make([]ops.Op, 0, len(edits))
	for i, edit := range edits {
		op, err := editOp(s, tab, edit, fresh)
		if err != nil {
			return nil, fmt.Errorf("workbench: edit %d: %w", i, err)
		}
		planned = append(planned, op)
	}

	clock := s.reserveClocks(len(planned))
	for i := range planned {
		id, err := mintID()
		if err != nil {
			return nil, err
		}
		planned[i].ID = id
		planned[i].Actor = s.actor
		planned[i].Clock = clock + uint64(i)
	}
	return planned, nil
}

// editOp checks one edit and turns it into an op.
func editOp(s *Sync, tab string, edit Edit, fresh map[string]struct{}) (ops.Op, error) {
	if !edit.Kind.Valid() {
		return ops.Op{}, fmt.Errorf("unknown kind %q", edit.Kind)
	}

	node := strings.TrimSpace(edit.Node)
	if node == "" {
		if edit.Kind != ops.KindCreateNode {
			return ops.Op{}, fmt.Errorf("%s with no node", edit.Kind)
		}
		minted, err := mintID()
		if err != nil {
			return ops.Op{}, err
		}
		node = minted
	}

	switch edit.Kind {
	case ops.KindCreateNode:
		fresh[node] = struct{}{}
	default:
		// Everything else edits something that already exists, and what it
		// may edit is the outline -- not the tab strip, and not another
		// machine's board cards. Those are written by the workbench itself,
		// from state it owns, and a webview reaching them would have the tab
		// list and the board disagreeing with the config file that persists
		// them.
		if err := editable(s, node, fresh); err != nil {
			return ops.Op{}, err
		}
	}

	fields, err := jsonFields(edit.Fields)
	if err != nil {
		return ops.Op{}, err
	}

	op := ops.Op{Kind: edit.Kind, Node: node, Fields: fields}

	switch edit.Kind {
	case ops.KindSetFields:
		if len(op.Fields) == 0 {
			return ops.Op{}, errors.New("set-fields with no fields")
		}
		return op, nil
	case ops.KindDeleteNode:
		// A delete carries nothing else. Fields on one would be silently
		// dropped by the merge, so they are refused rather than accepted and
		// ignored.
		op.Fields = nil
		return op, nil
	}

	// What is left places a node: create, move, extract.
	placed := node
	if edit.Kind == ops.KindExtractToTask {
		task := strings.TrimSpace(edit.Task)
		if task == "" {
			minted, err := mintID()
			if err != nil {
				return ops.Op{}, err
			}
			task = minted
		} else if err := editable(s, task, fresh); err != nil {
			return ops.Op{}, fmt.Errorf("task: %w", err)
		}
		op.Task = task
		fresh[task] = struct{}{}
		placed = task
		// The extracted node is a task by definition. Saying so here rather
		// than trusting the caller is what keeps a task out of the outline
		// view it was just extracted from.
		if op.Fields == nil {
			op.Fields = make(map[string]json.RawMessage, 1)
		}
		op.Fields[FieldType], _ = json.Marshal(TypeTask)
	}

	parent, err := editParent(s, tab, edit.Parent, fresh)
	if err != nil {
		return ops.Op{}, err
	}
	if parent == placed {
		return ops.Op{}, fmt.Errorf("node %s is its own parent", placed)
	}
	op.Parent = parent

	op.Position = edit.Position
	if op.Position == "" {
		// Appending, which is what a new last line is. Resolved from the
		// document rather than from a count the caller keeps, so two people
		// appending at once both land at the end instead of on top of each
		// other.
		op.Position = between(tabTail(s, parent), "")
	}
	if len(op.Position) > ops.MaxPositionLen {
		return ops.Op{}, fmt.Errorf("position is %d bytes, over the %d limit", len(op.Position), ops.MaxPositionLen)
	}
	return op, nil
}

// editParent resolves the parent an edit asked for.
//
// An empty parent is the tab, not the document root. That is the one
// translation this layer does: the frontend's outline has always used "" for a
// top-level line, and a shared document's top level per tab is the tab node.
func editParent(s *Sync, tab, parent string, fresh map[string]struct{}) (string, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" || parent == tab {
		return tab, nil
	}
	if err := editable(s, parent, fresh); err != nil {
		return "", fmt.Errorf("parent: %w", err)
	}
	return parent, nil
}

// editable reports whether a node is one the outline may write to.
//
// The test is the presence of FieldKind, which only the workbench's own nodes
// carry: a tab has kind "tab" and a board card has kind "card". Testing for
// the field rather than for its value is deliberate -- a kind written by a
// newer release is still not the outline's to edit.
//
// A node this batch is creating passes, and so does one no op has mentioned
// yet: an edit can legitimately overtake the create it belongs to, and the
// merge is built for exactly that (see ops.State.touch).
func editable(s *Sync, node string, fresh map[string]struct{}) error {
	if _, ok := fresh[node]; ok {
		return nil
	}
	existing, ok := s.node(node)
	if !ok {
		return nil
	}
	if kind := fieldString(existing, FieldKind); kind != "" {
		return fmt.Errorf("node %s is a %s and is not the outline's to edit", node, kind)
	}
	return nil
}

// isTask reports whether a node is a line on the plan rather than a line of
// the outline. An absent type reads as an idea; see FieldType.
func isTask(node ops.Node) bool { return fieldString(node, FieldType) == TypeTask }

// tabTasks reads one tab's plan out of the merged document, in sibling order.
//
// Tasks are read from the whole of the tab's subtree rather than only its
// direct children. A task extracted from a nested idea is placed at the top
// level of the plan by the frontend, but nothing in the protocol makes it stay
// there -- a colleague may move it under another line -- and a plan that
// quietly dropped a task because somebody indented it would be a plan that
// lies about what is left to do.
func tabTasks(doc Document, tab string) []ops.Node {
	var out []ops.Node
	for _, root := range doc.Tree {
		if root.ID != tab {
			continue
		}
		collectTasks(root.Children, &out)
	}
	return out
}

func collectTasks(nodes []ops.TreeNode, out *[]ops.Node) {
	for _, n := range nodes {
		if isTask(n.Node) {
			*out = append(*out, n.Node)
		}
		collectTasks(n.Children, out)
	}
}
