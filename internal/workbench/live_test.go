package workbench

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/config"
)

// These tests run against a real sync server -- real routing, real bearer
// auth, real Postgres behind it -- rather than a fake. They skip themselves
// when there is not one, the same way the server's own store tests skip
// without TEST_DATABASE_URL, so `go test ./...` stays runnable with nothing
// installed.
//
//	docker compose up -d
//	WORK_LIVE_SERVER=http://127.0.0.1:8080 \
//	WORK_LIVE_SIGNUP_TOKEN=<WORK_SIGNUP_TOKEN> \
//	    go test ./internal/workbench -run Live -v
//
// What a fake cannot check, and these do: that the JSON this package writes
// is the JSON the server parses, that its 401 and 429 look like what the
// retry logic expects, that a write key is accepted where it should be, and
// that the sequence numbers a real Postgres hands out drive the cursor
// correctly.
func liveServer(t *testing.T) (serverURL, signupToken string) {
	t.Helper()
	serverURL = os.Getenv("WORK_LIVE_SERVER")
	signupToken = os.Getenv("WORK_LIVE_SIGNUP_TOKEN")
	if serverURL == "" || signupToken == "" {
		t.Skip("set WORK_LIVE_SERVER and WORK_LIVE_SIGNUP_TOKEN to run against a real server")
	}
	return serverURL, signupToken
}

// gate is a RoundTripper that can be cut, which is what being offline is.
//
// Everything above the socket stays real -- the same HTTPClient, the same
// request bodies, the same real server on the other side when the gate is
// open. Only the network goes away, which is the failure this is about.
type gate struct {
	open  atomic.Bool
	inner http.RoundTripper
}

func newGate() *gate {
	g := &gate{inner: http.DefaultTransport}
	g.open.Store(true)
	return g
}

var errCut = errors.New("gate: no network")

func (g *gate) RoundTrip(r *http.Request) (*http.Response, error) {
	if !g.open.Load() {
		return nil, errCut
	}
	return g.inner.RoundTrip(r)
}

// liveWorkbench builds a Workbench wired exactly as main.go wires one, with
// its own config home so it is a distinct machine.
func liveWorkbench(t *testing.T, configHome, projectDir string, client OpsClient) *Workbench {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, projectDir)
	w.UseOps(client)
	return w
}

// liveCreate makes a workspace on the real server and returns its write key.
func liveCreate(t *testing.T, serverURL, signupToken, name string) Created {
	t.Helper()
	client := &HTTPClient{}
	created, err := client.Create(t.Context(), serverURL, signupToken, name)
	if err != nil {
		t.Fatalf("create workspace on %s: %v", serverURL, err)
	}
	if created.Workspace.ID == "" || created.WriteKey == "" {
		t.Fatalf("server returned %+v, want an id and a write key", created)
	}
	return created
}

// pushBoard puts a card on the board and publishes it, which is the path a
// run takes. Sync is driven explicitly rather than by the poll so the test
// does not sleep on a timer.
func pushBoard(t *testing.T, w *Workbench, title string) string {
	t.Helper()
	id := w.board.Add("r1", "anton", title)
	w.publishBoard()
	return id
}

// drain runs sync cycles until the outbox is empty and the cursor has caught
// up with the server.
func drain(t *testing.T, w *Workbench) Status {
	t.Helper()
	s := w.sync.Load()
	if s == nil {
		t.Fatal("no workspace joined")
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := s.cycle(context.Background()); err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("sync never succeeded: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		st := s.status()
		if st.Pending == 0 && st.Cursor == st.Head {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("sync did not settle: %+v", st)
		}
	}
}

func TestLiveCreateJoinAndShareABoard(t *testing.T) {
	serverURL, signupToken := liveServer(t)
	created := liveCreate(t, serverURL, signupToken, "live/create-join")

	projectA := t.TempDir()
	a := liveWorkbench(t, t.TempDir(), projectA, &HTTPClient{})

	if _, err := a.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
		t.Fatalf("join with the write key: %v", err)
	}
	if got := a.Workspace().ID; got != created.Workspace.ID {
		t.Fatalf("joined workspace %s, want %s", got, created.Workspace.ID)
	}

	// A tab, bound locally, then made active.
	tab, err := a.NewTab("api")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if err := a.BindTab(tab.ID, projectA); err != nil {
		t.Fatalf("BindTab: %v", err)
	}
	if err := a.ActivateTab(tab.ID); err != nil {
		t.Fatalf("ActivateTab: %v", err)
	}
	if a.WorkDir() != projectA {
		t.Fatalf("workDir = %q, want the tab's folder %q", a.WorkDir(), projectA)
	}

	pushBoard(t, a, "Ship the sync server")
	first := drain(t, a)
	if first.Cursor == 0 {
		t.Fatal("nothing reached the server")
	}

	// A second machine, its own config home, joining with the same key.
	projectB := t.TempDir()
	b := liveWorkbench(t, t.TempDir(), projectB, &HTTPClient{})
	if _, err := b.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
		t.Fatalf("second machine join: %v", err)
	}
	drain(t, b)

	// The tab arrives through the log, and arrives unbound: where the first
	// machine keeps its project is not the second machine's business.
	view := b.Workspace()
	if len(view.Tabs) != 1 {
		t.Fatalf("second machine sees %d tabs, want 1: %+v", len(view.Tabs), view.Tabs)
	}
	if view.Tabs[0].Name != "api" {
		t.Errorf("tab name = %q, want %q", view.Tabs[0].Name, "api")
	}
	if view.Tabs[0].Dir != "" || view.Tabs[0].Bound {
		t.Errorf("adopted tab is bound to %q; a peer's folder must not cross the wire", view.Tabs[0].Dir)
	}
	if b.WorkDir() != projectB {
		t.Errorf("second machine's agents moved to %q, want its own start folder %q", b.WorkDir(), projectB)
	}

	cards := b.WorkspaceCards(tab.ID)
	if len(cards) != 1 || cards[0].Title != "Ship the sync server" {
		t.Fatalf("second machine sees %+v, want one card titled %q", cards, "Ship the sync server")
	}
	// A peer's cards are shown, not adopted: the local board is what this
	// machine's own runs are driving.
	if got := len(b.Board()); got != 0 {
		t.Errorf("second machine's own board has %d cards, want 0", got)
	}
}

func TestLiveOfflineQueuesPersistsAndReplays(t *testing.T) {
	serverURL, signupToken := liveServer(t)
	created := liveCreate(t, serverURL, signupToken, "live/offline")

	configHome := t.TempDir()
	project := t.TempDir()
	net := newGate()
	client := &HTTPClient{HTTP: &http.Client{Timeout: syncTimeout, Transport: net}}

	w := liveWorkbench(t, configHome, project, client)
	if _, err := w.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
		t.Fatalf("join: %v", err)
	}
	tab, err := w.NewTab("api")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if err := w.BindTab(tab.ID, project); err != nil {
		t.Fatalf("BindTab: %v", err)
	}
	if err := w.ActivateTab(tab.ID); err != nil {
		t.Fatalf("ActivateTab: %v", err)
	}
	drain(t, w)

	// The network goes away.
	net.open.Store(false)

	cardID := pushBoard(t, w, "Written while offline")
	w.board.SetStatus(cardID, board.StatusDoing, "")
	w.publishBoard()

	// Stage one happened anyway: the local board moved, and so did the local
	// document.
	if got := len(w.Board()); got != 1 {
		t.Fatalf("local board has %d cards while offline, want 1", got)
	}
	offline := w.SyncStatus()
	if offline.Pending == 0 {
		t.Fatal("offline edits did not queue")
	}
	found := false
	for _, c := range w.WorkspaceCards(tab.ID) {
		if c.Title == "Written while offline" && c.Status == board.StatusDoing {
			found = true
		}
	}
	if !found {
		t.Fatalf("the offline edit is not in the local document: %+v", w.WorkspaceCards(tab.ID))
	}

	// And it is durable, not just in memory.
	// Asked for rather than assembled: the directory is a hash of the
	// workspace ID, because the server says its IDs are opaque.
	stateDir, err := config.StateDir(created.Workspace.ID)
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if !strings.HasPrefix(stateDir, configHome) {
		t.Fatalf("state dir %q is outside this test's config home %q", stateDir, configHome)
	}
	queue := filepath.Join(stateDir, outboxFile)
	data, err := os.ReadFile(queue)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if !strings.Contains(string(data), "Written while offline") {
		t.Fatalf("the offline edit is not in the outbox on disk (%d bytes)", len(data))
	}
	if info, err := os.Stat(queue); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("outbox mode = %o, want 600", info.Mode().Perm())
	}

	// A cycle while offline must fail and change nothing.
	if err := w.sync.Load().cycle(context.Background()); err == nil {
		t.Fatal("a sync cycle succeeded with no network")
	}
	if got := w.SyncStatus().Pending; got != offline.Pending {
		t.Fatalf("a failed cycle changed the outbox: %d -> %d", offline.Pending, got)
	}

	// Restart: a new Workbench on the same config home, which is what the
	// next launch is. Nothing is carried over in memory.
	restarted := liveWorkbench(t, configHome, project, client)
	saved, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.Workspace == nil {
		t.Fatal("the workspace was not persisted, so a restart would lose it")
	}
	if err := restarted.UseWorkspace(saved.Workspace); err != nil {
		t.Fatalf("restore workspace: %v", err)
	}

	if got := restarted.SyncStatus().Pending; got != offline.Pending {
		t.Fatalf("outbox after restart holds %d ops, want %d", got, offline.Pending)
	}
	// The board a peer would see is already right, before any network call:
	// the outbox was replayed off disk.
	found = false
	for _, c := range restarted.WorkspaceCards(tab.ID) {
		if c.Title == "Written while offline" && c.Status == board.StatusDoing {
			found = true
		}
	}
	if !found {
		t.Fatal("the outbox was not replayed into the document on restart")
	}

	// The network comes back.
	net.open.Store(true)
	final := drain(t, restarted)
	if final.Pending != 0 {
		t.Fatalf("outbox did not drain: %d left", final.Pending)
	}
	if final.State != SyncOnline {
		t.Fatalf("state = %q, want %q", final.State, SyncOnline)
	}
	if final.Cursor != final.Head || final.Cursor == 0 {
		t.Fatalf("not caught up: cursor %d, head %d", final.Cursor, final.Head)
	}
	if data, err := os.ReadFile(queue); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Errorf("outbox file still holds %d bytes after the drain", len(data))
	}

	// Another machine sees the work that was done offline.
	peer := liveWorkbench(t, t.TempDir(), t.TempDir(), &HTTPClient{})
	if _, err := peer.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
		t.Fatalf("peer join: %v", err)
	}
	drain(t, peer)

	found = false
	for _, c := range peer.WorkspaceCards(tab.ID) {
		if c.Title == "Written while offline" && c.Status == board.StatusDoing {
			found = true
		}
	}
	if !found {
		t.Fatalf("offline work never reached another machine: %+v", peer.WorkspaceCards(tab.ID))
	}
}

func TestLiveBadKeyIsRejectedNotRetried(t *testing.T) {
	serverURL, signupToken := liveServer(t)
	created := liveCreate(t, serverURL, signupToken, "live/keys")

	w := liveWorkbench(t, t.TempDir(), t.TempDir(), &HTTPClient{})

	// A read key can read and cannot write, so joining with one is refused
	// here rather than at the first push.
	if _, err := w.JoinWorkspace(t.Context(), serverURL, created.ReadKey); err == nil {
		t.Fatal("joining with a read key was allowed")
	} else if !strings.Contains(err.Error(), "read") {
		t.Errorf("error does not explain the key is read-only: %v", err)
	}

	// A key the server has never seen fails as unauthorized, and the sync
	// loop must treat that as final rather than polling forever.
	_, err := w.JoinWorkspace(t.Context(), serverURL, "wk_0000000000000000000000000000000")
	if err == nil {
		t.Fatal("joining with an unknown key was allowed")
	}
	var api *APIError
	if !errors.As(err, &api) {
		t.Fatalf("error is not an APIError: %v", err)
	}
	if api.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", api.Status)
	}
	if !api.Fatal() {
		t.Error("a 401 is not treated as final; the loop would retry a dead key forever")
	}
	if _, retry := retryAfter(err); retry {
		t.Error("retryAfter says to keep polling on a 401")
	}

	// The real write key works, which is what makes the two checks above
	// mean something.
	if _, err := w.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
		t.Fatalf("join with the real write key: %v", err)
	}
}

func TestLiveDuplicatePushIsNotDuplicatedWork(t *testing.T) {
	serverURL, signupToken := liveServer(t)
	created := liveCreate(t, serverURL, signupToken, "live/idempotency")

	client := &HTTPClient{}
	end := struct{ url, key string }{serverURL, created.WriteKey}

	w := liveWorkbench(t, t.TempDir(), t.TempDir(), client)
	if _, err := w.JoinWorkspace(t.Context(), serverURL, created.WriteKey); err != nil {
		t.Fatalf("join: %v", err)
	}
	tab, err := w.NewTab("api")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if err := w.BindTab(tab.ID, t.TempDir()); err != nil {
		t.Fatalf("BindTab: %v", err)
	}
	if err := w.ActivateTab(tab.ID); err != nil {
		t.Fatalf("ActivateTab: %v", err)
	}
	pushBoard(t, w, "Only once")

	s := w.sync.Load()
	batch := s.q.head(pushBatch)
	if len(batch) == 0 {
		t.Fatal("nothing queued to push")
	}

	firstPush, err := client.Push(t.Context(), end.url, end.key, batch)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(firstPush.Accepted) != len(batch) {
		t.Fatalf("server accepted %d of %d ops", len(firstPush.Accepted), len(batch))
	}

	// The same request again is what a client that lost the response does.
	// It must append nothing and report every op as a duplicate, which is
	// the contract the outbox's ack relies on.
	again, err := client.Push(t.Context(), end.url, end.key, batch)
	if err != nil {
		t.Fatalf("replayed push: %v", err)
	}
	if len(again.Accepted) != 0 {
		t.Errorf("a replayed push appended %d ops", len(again.Accepted))
	}
	if len(again.Duplicates) != len(batch) {
		t.Errorf("replayed push reported %d duplicates, want %d", len(again.Duplicates), len(batch))
	}
	if again.Head != firstPush.Head {
		t.Errorf("head moved on a replay: %d -> %d", firstPush.Head, again.Head)
	}
}
