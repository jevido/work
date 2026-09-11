package workbench

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"dev.jevido/work/internal/ops"
)

// The local state directory holds two op files and a cursor.
//
// outboxFile is what this machine has produced and the server has not taken.
// journalFile is everything this machine has merged, its own and everyone
// else's, which is what a restart rebuilds the workspace from.
//
// They are separate because they are genuinely different streams -- the
// outbox shrinks as the server acknowledges, the journal only grows -- and
// trying to serve both from one file breaks the moment a pull interleaves
// with unpushed local work: "acknowledged" stops being a prefix and there is
// nothing cheap left to track it with.
//
// What they are not is two fsyncs. See journal.append.
const (
	outboxFile  = "pending.jsonl"
	journalFile = "applied.jsonl"
)

// maxOutbox bounds the outbox so a machine left offline for a week cannot
// fill a disk. Past this the oldest ops are dropped and the count says so:
// this machine's history then has a hole in it that nothing can fill,
// because those ops never reached anyone else.
const maxOutbox = 50_000

// maxJournal bounds the journal.
//
// Past it the journal is discarded and the cursor reset, so the next sync
// rebuilds from the server's copy. That is a full re-download, which is why
// the bound is high: a recovery path for a workspace with years of history,
// not something a session should reach. It is safe in a way dropping from
// the outbox is not -- everything in the journal is, by definition, already
// somewhere else.
const maxJournal = 200_000

// pushBatch is how many ops go to the server in one request.
//
// Under the server's documented 500 so a batch of large ops stays inside the
// 1 MiB body limit as well as the count limit, and it bounds the request
// rather than the outbox: a reconnect after a long offline spell drains in
// several requests instead of one that times out halfway and starts again.
const pushBatch = 250

// readOps loads a JSONL op file. A missing file is an empty one.
//
// A line that will not parse is skipped rather than fatal. The alternative is
// refusing to sync at all because of one corrupt tail record, which is the
// worse trade: the point of these files is not to lose work, and losing one
// op is not a reason to lose the rest. A half-written line at the end is
// exactly what an unsynced journal looks like after a power cut, and it is
// expected rather than exceptional.
func readOps(path string) ([]ops.Op, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("workbench: read %s: %w", path, err)
	}
	defer f.Close()

	var out []ops.Op
	sc := bufio.NewScanner(f)
	// An op carries a node's fields, which are small, but the default 64 KiB
	// line limit is close enough to a card with a long note to be worth
	// raising deliberately rather than discovering.
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var op ops.Op
		if err := json.Unmarshal(line, &op); err != nil {
			continue
		}
		out = append(out, op)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("workbench: read %s: %w", path, err)
	}
	return out, nil
}

// encodeOps renders ops as JSONL.
func encodeOps(batch []ops.Op) ([]byte, error) {
	var buf []byte
	for _, op := range batch {
		line, err := json.Marshal(op)
		if err != nil {
			return nil, fmt.Errorf("workbench: encode op %s: %w", op.ID, err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return buf, nil
}

// queue is the outbox: what this machine has produced and the server has not
// acknowledged. Every op is on disk before the workbench claims it happened,
// and it stays there until the server has taken it.
//
// The whole outbox is held in memory too. That is not a cache -- it is what
// makes the common operations cheap. Appending is one write and one sync;
// asking how far behind we are is a length; taking a batch to push is a copy
// of the head. Only acknowledgement touches the file again, and it does so
// once per sync cycle rather than once per op, which keeps draining a large
// backlog linear instead of quadratic.
//
// A queue is safe for concurrent use.
type queue struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	pending []ops.Op
	// dropped counts ops discarded to stay under maxOutbox. Non-zero means
	// work this machine did will never reach the workspace.
	dropped int
}

// openQueue opens (or creates) the outbox in dir, reading back anything a
// previous run left behind.
func openQueue(dir string) (*queue, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("workbench: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, outboxFile)

	pending, err := readOps(path)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("workbench: open %s: %w", path, err)
	}
	return &queue{path: path, f: f, pending: pending}, nil
}

// append writes ops to the outbox and returns once they are on the platter.
//
// This is the one fsync on the board path, and it is not optional: an op that
// is only in the page cache is an op a power cut turns into work the user did
// and the workspace never hears about. It costs about 7ms on btrfs, and it is
// paid a handful of times per run rather than per token, because streamed
// output never becomes an op.
func (q *queue) append(batch ...ops.Op) error {
	if len(batch) == 0 {
		return nil
	}
	buf, err := encodeOps(batch)
	if err != nil {
		return err
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if _, err := q.f.Write(buf); err != nil {
		return fmt.Errorf("workbench: write %s: %w", q.path, err)
	}
	if err := q.f.Sync(); err != nil {
		return fmt.Errorf("workbench: sync %s: %w", q.path, err)
	}
	q.pending = append(q.pending, batch...)

	if over := len(q.pending) - maxOutbox; over > 0 {
		q.pending = append(q.pending[:0], q.pending[over:]...)
		q.dropped += over
		// The file now disagrees with memory, so rewrite it. This is the one
		// place append is not O(1), and it only happens once the outbox has
		// already gone badly wrong.
		return q.rewriteLocked()
	}
	return nil
}

// head returns a copy of the oldest n ops, without removing them. They are
// removed by ack, and only once the server has them.
func (q *queue) head(n int) []ops.Op {
	q.mu.Lock()
	defer q.mu.Unlock()
	if n > len(q.pending) {
		n = len(q.pending)
	}
	if n == 0 {
		return nil
	}
	out := make([]ops.Op, n)
	copy(out, q.pending)
	return out
}

// ack removes acknowledged ops and rewrites the outbox.
//
// It takes IDs rather than a count because appends race with the push being
// acknowledged: by the time the server answers, the head of the outbox may
// not be the ops that were sent. Filtering by ID is correct under that race;
// "drop the first n" is not.
func (q *queue) ack(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	done := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		done[id] = struct{}{}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	kept := q.pending[:0]
	for _, op := range q.pending {
		if _, ok := done[op.ID]; ok {
			continue
		}
		kept = append(kept, op)
	}
	q.pending = kept
	return q.rewriteLocked()
}

// rewriteLocked writes the in-memory outbox over the file. Caller holds mu.
//
// The write goes to a temporary file and is renamed into place, so a crash
// during compaction leaves the old complete outbox rather than half a new one.
func (q *queue) rewriteLocked() error {
	f, err := rewrite(q.path, q.pending)
	if err != nil {
		return err
	}
	old := q.f
	q.f = f
	if old != nil {
		old.Close()
	}
	return nil
}

// depth is how many ops have not reached the server, and how many were lost
// to maxOutbox along the way.
func (q *queue) depth() (pending, dropped int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending), q.dropped
}

// touches reports whether any unpushed op targets this node.
//
// It answers one question: "did this machine make that node while offline, or
// has a peer deleted it?" Absent from the document and present here means the
// first. A linear scan of what is normally empty, run only when the tab list
// disagrees with the document.
func (q *queue) touches(node string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, op := range q.pending {
		if op.Node == node {
			return true
		}
	}
	return false
}

// close releases the outbox. The ops stay on disk for the next run.
func (q *queue) close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.f == nil {
		return nil
	}
	err := q.f.Close()
	q.f = nil
	return err
}

// journal is every op this machine has merged, and it is what a restart
// rebuilds the workspace from.
//
// The outbox cannot do that job: an op leaves it the moment the server
// acknowledges it, so a machine that had pushed everything would come up to
// an empty board and stay there until a pull succeeded -- which, on a laptop
// that was closed on a plane, is not soon.
//
// A journal is safe for concurrent use.
type journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	n    int
}

// openJournal opens the journal in dir and returns everything already in it,
// oldest first, for the caller to replay.
func openJournal(dir string) (*journal, []ops.Op, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("workbench: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, journalFile)

	applied, err := readOps(path)
	if err != nil {
		return nil, nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("workbench: open %s: %w", path, err)
	}
	return &journal{path: path, f: f, n: len(applied)}, applied, nil
}

// append records merged ops, syncing only when the caller has nothing else
// that can recover them.
//
// Local work does not need the sync, and that is what keeps a board change to
// one fsync rather than two: the same ops are in the outbox, which is
// synced, and startup replays the outbox over the journal. A journal that
// loses its tail to a power cut loses nothing that is not about to be
// replayed from beside it.
//
// A pulled page is the opposite: nothing else on this machine has it, and the
// cursor is about to advance past it. Losing that tail would mean never
// asking for those ops again and quietly diverging from every other replica,
// so it is synced before the cursor moves.
func (j *journal) append(fsync bool, batch ...ops.Op) error {
	if len(batch) == 0 {
		return nil
	}
	buf, err := encodeOps(batch)
	if err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if _, err := j.f.Write(buf); err != nil {
		return fmt.Errorf("workbench: write %s: %w", j.path, err)
	}
	if fsync {
		if err := j.f.Sync(); err != nil {
			return fmt.Errorf("workbench: sync %s: %w", j.path, err)
		}
	}
	j.n += len(batch)
	return nil
}

// full reports whether the journal has outgrown maxJournal.
func (j *journal) full() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.n > maxJournal
}

// reset empties the journal. The caller must reset the cursor as well:
// without the journal, the only complete copy of the history is the server's.
func (j *journal) reset() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	f, err := rewrite(j.path, nil)
	if err != nil {
		return err
	}
	old := j.f
	j.f = f
	j.n = 0
	if old != nil {
		old.Close()
	}
	return nil
}

// close releases the journal. What is in it stays on disk for the next run.
func (j *journal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// rewrite replaces path with batch and returns a fresh append handle.
//
// The write goes to a temporary file and is renamed over the target, so a
// crash mid-rewrite leaves the old complete file rather than half a new one.
// The handle is returned because the caller's old one still points at the
// replaced file, where appends would go somewhere nothing reads.
func rewrite(path string, batch []ops.Op) (*os.File, error) {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "ops-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("workbench: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // No-op once the rename has succeeded.

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("workbench: protect %s: %w", tmpName, err)
	}

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	for _, op := range batch {
		if err := enc.Encode(op); err != nil {
			tmp.Close()
			return nil, fmt.Errorf("workbench: encode op %s: %w", op.ID, err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("workbench: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("workbench: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("workbench: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, fmt.Errorf("workbench: replace %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("workbench: reopen %s: %w", path, err)
	}
	return f, nil
}
