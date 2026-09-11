package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/ops"
)

// stateHome points config.StateDir at a temporary directory, so a test never
// touches the developer's own queue.
func stateHome(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// fakeOps is an OpsClient that records what it was asked to do and can be
// switched offline. Everything interesting about sync -- the outbox, the
// replay, the cursor -- is above the transport, so a fake is enough to test
// all of it and nothing here needs a server.
type fakeOps struct {
	mu sync.Mutex
	// down makes every call fail, standing in for no network.
	down bool
	// log is the workspace's shared log, in arrival order.
	log []ops.Op
	// seen is every op ID the log holds, so duplicates behave as the server's
	// contract says.
	seen map[string]bool

	pushes int
}

func newFakeOps() *fakeOps { return &fakeOps{seen: map[string]bool{}} }

var errOffline = errors.New("fake: offline")

func (f *fakeOps) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeOps) Create(context.Context, string, string, string) (Created, error) {
	return Created{}, errOffline
}

func (f *fakeOps) Meta(_ context.Context, _, key string) (Meta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return Meta{}, errOffline
	}
	return Meta{Workspace: Workspace{ID: "ws_test", Name: "test"}, Access: "write"}, nil
}

func (f *fakeOps) Push(_ context.Context, _, _ string, batch []ops.Op) (PushResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return PushResult{}, errOffline
	}
	f.pushes++

	var res PushResult
	for _, op := range batch {
		if f.seen[op.ID] {
			res.Duplicates = append(res.Duplicates, Ack{ID: op.ID})
			continue
		}
		f.seen[op.ID] = true
		f.log = append(f.log, op)
		res.Accepted = append(res.Accepted, Ack{ID: op.ID, Seq: int64(len(f.log))})
	}
	res.Head = int64(len(f.log))
	return res, nil
}

func (f *fakeOps) Pull(_ context.Context, _, _ string, since int64, limit int) (PullResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return PullResult{}, errOffline
	}

	res := PullResult{Head: int64(len(f.log))}
	for i := int(since); i < len(f.log) && len(res.Ops) < limit; i++ {
		res.Ops = append(res.Ops, Logged{Seq: int64(i + 1), Op: f.log[i]})
	}
	res.More = since+int64(len(res.Ops)) < res.Head
	return res, nil
}

func (f *fakeOps) entries() []ops.Op {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ops.Op(nil), f.log...)
}

// newTestSync builds a Sync against a fake, with events discarded.
//
// The workspace ID names the state directory, so two replicas in one test
// take different ones -- otherwise they would share an outbox and a cursor,
// which is not what two machines do.
func newTestSync(t *testing.T, client OpsClient, actor string) *Sync {
	t.Helper()
	s, err := newSync(client, "https://example.invalid", "wk_test", "ws_"+actor, actor, func(string, any) {})
	if err != nil {
		t.Fatalf("newSync: %v", err)
	}
	t.Cleanup(s.close)
	return s
}

func TestQueueSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	q, err := openQueue(dir)
	if err != nil {
		t.Fatalf("openQueue: %v", err)
	}
	want := []ops.Op{
		{ID: "a", Kind: ops.KindCreateNode, Actor: "x", Clock: 1, Node: "n1"},
		{ID: "b", Kind: ops.KindDeleteNode, Actor: "x", Clock: 2, Node: "n1"},
	}
	if err := q.append(want...); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := q.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := openQueue(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.close() })

	got := reopened.head(10)
	if len(got) != len(want) {
		t.Fatalf("after restart got %d ops, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Node != want[i].Node {
			t.Errorf("op %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestQueueAckCompactsAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	q, err := openQueue(dir)
	if err != nil {
		t.Fatalf("openQueue: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := q.append(ops.Op{ID: id, Kind: ops.KindDeleteNode, Actor: "x", Clock: 1, Node: "n"}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	if err := q.ack([]string{"a", "c"}); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// A later append has to land in the file the rewrite created, not the one
	// it replaced -- which is what the reopen below actually checks.
	if err := q.append(ops.Op{ID: "d", Kind: ops.KindDeleteNode, Actor: "x", Clock: 1, Node: "n"}); err != nil {
		t.Fatalf("append after ack: %v", err)
	}
	q.close()

	reopened, err := openQueue(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.close() })

	var ids []string
	for _, op := range reopened.head(10) {
		ids = append(ids, op.ID)
	}
	if len(ids) != 2 || ids[0] != "b" || ids[1] != "d" {
		t.Fatalf("after ack and restart queue holds %v, want [b d]", ids)
	}
}

func TestOfflineWorkQueuesAndReplaysOnReconnect(t *testing.T) {
	stateHome(t)

	client := newFakeOps()
	client.setDown(true)
	s := newTestSync(t, client, "actor-a")

	cards := []board.Card{
		{ID: "T1", RunID: "r1", AgentID: "anton", Title: "Ship it", Status: board.StatusTodo},
	}
	batch, err := boardOps(s, "tab1", cards)
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := s.apply(batch); err != nil {
		t.Fatalf("apply while offline: %v", err)
	}

	// Stage one happened: the document has the card even though nothing has
	// reached a server.
	if _, ok := s.node(cardNode("actor-a", "T1")); !ok {
		t.Fatal("card is not in the local document while offline")
	}
	pending, _ := s.q.depth()
	if pending != len(batch) {
		t.Fatalf("outbox holds %d ops, want %d", pending, len(batch))
	}
	if got := len(client.entries()); got != 0 {
		t.Fatalf("server received %d ops while offline", got)
	}

	// Stage two, once there is a network.
	client.setDown(false)
	if err := s.cycle(context.Background()); err != nil {
		t.Fatalf("cycle after reconnect: %v", err)
	}

	if pending, _ := s.q.depth(); pending != 0 {
		t.Fatalf("outbox still holds %d ops after a successful cycle", pending)
	}
	if got := len(client.entries()); got != len(batch) {
		t.Fatalf("server received %d ops, want %d", got, len(batch))
	}
	if st := s.status(); st.State != SyncOnline {
		t.Fatalf("state = %q, want %q", st.State, SyncOnline)
	}
}

func TestOutboxReplaysIntoDocumentOnRestart(t *testing.T) {
	stateHome(t)

	client := newFakeOps()
	client.setDown(true)

	first := newTestSync(t, client, "actor-a")
	batch, err := boardOps(first, "tab1", []board.Card{
		{ID: "T1", Title: "Ship it", Status: board.StatusTodo},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := first.apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	first.close()

	// A restart before the network came back must not show an empty board.
	second := newTestSync(t, client, "actor-a")
	if _, ok := second.node(cardNode("actor-a", "T1")); !ok {
		t.Fatal("card is missing from the document after a restart with an unpushed outbox")
	}
	if pending, _ := second.q.depth(); pending != len(batch) {
		t.Fatalf("outbox holds %d ops after restart, want %d", pending, len(batch))
	}
}

func TestAckedWorkSurvivesARestartWithNoNetwork(t *testing.T) {
	stateHome(t)

	client := newFakeOps()

	// Everything gets pushed, so the outbox ends up empty -- which is the
	// case the outbox alone cannot rebuild from.
	first := newTestSync(t, client, "actor-a")
	batch, err := boardOps(first, "tab1", []board.Card{
		{ID: "T1", Title: "Pushed and acked", Status: board.StatusDoing},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := first.apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := first.cycle(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if pending, _ := first.q.depth(); pending != 0 {
		t.Fatalf("outbox holds %d ops after a clean push, want 0", pending)
	}
	first.close()

	// Restart with the network gone. Nothing can be fetched, so whatever the
	// board shows has to have come off the disk.
	client.setDown(true)
	second := newTestSync(t, client, "actor-a")

	cards := cardsFromDocument(second.document(), "tab1")
	if len(cards) != 1 || cards[0].Title != "Pushed and acked" {
		t.Fatalf("after an offline restart the board is %+v, want the card that was already pushed", cards)
	}
	if cards[0].Status != board.StatusDoing {
		t.Errorf("status = %q, want %q", cards[0].Status, board.StatusDoing)
	}
}

func TestReplayDoesNotReuseAClockItAlreadyIssued(t *testing.T) {
	stateHome(t)

	client := newFakeOps()
	client.setDown(true)

	first := newTestSync(t, client, "actor-a")
	batch, err := boardOps(first, "tab1", []board.Card{{ID: "T1", Title: "One", Status: board.StatusTodo}})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := first.apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var highest uint64
	for _, op := range batch {
		highest = max(highest, op.Clock)
	}
	first.close()

	// A restart must not hand out a clock it used before the restart: two
	// ops from one actor sharing a clock are indistinguishable to the merge.
	second := newTestSync(t, client, "actor-a")
	next, err := boardOps(second, "tab1", []board.Card{
		{ID: "T1", Title: "One", Status: board.StatusDoing},
	})
	if err != nil {
		t.Fatalf("boardOps after restart: %v", err)
	}
	if len(next) == 0 {
		t.Fatal("the status change produced no ops, so the document did not survive the restart")
	}
	for _, op := range next {
		if op.Clock <= highest {
			t.Fatalf("op after restart has clock %d, not above the %d already issued", op.Clock, highest)
		}
	}
}

func TestBoardDiffOnlyWritesWhatChanged(t *testing.T) {
	stateHome(t)

	s := newTestSync(t, newFakeOps(), "actor-a")

	cards := []board.Card{{ID: "T1", Title: "Ship it", Status: board.StatusTodo}}
	first, err := boardOps(s, "tab1", cards)
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	// The tab node and the card node.
	if len(first) != 2 {
		t.Fatalf("first publish produced %d ops, want 2", len(first))
	}
	if err := s.apply(first); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Publishing an unchanged board must cost nothing at all.
	again, err := boardOps(s, "tab1", cards)
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("unchanged board produced %d ops, want 0", len(again))
	}

	// A status change is one op with one field, not a whole board.
	cards[0].Status = board.StatusDoing
	changed, err := boardOps(s, "tab1", cards)
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("status change produced %d ops, want 1", len(changed))
	}
	if changed[0].Kind != ops.KindSetFields {
		t.Fatalf("kind = %q, want %q", changed[0].Kind, ops.KindSetFields)
	}
	if len(changed[0].Fields) != 1 {
		t.Fatalf("op carries %d fields, want 1: %v", len(changed[0].Fields), changed[0].Fields)
	}
	if _, ok := changed[0].Fields[FieldStatus]; !ok {
		t.Fatalf("op does not carry %s: %v", FieldStatus, changed[0].Fields)
	}
}

func TestBoardOpsAreValidAndOrdered(t *testing.T) {
	stateHome(t)

	s := newTestSync(t, newFakeOps(), "actor-a")
	batch, err := boardOps(s, "tab1", []board.Card{
		{ID: "T1", Title: "One", Status: board.StatusTodo},
		{ID: "T2", Title: "Two", Status: board.StatusDoing},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}

	seen := map[string]bool{}
	var last uint64
	for i, op := range batch {
		if err := op.Validate(); err != nil {
			t.Errorf("op %d is invalid: %v", i, err)
		}
		if seen[op.ID] {
			t.Errorf("op %d reuses id %s", i, op.ID)
		}
		seen[op.ID] = true
		// Two ops from one actor sharing a clock are indistinguishable to the
		// merge, so a batch has to be strictly increasing.
		if op.Clock <= last {
			t.Errorf("op %d has clock %d, not above %d", i, op.Clock, last)
		}
		last = op.Clock
	}
}

func TestNoWorkspaceLeavesTheBoardPathUntouched(t *testing.T) {
	var events []string
	w := New(agents.Default(), claude.NewRunner(""), func(name string, _ any) {
		events = append(events, name)
	}, t.TempDir())

	// No workspace joined: publishing the board must emit exactly what it
	// always did and reach no sync layer at all.
	w.board.Add("r1", "anton", "Ship it")
	w.publishBoard()

	if len(events) != 1 || events[0] != EventBoardUpdated {
		t.Fatalf("events = %v, want exactly [%s]", events, EventBoardUpdated)
	}
	if w.sync.Load() != nil {
		t.Fatal("a workbench with no workspace has a sync layer")
	}
	if w.WorkDir() != w.baseDir {
		t.Fatalf("workDir = %q, want the startup folder %q", w.WorkDir(), w.baseDir)
	}
}

func TestCursorRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor")

	if got := readCursor(path); got != 0 {
		t.Fatalf("missing cursor reads as %d, want 0", got)
	}
	if err := writeCursor(path, 4127); err != nil {
		t.Fatalf("writeCursor: %v", err)
	}
	if got := readCursor(path); got != 4127 {
		t.Fatalf("cursor = %d, want 4127", got)
	}

	// A cursor is a bookmark, not a secret, but it lives beside the outbox in
	// a directory that is 0700 -- the file mode is set for the same reason.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cursor mode = %o, want 600", perm)
	}
}

func TestPullMergesAnotherMachinesWork(t *testing.T) {
	stateHome(t)

	client := newFakeOps()

	// A colleague's replica pushes a tab and a card.
	them := newTestSync(t, client, "actor-b")
	theirs, err := boardOps(them, "tab1", []board.Card{
		{ID: "T1", Title: "Their card", Status: board.StatusDoing},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := them.apply(theirs); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := them.cycle(context.Background()); err != nil {
		t.Fatalf("their cycle: %v", err)
	}

	// This machine pulls and sees it, in a tab it never created.
	me := newTestSync(t, client, "actor-a")
	if err := me.cycle(context.Background()); err != nil {
		t.Fatalf("my cycle: %v", err)
	}

	if _, ok := documentTabs(me)["tab1"]; !ok {
		t.Fatal("the colleague's tab did not arrive")
	}
	cards := cardsFromDocument(me.document(), "tab1")
	if len(cards) != 1 || cards[0].Title != "Their card" {
		t.Fatalf("cards = %+v, want one card titled %q", cards, "Their card")
	}
	if cards[0].Status != board.StatusDoing {
		t.Fatalf("status = %q, want %q", cards[0].Status, board.StatusDoing)
	}
}

func TestRejectedKeyStopsPolling(t *testing.T) {
	err := &APIError{Status: 401, Code: "unauthorized"}
	if _, retry := retryAfter(err); retry {
		t.Error("a 401 is reported as worth retrying")
	}

	if _, retry := retryAfter(errOffline); !retry {
		t.Error("a transport failure is reported as not worth retrying")
	}

	if _, retry := retryAfter(&APIError{Status: 500, Code: "internal"}); !retry {
		t.Error("a 500 is reported as not worth retrying; the server says it is safe to retry")
	}
}

func TestReplacingARunningWorkspaceDoesNotDeadlock(t *testing.T) {
	stateHome(t)

	client := newFakeOps()
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	w.UseOps(client)
	w.StartSync(t.Context())

	// A colleague's work is already on the server, so every pull has ops to
	// merge -- which is what puts the sync loop inside absorb, holding
	// nothing and wanting wsMu, at the moment the second join wants to close
	// it.
	them := newTestSync(t, client, "actor-b")
	batch, err := boardOps(them, "tab1", []board.Card{{ID: "T1", Title: "Theirs", Status: board.StatusTodo}})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := them.apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := them.cycle(context.Background()); err != nil {
		t.Fatalf("their cycle: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		for i := range 5 {
			if _, err := w.JoinWorkspace(context.Background(), "https://example.invalid", "wk_test"); err != nil {
				done <- err
				return
			}
			_ = i
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("join: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rejoining a live workspace did not finish: the sync loop and wsMu are waiting on each other")
	}

	if err := w.LeaveWorkspace(); err != nil {
		t.Fatalf("leave: %v", err)
	}
}

// BenchmarkPublishBoardNoWorkspace measures what the second stage costs a
// machine that has not joined one.
//
// The claim this exists to check is that Work with no workspace behaves
// exactly as it did before any of this, and that "exactly" includes the
// price. publishBoard is the only place the sync layer attaches to a run, and
// with none joined it is one atomic load and a nil compare.
func BenchmarkPublishBoardNoWorkspace(b *testing.B) {
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, b.TempDir())
	for i := range 8 {
		w.board.Add("r1", "anton", "card")
		_ = i
	}

	b.ReportAllocs()
	for b.Loop() {
		w.publishBoard()
	}
}

// BenchmarkShareBoardNoWorkspace isolates the added call itself.
func BenchmarkShareBoardNoWorkspace(b *testing.B) {
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, b.TempDir())
	cards := w.board.Snapshot()

	b.ReportAllocs()
	for b.Loop() {
		w.shareBoard(cards)
	}
}

// BenchmarkApplyOneBoardChange measures the durable half of stage two: two
// appends and two fsyncs -- the journal a restart reads, and the outbox the
// server still owes -- plus the merge.
//
// This is what a card moving to "doing" costs a joined workspace. It is paid
// a handful of times per run, never per token, which is the trade the whole
// design rests on: an expensive write on the rare path so the streaming path
// stays free.
func BenchmarkApplyOneBoardChange(b *testing.B) {
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())

	s, err := newSync(newFakeOps(), "https://example.invalid", "wk", "ws_bench", "actor", func(string, any) {})
	if err != nil {
		b.Fatalf("newSync: %v", err)
	}
	b.Cleanup(s.close)

	cards := []board.Card{{ID: "T1", Title: "Ship it", Status: board.StatusTodo}}
	seed, err := boardOps(s, "tab1", cards)
	if err != nil {
		b.Fatalf("boardOps: %v", err)
	}
	if err := s.apply(seed); err != nil {
		b.Fatalf("apply: %v", err)
	}

	statuses := []board.Status{board.StatusDoing, board.StatusTodo}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		cards[0].Status = statuses[i%len(statuses)]
		batch, err := boardOps(s, "tab1", cards)
		if err != nil {
			b.Fatalf("boardOps: %v", err)
		}
		if err := s.apply(batch); err != nil {
			b.Fatalf("apply: %v", err)
		}
	}
}

// TestTornJournalIsCoveredByTheOutbox is the assumption the single fsync
// rests on.
//
// A local apply syncs the outbox and not the journal, which is only safe
// because both hold the ops and startup replays the outbox over the journal.
// This truncates the journal the way a power cut would and checks the
// document still comes back.
func TestTornJournalIsCoveredByTheOutbox(t *testing.T) {
	stateHome(t)

	client := newFakeOps()
	client.setDown(true)

	first := newTestSync(t, client, "actor-a")
	batch, err := boardOps(first, "tab1", []board.Card{
		{ID: "T1", Title: "Unsynced", Status: board.StatusDoing},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := first.apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dir := first.dir
	first.close()

	// The journal is gone entirely -- a worse loss than a torn tail.
	if err := os.Truncate(filepath.Join(dir, journalFile), 0); err != nil {
		t.Fatalf("truncate journal: %v", err)
	}
	// The outbox is intact, because that is the one that was synced.
	if data, err := os.ReadFile(filepath.Join(dir, outboxFile)); err != nil {
		t.Fatalf("read outbox: %v", err)
	} else if !strings.Contains(string(data), "Unsynced") {
		t.Fatal("the outbox does not hold the op, so this test proves nothing")
	}

	second := newTestSync(t, client, "actor-a")
	cards := cardsFromDocument(second.document(), "tab1")
	if len(cards) != 1 || cards[0].Title != "Unsynced" {
		t.Fatalf("after a lost journal the board is %+v, want the op the outbox still had", cards)
	}
	if pending, _ := second.q.depth(); pending != len(batch) {
		t.Errorf("outbox holds %d ops, want %d", pending, len(batch))
	}
}

func TestOversizedOpIsRefusedBeforeTheOutbox(t *testing.T) {
	stateHome(t)

	s := newTestSync(t, newFakeOps(), "actor-a")

	// A note big enough that the server would answer 413. It must never
	// reach the outbox: a 413 is final, so the op would be retried forever
	// and everything behind it would stop.
	huge := strings.Repeat("x", maxOpBytes)
	batch, err := boardOps(s, "tab1", []board.Card{
		{ID: "T1", Title: "Too big", Note: huge, Status: board.StatusBlocked},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := s.apply(batch); err == nil {
		t.Fatal("an op over the server's size limit was queued")
	}
	if pending, _ := s.q.depth(); pending != 0 {
		t.Fatalf("outbox holds %d ops after a refused apply, want 0", pending)
	}
}

func TestPushBatchesStayUnderTheBodyLimit(t *testing.T) {
	// Ops just under the per-op cap: a full count-limited batch of these
	// would be far over the server's 1 MiB body.
	big := strings.Repeat("y", 200<<10)
	batch := make([]ops.Op, 0, pushBatch)
	for i := range pushBatch {
		fields, err := jsonFields(map[string]any{FieldNote: big})
		if err != nil {
			t.Fatalf("jsonFields: %v", err)
		}
		batch = append(batch, ops.Op{
			ID:     "op" + strconv.Itoa(i),
			Kind:   ops.KindSetFields,
			Actor:  "actor-a",
			Clock:  uint64(i + 1),
			Node:   "n1",
			Fields: fields,
		})
	}

	fitted := fitBatch(batch, maxPushBytes)
	if len(fitted) == 0 {
		t.Fatal("fitBatch returned nothing, so a drain would never move")
	}
	if len(fitted) >= len(batch) {
		t.Fatalf("fitBatch kept all %d ops, which is over the body limit", len(fitted))
	}

	encoded, err := json.Marshal(struct {
		Ops []ops.Op `json:"ops"`
	}{fitted})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) > 1<<20 {
		t.Fatalf("push body is %d bytes, over the server's 1 MiB limit", len(encoded))
	}

	// One op on its own always goes, however big, because apply already
	// refused anything unsendable.
	if got := len(fitBatch(batch[:1], 1)); got != 1 {
		t.Fatalf("fitBatch dropped a lone op, returning %d", got)
	}
}
