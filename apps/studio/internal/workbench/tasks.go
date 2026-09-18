package workbench

import (
	"cmp"
	"errors"
	"strings"

	"dev.jevido/work/apps/studio/internal/board"
	"dev.jevido/work/packages/ops"
)

// The board's source and its outlet.
//
// Before this, a card existed because a run made one: Anton split a request,
// the board showed the split, and when the conversation was cleared the cards
// went with it. That is still true of a machine with no workspace, and it has
// to be -- see the gate in tabTasks' callers.
//
// With a workspace joined and a tab bound to a project, the board gains a
// source: the tasks somebody ordered the outline into, in isolation. Those are
// nodes in the shared document, they outlive a conversation, and they are what
// the work actually is. So they are adopted onto the board, and the board's
// progress is written back onto them -- one card per task, in both directions.
//
// The directions are deliberately not symmetric, because a tie has to be
// broken somewhere:
//
//   - The plan owns the words. A task's text is the title of its card, always,
//     because that is the sentence somebody wrote in isolation and a run that
//     renamed it would be arguing with the plan.
//   - The run owns the progress. A card that has started reports its own
//     column onto the task; the task's status is only read back onto a card
//     that has not started yet, or when it says done -- a colleague closing a
//     task in isolation closes the card here too.
//
// The cost with no workspace joined is one atomic load per call, which is what
// every other second-stage hook in this package costs. See shareBoard.

// maxDiscoveries bounds what one step may add to the outline.
//
// A specialist reporting eight things it found is reporting; a specialist
// reporting eighty is filling somebody's mindmap with its own transcript. The
// cap is on the run's side of the wire on purpose: an op that reaches the
// outbox is durable and shared, so the place to refuse it is before it gets
// there.
const maxDiscoveries = 8

// maxDiscoveryBytes caps one discovery's text. A line of an outline is a
// sentence, and a whole paragraph in one is a node nobody can read in the
// tree it lands in.
const maxDiscoveryBytes = 400

// adoptTasks reconciles this machine's board with the active tab's plan.
//
// It is called wherever either side can have moved: after a local outline
// edit, after a peer's ops merge, when a tab is activated or bound, and when
// the conversation is cleared. Each of those is a handful of nodes and a
// handful of cards, and the common case -- nothing to do -- costs one walk of
// the tab's subtree and no ops at all.
//
// The gate is the whole of the offline-first promise for this feature: no
// workspace, or an active tab with no folder on this machine, and this returns
// having done nothing. Work then behaves exactly as it always has, which is
// the requirement rather than a nicety -- a board fed from a document that
// does not exist would make the local-only path depend on a server.
func (w *Workbench) adoptTasks() {
	s := w.sync.Load()
	if s == nil {
		return
	}

	w.wsMu.Lock()
	var tab, dir string
	if w.ws != nil {
		tab = w.ws.ActiveTab
		dir = w.tabDirLocked(tab)
	}
	w.wsMu.Unlock()

	// A tab with no folder here cannot run anything, so a card for its work
	// would be a card nobody can start. tabDirLocked answers baseDir for an
	// unbound tab, which is how that is detected without a second lookup.
	if tab == "" || dir == "" || dir == w.baseDir {
		return
	}

	tasks := tabTasks(s.document(), tab)
	if len(tasks) == 0 {
		return
	}

	// One snapshot for the whole reconciliation rather than one per task.
	// Snapshot copies every card, and a plan with twenty lines against a board
	// with twenty cards would otherwise copy four hundred.
	onBoard := make(map[string]board.Card)
	for _, c := range w.board.Snapshot() {
		onBoard[c.ID] = c
	}

	changed := false
	for _, task := range tasks {
		title := strings.TrimSpace(fieldString(task, FieldText))
		if title == "" {
			// A blank line is a real state in an outliner -- you get one the
			// moment you press Enter -- and a card with no title is not worth
			// putting on a board. It is adopted the moment it says something.
			continue
		}

		if card, linked := w.cardForTask(task.ID); linked {
			if existing, ok := onBoard[card]; ok {
				if w.syncCard(existing, task, title) {
					changed = true
				}
				continue
			}
		}

		// New to this board: a task somebody wrote in isolation, or one this
		// machine has not seen since the conversation was cleared.
		//
		// No run ID and no agent: nobody has been given it yet. Anton picks it
		// up by task ID on the next turn, which is exactly what the board was
		// already for.
		id := w.board.Add("", "", title)
		w.linkTask(id, task.ID)
		// The task's status, not only "done". A card is created todo and
		// taskOps writes a card's status back, so a task that was already
		// underway got a todo card and then had todo written over it. Not a
		// loop -- both sides refuse to write a value that is already there --
		// but a status lost the first time the two mirrors met.
		if status := cardStatus(fieldString(task, FieldStatus)); status != board.StatusTodo {
			// A task already finished elsewhere arrives finished, rather than
			// as work to do that somebody has to close by hand.
			w.board.SetStatus(id, status, "")
		}
		changed = true
	}

	if changed {
		w.publishBoard()
	}
}

// syncCard brings one already-adopted card into line with its task, and
// reports whether anything moved.
func (w *Workbench) syncCard(card board.Card, task ops.Node, title string) bool {
	changed := false

	// The plan owns the words.
	if card.Title != title {
		w.board.Update(card.ID, title, "", "")
		changed = true
	}
	// The task says done and the card does not: somebody closed it in
	// planning, or on another machine, and the card follows. The reverse -- a
	// task moved back to todo under a card that is running -- is deliberately
	// not followed; see the note at the top of this file.
	// Every status, not only done. Mirroring one direction of one value meant
	// a task moved back to todo in the plan left its card sitting in done, and
	// the next board-to-plan pass wrote done back over the correction.
	if want := cardStatus(fieldString(task, FieldStatus)); want != card.Status {
		w.board.SetStatus(card.ID, want, "")
		changed = true
	}
	return changed
}

// cardForTask returns the board card standing in for a task node.
func (w *Workbench) cardForTask(task string) (string, bool) {
	w.linkMu.Lock()
	defer w.linkMu.Unlock()
	for card, node := range w.cardTask {
		if node == task {
			return card, true
		}
	}
	return "", false
}

// taskForCard returns the task node a card came from, or "" for a card a run
// invented.
func (w *Workbench) taskForCard(card string) string {
	w.linkMu.Lock()
	defer w.linkMu.Unlock()
	return w.cardTask[card]
}

// linkTask records that a card stands in for a task node.
//
// The link is in memory and not in the document, and that is right: it pairs a
// board ID -- "T3", minted per conversation on this machine -- with a node
// every replica can see. It is rebuilt by adoptTasks whenever the board is,
// which is what makes losing it on restart cost nothing.
func (w *Workbench) linkTask(card, task string) {
	w.linkMu.Lock()
	defer w.linkMu.Unlock()
	if w.cardTask == nil {
		w.cardTask = make(map[string]string)
	}
	w.cardTask[card] = task
}

// forgetTasks drops every link, for when the board is emptied.
func (w *Workbench) forgetTasks() {
	w.linkMu.Lock()
	w.cardTask = nil
	w.linkMu.Unlock()
}

// taskLinks is a snapshot of the card-to-task links, for building ops without
// holding the lock across a disk write.
func (w *Workbench) taskLinks() map[string]string {
	w.linkMu.Lock()
	defer w.linkMu.Unlock()
	if len(w.cardTask) == 0 {
		return nil
	}
	out := make(map[string]string, len(w.cardTask))
	for card, task := range w.cardTask {
		out[card] = task
	}
	return out
}

// taskOps writes the board's progress back onto the plan.
//
// This is the outlet half for the board: a card that moves to doing, done or
// blocked says so on the task node it came from, so planning shows what work
// is actually underway rather than what somebody last typed. It is a diff for
// the same reason boardOps is -- a publish that changed nothing produces no
// ops and never reaches the disk.
func taskOps(s *Sync, tab string, links map[string]string, cards []board.Card) ([]ops.Op, error) {
	if tab == "" || len(links) == 0 {
		return nil, nil
	}

	var planned []ops.Op
	for _, card := range cards {
		task := links[card.ID]
		if task == "" {
			continue
		}
		node, ok := s.node(task)
		if !ok || node.Deleted {
			// The task was retired while this machine still had a card for
			// it. A tombstone is permanent, so there is nothing to write.
			continue
		}

		want := map[string]any{FieldStatus: taskStatus(card.Status)}
		// A blocked card's reason is worth carrying: the plan showing a task
		// as underway with no hint that it is stuck is the version of this
		// that wastes somebody's afternoon.
		if card.Status == board.StatusBlocked && card.Note != "" {
			want[FieldNote] = card.Note
		}
		changed, err := changedFields(node, want)
		if err != nil {
			return nil, err
		}
		if len(changed) > 0 {
			planned = append(planned, ops.Op{Kind: ops.KindSetFields, Node: task, Fields: changed})
		}

		// The card node points at its task, once. It is what lets a viewer
		// pair the two halves without keeping this machine's board IDs, and
		// ops.FieldTaskID is the field the protocol already reserves for
		// exactly this direction.
		cardID := cardNode(s.actor, card.ID)
		if cardNodeState, ok := s.node(cardID); ok && !cardNodeState.Deleted {
			link, err := changedFields(cardNodeState, map[string]any{ops.FieldTaskID: task})
			if err != nil {
				return nil, err
			}
			if len(link) > 0 {
				planned = append(planned, ops.Op{Kind: ops.KindSetFields, Node: cardID, Fields: link})
			}
		}
	}
	if len(planned) == 0 {
		return nil, nil
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

// taskStatus maps a board column onto a plan status.
//
// The board has four columns and the plan has three, so blocked has nowhere of
// its own to go. It reports as doing, with the reason in FieldNote: a blocked
// task is started and not finished, which is what doing means, and calling it
// todo would say nobody had picked it up. An unknown column -- a board written
// by a newer release -- reads the same way rather than being dropped.
// cardStatus is taskStatus the other way round.
//
// Blocked has no opposite: the board has four states and the plan has three,
// and a blocked card is doing with a reason attached. Mapping it back to doing
// is what stops a round trip inventing a state the plan cannot hold.
func cardStatus(status string) board.Status {
	switch status {
	case TaskDone:
		return board.StatusDone
	case TaskDoing:
		return board.StatusDoing
	default:
		return board.StatusTodo
	}
}

func taskStatus(status board.Status) string {
	switch status {
	case board.StatusTodo:
		return TaskTodo
	case board.StatusDone:
		return TaskDone
	default:
		return TaskDoing
	}
}

// shareDiscoveries puts what a run found back into the mindmap.
//
// This is the other outlet, and it is the one that makes implementation mode a source of
// ideas rather than only a consumer of them. A specialist that finds a task is
// really two tasks, or that a line of attack is a dead end, or that something
// worth doing came up on the way, reports it (see the marker in plan.go) and
// it lands as a line of the outline next to the idea it came from -- where the
// next isolation pass will see it.
//
// It attaches to the idea the task was extracted from, when there is one, so a
// dead end is recorded against the thinking that led there. A task nobody
// extracted, and a run with no task at all, put their findings at the top
// level of the tab.
func (w *Workbench) shareDiscoveries(cardID string, found []Discovery) {
	if len(found) == 0 {
		return
	}
	s := w.sync.Load()
	if s == nil {
		// Nothing to push into: a machine with no workspace has no mindmap.
		// The specialist's own reply still carries what it found, and that is
		// where a local-only session reads it.
		return
	}

	w.wsMu.Lock()
	var tab, dir string
	if w.ws != nil {
		tab = w.ws.ActiveTab
		dir = w.tabDirLocked(tab)
	}
	w.wsMu.Unlock()
	if tab == "" || dir == "" || dir == w.baseDir {
		return
	}

	batch, err := discoveryOps(s, tab, w.taskForCard(cardID), found)
	if err != nil {
		s.setErr(err)
		s.publish()
		return
	}
	// The error is already on the sync status by the time apply returns, and
	// the run carries on either way: a discovery that could not be written is
	// still in the specialist's reply on screen.
	_ = s.apply(batch)
}

// discoveryOps places a run's findings in the outline.
func discoveryOps(s *Sync, tab, task string, found []Discovery) ([]ops.Op, error) {
	parent := tab
	if task != "" {
		// The idea the task came out of, if a person extracted it from one.
		// A source that has since been deleted falls back to the tab: a
		// tombstone takes its children with it, so hanging a finding off one
		// would put it where no tree can reach it.
		if node, ok := s.node(task); ok && !node.Deleted {
			source := fieldString(node, ops.FieldExtractedFrom)
			if idea, ok := s.node(source); source != "" && ok && !idea.Deleted {
				parent = source
			}
		}
	}

	// Resolved once, then walked forward. tabTail reads the whole node set,
	// so calling it per discovery would be a walk per line for no reason --
	// the keys only have to be increasing among themselves.
	tail := tabTail(s, parent)

	existing := discoveryTexts(s, parent)

	var planned []ops.Op
	for _, d := range found {
		text := strings.TrimSpace(d.Text)
		if text == "" {
			continue
		}
		if len(text) > maxDiscoveryBytes {
			text = strings.TrimSpace(text[:maxDiscoveryBytes])
		}
		// A step that is handed back after a block reports what it found
		// twice, and two agents can find the same thing. The outline is a
		// document a person reads, so the same sentence twice under one parent
		// is noise rather than information.
		if _, seen := existing[text]; seen {
			continue
		}
		existing[text] = struct{}{}

		fields, err := jsonFields(map[string]any{
			FieldType:      TypeIdea,
			FieldText:      text,
			FieldDiscovery: string(d.Kind),
			// Empty when the run had no task, in which case the line stands
			// on its own. Written as a field rather than left out so a viewer
			// can tell "found by a run, task unknown" from "typed by hand".
			FieldDiscoveredIn: task,
		})
		if err != nil {
			return nil, err
		}

		node, err := mintID()
		if err != nil {
			return nil, err
		}
		tail = between(tail, "")
		planned = append(planned, ops.Op{
			Kind:     ops.KindCreateNode,
			Node:     node,
			Parent:   parent,
			Position: tail,
			Fields:   fields,
		})
		if len(planned) == maxDiscoveries {
			break
		}
	}
	if len(planned) == 0 {
		return nil, nil
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

// discoveryTexts is the text of every discovery already recorded under a
// parent, so the same finding is not written twice.
func discoveryTexts(s *Sync, parent string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, node := range s.liveNodes() {
		if node.Parent != parent {
			continue
		}
		if fieldString(node, FieldDiscovery) == "" {
			continue
		}
		if text := strings.TrimSpace(fieldString(node, FieldText)); text != "" {
			out[text] = struct{}{}
		}
	}
	return out
}

// PlanTask is one task on the plan, as implementation mode needs to see it.
//
// Small on purpose: the id to act on, the words to show, and the idea it came
// from. No actor, no card, no position -- position is the plan's business and
// the only thing anybody outside it needs from the order is which one is next.
type PlanTask struct {
	// ID is the task node.
	ID string `json:"id"`
	// Text is what it says.
	Text string `json:"text"`
	// Status is todo, doing or done.
	Status string `json:"status"`
	// From is the idea this was extracted from, empty when the link is broken
	// or was never made.
	From string `json:"from,omitempty"`
	// FromText is that idea's text, so a caller can show the reason without a
	// second round trip for it.
	FromText string `json:"fromText,omitempty"`
}

// NextTask is the first task in plan order that is still to do.
//
// Not done, rather than todo. A task that is already `doing` is the next task:
// somebody started it and stopped, and offering the one after it would quietly
// skip work that is half finished.
//
// Nothing is an ordinary answer, not an error. An empty plan, a tab with no
// workspace, a tab this machine has bound to no folder, a plan where everything
// is finished -- all four mean there is nothing to offer, and none of them is a
// failure worth reporting as one.
func (w *Workbench) NextTask() (PlanTask, bool) {
	s := w.sync.Load()
	if s == nil {
		return PlanTask{}, false
	}

	w.wsMu.Lock()
	var tab, dir string
	if w.ws != nil {
		tab = w.ws.ActiveTab
		dir = w.tabDirLocked(tab)
	}
	w.wsMu.Unlock()
	// The same condition adoptTasks uses. A task that cannot be run in a folder
	// is not a task to offer, and a tab pointed at the directory Work was
	// started in is a tab pointed at nothing in particular.
	if tab == "" || dir == "" || dir == w.baseDir {
		return PlanTask{}, false
	}

	doc := s.document()
	// The ideas, by id, so the link back can carry its words with it. The link
	// is described as the spine of the whole system: a task without the reason
	// it exists is a line of text somebody has to go and look up.
	text := map[string]string{}
	for _, root := range doc.Tree {
		if root.ID != tab {
			continue
		}
		collectText(root.Children, text)
	}

	for _, task := range tabTasks(doc, tab) {
		if fieldString(task, FieldStatus) == TaskDone {
			continue
		}
		title := strings.TrimSpace(fieldString(task, FieldText))
		if title == "" {
			// A task with no words is one somebody started typing and left. It
			// cannot be run and it cannot be shown, so it is not next.
			continue
		}
		from := fieldString(task, ops.FieldExtractedFrom)
		return PlanTask{
			ID:       task.ID,
			Text:     title,
			Status:   cmp.Or(fieldString(task, FieldStatus), TaskTodo),
			From:     from,
			FromText: text[from],
		}, true
	}
	return PlanTask{}, false
}

func collectText(nodes []ops.TreeNode, into map[string]string) {
	for _, n := range nodes {
		into[n.ID] = strings.TrimSpace(fieldString(n.Node, FieldText))
		collectText(n.Children, into)
	}
}

// StartNextTask runs the next task on the plan, and marks it as being done.
//
// This is the sentence the whole loop turns on: tasks are sequenced, and work
// mode takes the next one. Before this, the order the plan held was decoration
// -- the board showed the tasks and a person picked.
//
// The run carries the idea the task came from. A run that sees only "Mount the
// static handler" has lost the reason it is being done, and that link is the
// spine of the system rather than a nicety.
func (w *Workbench) StartNextTask() (Task, error) {
	next, ok := w.NextTask()
	if !ok {
		// Three different situations, three different sentences. "There is
		// nothing left" and "this tab has nowhere to run" are not the same
		// news, and only one of them is good.
		if w.sync.Load() == nil {
			return Task{}, errors.New("workbench: no workspace, so there is no plan to take from")
		}
		w.wsMu.Lock()
		tab := ""
		dir := ""
		if w.ws != nil {
			tab = w.ws.ActiveTab
			dir = w.tabDirLocked(tab)
		}
		w.wsMu.Unlock()
		if tab == "" || dir == "" || dir == w.baseDir {
			return Task{}, errors.New("workbench: this tab has no folder on this machine, so there is nowhere to run")
		}
		return Task{}, errors.New("workbench: nothing left on the plan")
	}

	// Started before it is marked, so a refused run -- no CLI, one already in
	// flight -- does not leave a task claiming to be underway with nothing
	// working on it.
	//
	// Filed against the active tab's work transcript. Nobody typed this: it
	// came off the plan, and the plan belongs to a tab -- so it belongs in the
	// conversation somebody watching that tab is looking at, which is the one
	// place they would go to see what happened.
	w.wsMu.Lock()
	active := ""
	if w.ws != nil {
		active = w.ws.ActiveTab
	}
	w.wsMu.Unlock()

	task, err := w.Submit(Conversation{TabID: active, Mode: ModeImplementation}, "", taskPrompt(next))
	if err != nil {
		return Task{}, err
	}

	w.wsMu.Lock()
	tab := ""
	if w.ws != nil {
		tab = w.ws.ActiveTab
	}
	w.wsMu.Unlock()
	if next.Status != TaskDoing && tab != "" {
		// Through the same edit path everything else uses. A status written
		// here by hand would be a second writer with its own clock.
		if _, err := w.ApplyEdits(tab, []Edit{{
			Kind:   ops.KindSetFields,
			Node:   next.ID,
			Fields: map[string]any{FieldStatus: TaskDoing},
		}}); err != nil {
			// The run is already going, so this is worth saying and not worth
			// failing on: the task is being done either way and the status
			// will be corrected the next time anybody touches it.
			w.emit(EventWorkspaceChanged, WorkspaceEvent{})
		}
	}
	return task, nil
}

// taskPrompt is what the run is asked to do.
//
// The task, then the idea underneath it. Plainly, because this is read by
// Claude and by whoever reads the transcript afterwards.
func taskPrompt(next PlanTask) string {
	var b strings.Builder
	b.WriteString(next.Text)
	if next.FromText != "" {
		b.WriteString("\n\nThis came out of an idea on the map:\n\n")
		b.WriteString(next.FromText)
	}
	return b.String()
}
