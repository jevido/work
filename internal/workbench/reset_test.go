package workbench

import (
	"context"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/config"
)

// The point of the reset: work this machine never got accepted is gone, and
// what comes back is what the server holds.
func TestResetToServerDropsUnsentWorkAndTakesTheLog(t *testing.T) {
	stateHome(t)

	client := newFakeOps()

	// A colleague's board is already in the log.
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

	// This machine writes something of its own while the server cannot be
	// reached, so it is in the outbox and nowhere else.
	me := newTestSync(t, client, "actor-a")
	client.setDown(true)
	mine, err := boardOps(me, "tab1", []board.Card{
		{ID: "M1", Title: "Mine, unsent", Status: board.StatusTodo},
	})
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := me.apply(mine); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if pending, _ := me.q.depth(); pending == 0 {
		t.Fatal("the unsent op did not reach the outbox, so this proves nothing")
	}
	client.setDown(false)

	if err := me.resetToServer(context.Background()); err != nil {
		t.Fatalf("resetToServer: %v", err)
	}

	if pending, _ := me.q.depth(); pending != 0 {
		t.Errorf("outbox holds %d ops, want none: they are what was discarded", pending)
	}
	cards := cardsFromDocument(me.document(), "tab1")
	if len(cards) != 1 || cards[0].Title != "Their card" {
		t.Fatalf("cards = %+v, want only the server's", cards)
	}

	// And nothing is left to push it back up on the next cycle, which is the
	// half that would make the reset undo itself.
	if err := me.cycle(context.Background()); err != nil {
		t.Fatalf("cycle after reset: %v", err)
	}
	for _, op := range client.entries() {
		if op.Actor == "actor-a" {
			t.Fatalf("a discarded op reached the server after the reset: %+v", op)
		}
	}
}

// A workspace with no server has no other copy, so "take the server's" has
// nothing to take and must not empty it.
func TestResetToServerRefusesALocalWorkspace(t *testing.T) {
	stateHome(t)

	s, err := newSync(nil, "", "", "ws_local", "actor", func(string, any) {})
	if err != nil {
		t.Fatalf("newSync: %v", err)
	}
	t.Cleanup(s.close)

	if err := s.resetToServer(context.Background()); err == nil {
		t.Fatal("a local workspace was reset from a server it does not have")
	}
}

// Every workspace whose keys this machine kept is listed, with the joined one
// marked -- which is the whole of what the keys panel draws.
func TestKnownWorkspacesListsWhatThisMachineHolds(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	w.UseOps(newFakeOps())

	left := &config.Workspace{
		ID:        "ws_left",
		Name:      "Left behind",
		ServerURL: "https://work.example",
		WriteKey:  "wk_left",
		ReadKey:   "rk_left",
		Actor:     "actor_left",
	}
	if err := w.adopt(left, true); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	joined := &config.Workspace{
		ID:        "ws_here",
		Name:      "In front",
		ServerURL: "https://work.example",
		WriteKey:  "wk_here",
		Actor:     "actor_here",
	}
	if err := w.adopt(joined, true); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	known, err := w.KnownWorkspaces()
	if err != nil {
		t.Fatalf("KnownWorkspaces: %v", err)
	}
	if len(known) != 2 {
		t.Fatalf("listed %d workspaces, want both: %+v", len(known), known)
	}
	if !known[0].Joined || known[0].ID != "ws_here" {
		t.Errorf("first entry = %+v, want the joined workspace first", known[0])
	}
	byID := map[string]KnownWorkspace{}
	for _, entry := range known {
		byID[entry.ID] = entry
	}
	if got := byID["ws_left"]; got.WriteKey != "wk_left" || got.ReadKey != "rk_left" {
		t.Errorf("the workspace this machine left = %+v, want both its keys", got)
	}

	// The joined one cannot be forgotten: that is leaving, and leaving is a
	// different call.
	if err := w.ForgetWorkspace("ws_here"); err == nil {
		t.Error("the joined workspace's keys were forgotten out from under it")
	}
	if err := w.ForgetWorkspace("ws_left"); err != nil {
		t.Fatalf("ForgetWorkspace: %v", err)
	}
	known, err = w.KnownWorkspaces()
	if err != nil {
		t.Fatalf("KnownWorkspaces: %v", err)
	}
	if len(known) != 1 || known[0].ID != "ws_here" {
		t.Errorf("after forgetting, listed %+v, want only the joined one", known)
	}
}
