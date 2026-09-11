package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dev.jevido/work/internal/config"
	"dev.jevido/work/internal/ops"
)

// ErrNoTransport is returned by every workspace call when no ops client has
// been wired in. It is not a failure state: a build with no transport is Work
// exactly as it has always been, and the UI uses it to hide the workspace
// controls rather than to show an error.
var ErrNoTransport = errors.New("workbench: no workspace transport")

// Sync state, as reported to the frontend.
const (
	// SyncOffline means the last attempt to reach the server failed. Work
	// carries on; ops pile up in the outbox.
	SyncOffline = "offline"
	// SyncSyncing means a push or pull is in flight.
	SyncSyncing = "syncing"
	// SyncOnline means the server has everything this machine has produced
	// and this machine has everything the server holds.
	SyncOnline = "online"
	// SyncRejected means the server refused something in a way retrying
	// cannot fix -- a revoked key, an op it will never accept. The outbox is
	// kept and the loop stops asking; see APIError.Fatal.
	SyncRejected = "rejected"
)

// Sync intervals.
//
// Polling is not a choice: the server is explicit that there is no websocket
// and no stream. Three seconds is the trade -- one indexed query per interval
// against a board a person changes a few times a minute. The backoff is what
// stops a laptop with no network from talking to itself in a tight loop.
const (
	syncPoll       = 3 * time.Second
	syncBackoffMin = 5 * time.Second
	syncBackoffMax = 2 * time.Minute
	syncTimeout    = 30 * time.Second

	// maxPushBytes caps a push body under the server's 1 MiB limit, with
	// room for the JSON envelope around the ops.
	//
	// Without it, 250 ops carrying long notes could exceed the limit, and a
	// 413 is one of the failures retrying cannot fix -- so the outbox would
	// stop draining and stay stopped. Counting bytes as the batch is built
	// means that never happens.
	maxPushBytes = 768 << 10
	// maxOpBytes is the largest single op this will queue.
	//
	// It is the same guarantee from the other side: an op too big to send is
	// refused before it reaches the outbox, so the outbox can never hold
	// something that will be rejected forever.
	maxOpBytes = 256 << 10

	// pullBatch is the server's maximum. Paging is driven by the response's
	// "more" flag rather than by a short page, which the server is allowed
	// to return.
	pullBatch = maxPullOps
)

// cursorFile records how far this machine has read the server's log.
const cursorFile = "cursor"

// Document is the merged workspace as the frontend sees it.
//
// Detached is not an error case to hide. It is what an eventually consistent
// tree looks like while it converges -- and, after concurrent moves, sometimes
// once it has. A viewer that drops those nodes is showing an incomplete
// workspace, so they travel separately and get shown.
type Document struct {
	Tree     []ops.TreeNode `json:"tree"`
	Detached []ops.Node     `json:"detached"`
	// Cursor is the highest sequence merged here.
	Cursor int64 `json:"cursor"`
}

// Sync is the second stage.
//
// The first stage already happened: the board moved, locally, immediately,
// network or no network. This turns that into ops, merges them into the
// workspace document, puts them on disk, and gets them to the server when
// there is one to reach -- then brings everyone else's back.
//
// It owns no workbench state and takes no workbench locks, which is what lets
// apply be called from the board path without a disk write ever being able to
// deadlock a run.
type Sync struct {
	client   OpsClient
	server   string
	writeKey string
	actor    string
	dir      string
	// q is the outbox: what the server has not taken yet. journal is
	// everything merged, which is what a restart rebuilds from. See oplog.go
	// for why those are two files and still one fsync.
	q       *queue
	journal *journal
	emit    Emitter

	// onRemote runs after ops from other machines merge, so the workbench can
	// pick up anything it has to act on -- a tab a peer created -- before the
	// frontend is told.
	onRemote func(*Sync)

	// mu guards state and issued. ops.State is explicitly not safe for
	// concurrent use, and it is written by the sync loop and read by whichever
	// goroutine the UI called in on.
	mu    sync.Mutex
	state ops.State
	// issued is the highest clock this replica has handed out. It is tracked
	// separately from state.Clock() because a clock is reserved before its op
	// exists: two ops from one actor sharing a clock are indistinguishable to
	// the merge, so which of them won a contested field would be arbitrary.
	issued uint64

	// cursor is the highest server sequence merged here. Sequences are
	// gapless, so this marks a complete history rather than a bookmark.
	cursor atomic.Int64
	// head is the highest sequence the server said it holds. head-cursor is
	// exactly how far behind this machine is.
	head atomic.Int64

	syncState atomic.Pointer[string]
	lastErr   atomic.Pointer[string]

	wake chan struct{}

	stopOnce sync.Once
	// stop is nil until start runs. It is atomic because start is called from
	// the service's startup and close from whichever goroutine a UI call
	// arrived on, and "those two never overlap today" is a property of the
	// current wiring rather than of this type.
	stop     atomic.Pointer[context.CancelFunc]
	finished chan struct{}
}

// newSync prepares a workspace's sync layer, rebuilding the document from
// disk before anything touches the network.
//
// What it rebuilds from is the local log: every op this machine has merged,
// its own and everyone else's. That is the difference between opening Work
// on a plane to the board you left and opening it to nothing.
//
// The unacknowledged tail of that same log is the outbox. Keeping the two as
// one file is what makes them agree by construction -- and it is the reason
// this rebuilds correctly at all, because an outbox on its own loses an op
// the moment the server takes it.
func newSync(client OpsClient, server, writeKey, workspaceID, actor string, emit Emitter) (*Sync, error) {
	if client == nil {
		return nil, ErrNoTransport
	}
	dir, err := config.StateDir(workspaceID)
	if err != nil {
		return nil, err
	}
	q, err := openQueue(dir)
	if err != nil {
		return nil, err
	}
	jrnl, applied, err := openJournal(dir)
	if err != nil {
		q.close()
		return nil, err
	}

	s := &Sync{
		client:   client,
		server:   server,
		writeKey: writeKey,
		actor:    actor,
		dir:      dir,
		q:        q,
		journal:  jrnl,
		emit:     emit,
		wake:     make(chan struct{}, 1),
		finished: make(chan struct{}),
	}
	s.setState(SyncOffline)
	s.cursor.Store(readCursor(filepath.Join(dir, cursorFile)))

	// One unmergeable op must not cost the user everything after it, so a
	// replay failure is reported and the rest is kept: the server will
	// reject a bad op with a reason, which is a better place to find out
	// than here.
	if len(applied) > 0 {
		if _, err := s.state.ApplyAll(applied); err != nil {
			s.setErr(fmt.Errorf("workbench: replay journal: %w", err))
		}
	}
	// The outbox on top, which covers the journal's unsynced tail: local ops
	// are in both, and replaying one twice is free because ops are
	// idempotent by ID.
	if pending := q.head(maxOutbox); len(pending) > 0 {
		if _, err := s.state.ApplyAll(pending); err != nil {
			s.setErr(fmt.Errorf("workbench: replay outbox: %w", err))
		}
	}
	// Every clock this replica has issued is in one of those two, so the
	// highest one seen is the floor for the next.
	s.issued = s.state.Clock()
	return s, nil
}

// start runs the push/pull loop until ctx ends or close is called. Calling it
// twice on one Sync is a no-op after the first.
func (s *Sync) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	if !s.stop.CompareAndSwap(nil, &cancel) {
		cancel()
		return
	}
	go s.loop(ctx)
}

// close ends the loop and releases the outbox. Queued ops stay on disk.
//
// It waits for the loop to finish, which means the caller must not hold any
// lock the loop can want. In practice that is wsMu: the loop calls onRemote,
// onRemote takes wsMu, and closing under it would have each side waiting for
// the other. Callers swap the Sync out under the lock and close it after.
func (s *Sync) close() {
	s.stopOnce.Do(func() {
		if cancel := s.stop.Load(); cancel != nil {
			(*cancel)()
			<-s.finished
		}
		s.q.close()
		s.journal.close()
	})
}

// loop drives sync cycles: at once, whenever ops are produced, and on a timer
// so another peer's work arrives without anything happening here.
func (s *Sync) loop(ctx context.Context) {
	defer close(s.finished)

	backoff := syncBackoffMin
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}

		wait := syncPoll
		switch err := s.cycle(ctx); {
		case err == nil:
			backoff = syncBackoffMin
		case ctx.Err() != nil:
			return
		default:
			after, retry := retryAfter(err)
			if !retry {
				// Waiting does not change the answer. Stop polling and leave
				// the state saying why; SyncNow is how a user retries once
				// they have fixed the key.
				s.setState(SyncRejected)
				s.publish()
				continue
			}
			wait = max(backoff, after)
			backoff = min(backoff*2, syncBackoffMax)
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// nudge asks the loop for a cycle now. It never blocks: a full channel already
// means a cycle is pending, which is the same request.
func (s *Sync) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// apply is stage two for a batch of local edits: merge, persist, publish, and
// ask for a push.
//
// The order is the contract. The ops reach the disk before the document moves
// and before anything is published, so what the user is shown is what will
// survive a crash. Nothing here waits on the network -- reaching the server is
// the loop's job, and an offline machine notices no difference.
func (s *Sync) apply(batch []ops.Op) error {
	if len(batch) == 0 {
		return nil
	}
	// Validate before the disk write rather than after: an op the merge will
	// never accept has no business in a durable outbox, where it would be
	// retried against the server forever.
	for _, op := range batch {
		if err := op.Validate(); err != nil {
			return fmt.Errorf("workbench: invalid op %s: %w", op.ID, err)
		}
		// Size is checked here too. Validate covers the server's limits on
		// identifiers and field counts, but not on how much JSON a field
		// value can be, and an op the server will always answer with 413 is
		// one the outbox must never accept: it would be retried forever and
		// block everything behind it.
		encoded, err := json.Marshal(op)
		if err != nil {
			return fmt.Errorf("workbench: encode op %s: %w", op.ID, err)
		}
		if len(encoded) > maxOpBytes {
			return fmt.Errorf("workbench: op %s is %d bytes, over the %d the server accepts", op.ID, len(encoded), maxOpBytes)
		}
	}
	// Durable before visible. An op the user was told happened, that a
	// restart cannot find, is the one outcome this layer exists to prevent.
	//
	// The journal is written without a sync and the outbox with one, which
	// is what makes this a single fsync: both hold these ops, the synced one
	// is replayed over the other at startup, so the journal losing its tail
	// to a power cut costs nothing.
	if err := s.journal.append(false, batch...); err != nil {
		s.setErr(err)
		s.publish()
		return err
	}
	if err := s.q.append(batch...); err != nil {
		s.setErr(err)
		s.publish()
		return err
	}

	s.mu.Lock()
	_, err := s.state.ApplyAll(batch)
	s.mu.Unlock()
	if err != nil {
		s.setErr(err)
		s.publish()
		return err
	}

	s.publish()
	s.nudge()
	return nil
}

// reserveClocks hands out n consecutive Lamport clock values.
//
// A replica must issue every op with a clock strictly greater than any it has
// seen or issued. Reserving a range rather than reading the clock n times is
// not an optimisation: two ops from one actor sharing a clock are
// indistinguishable to the merge, so a batch that writes the same field twice
// has to be ordered, and this is what orders it.
func (s *Sync) reserveClocks(n int) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	first := max(s.state.Clock(), s.issued) + 1
	s.issued = first + uint64(n) - 1
	return first
}

// document is the merged workspace, for the frontend.
func (s *Sync) document() Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Document{
		Tree:     s.state.Tree(),
		Detached: s.state.Detached(),
		Cursor:   s.cursor.Load(),
	}
}

// node reads one node out of the merged document.
func (s *Sync) node(id string) (ops.Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Node(id)
}

// liveNodes returns every node that has not been tombstoned.
func (s *Sync) liveNodes() []ops.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ops.Node
	for n := range s.state.All() {
		if n.Deleted {
			continue
		}
		out = append(out, n)
	}
	return out
}

// pendingNode reports whether the outbox still holds an op for this node.
func (s *Sync) pendingNode(node string) bool { return s.q.touches(node) }

// cycle is one push-then-pull against the server.
//
// Push comes first so this machine's work is in the shared log before it asks
// what everyone else did. A peer polling next then sees a consistent picture
// rather than a reply to something it has not been told about.
func (s *Sync) cycle(ctx context.Context) error {
	if pending, _ := s.q.depth(); pending > 0 {
		s.setState(SyncSyncing)
		s.publish()
	}

	if err := s.push(ctx); err != nil {
		s.fail(err)
		return err
	}
	if err := s.pull(ctx); err != nil {
		s.fail(err)
		return err
	}

	s.setState(SyncOnline)
	s.setErr(nil)
	s.publish()
	return nil
}

// push drains the outbox in batches, acknowledging each before the next so an
// interrupted drain does not start over.
func (s *Sync) push(ctx context.Context) error {
	for {
		batch := fitBatch(s.q.head(pushBatch), maxPushBytes)
		if len(batch) == 0 {
			return nil
		}

		callCtx, cancel := context.WithTimeout(ctx, syncTimeout)
		res, err := s.client.Push(callCtx, s.server, s.writeKey, batch)
		cancel()
		if err != nil {
			return fmt.Errorf("workbench: push %d ops: %w", len(batch), err)
		}
		s.bumpHead(res.Head)

		// Accepted and duplicates are both done with. A duplicate means an
		// earlier attempt landed and this client lost the response, which is
		// exactly the case op IDs exist to make survivable.
		ids := make([]string, 0, len(res.Accepted)+len(res.Duplicates))
		for _, a := range res.Accepted {
			ids = append(ids, a.ID)
		}
		for _, d := range res.Duplicates {
			ids = append(ids, d.ID)
		}
		if len(ids) == 0 {
			// The server took the request and claimed none of the ops.
			// Dropping the batch loses work and retrying it spins, so stop
			// and say so rather than pick one.
			return fmt.Errorf("workbench: server acknowledged none of %d pushed ops", len(batch))
		}
		if err := s.q.ack(ids); err != nil {
			return err
		}
	}
}

// fitBatch trims a batch to what fits in one request body.
//
// At least one op always survives, whatever it measures: apply refuses
// anything over maxOpBytes, so the head of the outbox is always sendable, and
// returning nothing here would be a drain that never moves.
func fitBatch(batch []ops.Op, limit int) []ops.Op {
	total := 0
	for i, op := range batch {
		encoded, err := json.Marshal(op)
		if err != nil {
			// It got into the outbox, so it encoded once already. Send what
			// is definitely fine and let the next cycle deal with this.
			return batch[:max(i, 1)]
		}
		total += len(encoded) + 1
		if total > limit && i > 0 {
			return batch[:i]
		}
	}
	return batch
}

// pull reads the log from the cursor and merges it.
//
// Ops this machine wrote come back too -- the log is everyone's -- and they
// merge like any other. That costs nothing: ops are idempotent by ID, so
// re-merging one this state already has changes nothing.
func (s *Sync) pull(ctx context.Context) error {
	for {
		since := s.cursor.Load()

		callCtx, cancel := context.WithTimeout(ctx, syncTimeout)
		res, err := s.client.Pull(callCtx, s.server, s.writeKey, since, pullBatch)
		cancel()
		if err != nil {
			return fmt.Errorf("workbench: pull since %d: %w", since, err)
		}
		s.bumpHead(res.Head)

		if len(res.Ops) == 0 {
			if since < res.Head {
				// The server holds ops this client cannot see. Paging will
				// not fix that, and pretending to be caught up would be a
				// lie the indicator repeats.
				return fmt.Errorf("workbench: server is at %d but returned nothing after %d", res.Head, since)
			}
			return nil
		}

		batch := make([]ops.Op, 0, len(res.Ops))
		var last int64
		var foreign bool
		for _, entry := range res.Ops {
			if entry.Seq > last {
				last = entry.Seq
			}
			if entry.Op.Actor != s.actor {
				foreign = true
			}
			batch = append(batch, entry.Op)
		}

		// Journalled before merged, and merged before the cursor moves. A
		// crash anywhere in that order replays the page, which is free.
		//
		// This one is synced, unlike a local apply: nothing else on this
		// machine holds a pulled page, and the cursor is about to advance
		// past it. It is also not put in the outbox -- these ops came from
		// the server, so pushing them back is the one thing they do not
		// need.
		if err := s.journal.append(true, batch...); err != nil {
			return err
		}

		s.mu.Lock()
		_, applyErr := s.state.ApplyAll(batch)
		s.mu.Unlock()
		if applyErr != nil {
			// The log outlives any one version of this binary, so an op a
			// newer release wrote can arrive here. Refusing to advance the
			// cursor is right: skipping it would diverge from every other
			// replica silently, and there is no correct guess to make.
			return fmt.Errorf("workbench: merge log at %d: %w", last, applyErr)
		}

		// The cursor moves only after the merge, so a crash between the two
		// replays the page instead of skipping it. Re-merging is free.
		if last > since {
			s.cursor.Store(last)
			if err := writeCursor(filepath.Join(s.dir, cursorFile), last); err != nil {
				return err
			}
		}

		if foreign && s.onRemote != nil {
			s.onRemote(s)
		}
		s.publish()

		// A journal that has outgrown its bound is discarded and the cursor
		// rewound so the next pull rebuilds it. Expensive, and the
		// alternative is a file that grows for the life of the workspace.
		// The outbox is untouched: nothing else has those ops.
		if s.journal.full() {
			if err := s.compact(); err != nil {
				return err
			}
			return nil
		}

		if !res.More {
			return nil
		}
	}
}

// compact discards the journal and rewinds to the start of the server's log.
//
// The two have to move together. A cursor kept across a discarded journal
// would mean this machine never asks for the history it just deleted, and it
// would be missing part of the workspace with nothing to say so.
func (s *Sync) compact() error {
	if err := s.journal.reset(); err != nil {
		return err
	}
	s.cursor.Store(0)
	return writeCursor(filepath.Join(s.dir, cursorFile), 0)
}

// bumpHead records the server's head, which only ever grows.
func (s *Sync) bumpHead(head int64) {
	for {
		cur := s.head.Load()
		if head <= cur || s.head.CompareAndSwap(cur, head) {
			return
		}
	}
}

// fail records a sync failure and tells the UI.
func (s *Sync) fail(err error) {
	s.setState(SyncOffline)
	s.setErr(err)
	s.publish()
}

func (s *Sync) setState(v string) { s.syncState.Store(&v) }

func (s *Sync) setErr(err error) {
	if err == nil {
		s.lastErr.Store(nil)
		return
	}
	msg := err.Error()
	s.lastErr.Store(&msg)
}

// Status is what the UI needs to draw the workspace indicator.
type Status struct {
	// Joined is false when no workspace has been joined, in which case every
	// other field is zero and Work behaves exactly as it always has.
	Joined bool `json:"joined"`
	// State is one of SyncOffline, SyncSyncing, SyncOnline, SyncRejected.
	State string `json:"state,omitempty"`
	// Pending is how many ops have not reached the server.
	Pending int `json:"pending"`
	// Dropped is how many ops were discarded because the outbox hit its cap.
	// Non-zero means this machine's history has a hole in it.
	Dropped int `json:"dropped,omitempty"`
	// Cursor is the highest sequence merged here and Head the highest the
	// server holds. Sequences are gapless, so Head-Cursor is exactly how many
	// ops behind this machine is -- not an estimate.
	Cursor int64 `json:"cursor"`
	Head   int64 `json:"head"`
	// Error is the last sync failure, or empty. Being offline is a normal
	// condition rather than an alarm; the text explains it, nothing more.
	Error string `json:"error,omitempty"`
}

// status is the current sync state.
func (s *Sync) status() Status {
	pending, dropped := s.q.depth()
	state := SyncOffline
	if v := s.syncState.Load(); v != nil {
		state = *v
	}
	var msg string
	if v := s.lastErr.Load(); v != nil {
		msg = *v
	}
	return Status{
		Joined:  true,
		State:   state,
		Pending: pending,
		Dropped: dropped,
		Cursor:  s.cursor.Load(),
		Head:    max(s.head.Load(), s.cursor.Load()),
		Error:   msg,
	}
}

// publish tells the frontend where sync stands.
//
// The document is deliberately not on this event. Sync state changes on every
// poll; the document changes when someone edits something. Putting a whole
// merged tree on a payload that fires every three seconds would mean
// re-encoding the workspace for the overwhelmingly common answer of "nothing
// happened". The frontend asks for the document when the cursor moves.
func (s *Sync) publish() {
	s.emit(EventWorkspaceSync, SyncEvent{Status: s.status()})
}

// readCursor reads the last merged sequence. Anything unreadable is zero,
// which replays the log from the start -- slower, and correct, because ops are
// idempotent by ID.
func readCursor(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// writeCursor persists the last merged sequence, atomically.
func writeCursor(path string, seq int64) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "cursor-*")
	if err != nil {
		return fmt.Errorf("workbench: create temp cursor in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("workbench: protect %s: %w", tmpName, err)
	}
	if _, err := tmp.WriteString(strconv.FormatInt(seq, 10)); err != nil {
		tmp.Close()
		return fmt.Errorf("workbench: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("workbench: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("workbench: replace %s: %w", path, err)
	}
	return nil
}
