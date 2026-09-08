package board

import "testing"

func TestAddAndSnapshotPreserveOrder(t *testing.T) {
	b := New()
	first := b.Add("r1", "jeff", "profile the renderer")
	second := b.Add("r1", "chris", "tidy the console")

	cards := b.Snapshot()
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].ID != first || cards[1].ID != second {
		t.Errorf("cards out of order: %+v", cards)
	}
	if cards[0].Status != StatusTodo {
		t.Errorf("new card status = %q, want %q", cards[0].Status, StatusTodo)
	}
	if first == second {
		t.Error("two cards share an ID")
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	b := New()
	b.Add("r1", "jeff", "original")

	cards := b.Snapshot()
	cards[0].Title = "tampered"

	if got := b.Snapshot()[0].Title; got != "original" {
		t.Errorf("board title = %q, want the snapshot to have been a copy", got)
	}
}

func TestSetStatusKeepsNotesOnlyWhenBlocked(t *testing.T) {
	b := New()
	id := b.Add("r1", "jeff", "profile the renderer")

	b.SetStatus(id, StatusBlocked, "claude exploded")
	if card := b.Snapshot()[0]; card.Status != StatusBlocked || card.Note != "claude exploded" {
		t.Fatalf("card = %+v, want blocked with a note", card)
	}

	b.SetStatus(id, StatusDoing, "")
	if card := b.Snapshot()[0]; card.Status != StatusDoing || card.Note != "" {
		t.Errorf("card = %+v, want doing with no note", card)
	}
}

func TestSetStatusIgnoresUnknownCards(t *testing.T) {
	b := New()
	b.Add("r1", "jeff", "profile the renderer")
	b.SetStatus("nope", StatusDone, "")

	if got := b.Snapshot()[0].Status; got != StatusTodo {
		t.Errorf("status = %q, want the real card untouched", got)
	}
}

func TestClearEmptiesTheBoard(t *testing.T) {
	b := New()
	b.Add("r1", "jeff", "profile the renderer")
	b.Clear()

	if got := b.Snapshot(); len(got) != 0 {
		t.Errorf("got %d cards after Clear, want none", len(got))
	}
}
