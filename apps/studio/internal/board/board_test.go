package board

import "testing"

func TestAddAndSnapshotPreserveOrder(t *testing.T) {
	b := New()
	first := b.Add("r1", "ada", "profile the renderer")
	second := b.Add("r1", "grace", "tidy the console")

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
	b.Add("r1", "ada", "original")

	cards := b.Snapshot()
	cards[0].Title = "tampered"

	if got := b.Snapshot()[0].Title; got != "original" {
		t.Errorf("board title = %q, want the snapshot to have been a copy", got)
	}
}

func TestSetStatusKeepsNotesOnlyWhenBlocked(t *testing.T) {
	b := New()
	id := b.Add("r1", "ada", "profile the renderer")

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
	b.Add("r1", "ada", "profile the renderer")
	b.SetStatus("nope", StatusDone, "")

	if got := b.Snapshot()[0].Status; got != StatusTodo {
		t.Errorf("status = %q, want the real card untouched", got)
	}
}

func TestClearEmptiesTheBoard(t *testing.T) {
	b := New()
	b.Add("r1", "ada", "profile the renderer")
	b.Clear()

	if got := b.Snapshot(); len(got) != 0 {
		t.Errorf("got %d cards after Clear, want none", len(got))
	}
}

func TestCardIDsAreShortAndReferenceable(t *testing.T) {
	b := New()
	if got := b.Add("r1", "ada", "one"); got != "T1" {
		t.Errorf("first card ID = %q, want T1", got)
	}
	if got := b.Add("r1", "grace", "two"); got != "T2" {
		t.Errorf("second card ID = %q, want T2", got)
	}
	// IDs keep counting across runs within a conversation.
	if got := b.Add("r2", "ada", "three"); got != "T3" {
		t.Errorf("third card ID = %q, want T3", got)
	}
}

func TestUpdateLeavesEmptyFieldsAlone(t *testing.T) {
	b := New()
	id := b.Add("r1", "ada", "original title")

	if !b.Update(id, "", "grace", StatusDoing) {
		t.Fatal("Update reported the card missing")
	}
	card := b.Snapshot()[0]
	if card.Title != "original title" {
		t.Errorf("title = %q, want it untouched", card.Title)
	}
	if card.AgentID != "grace" {
		t.Errorf("assignee = %q, want grace", card.AgentID)
	}
	if card.Status != StatusDoing {
		t.Errorf("status = %q, want doing", card.Status)
	}
}

func TestUpdateRejectsUnknownStatus(t *testing.T) {
	b := New()
	id := b.Add("r1", "ada", "one")
	b.Update(id, "", "", Status("finished-ish"))

	if got := b.Snapshot()[0].Status; got != StatusTodo {
		t.Errorf("status = %q, want the invalid value ignored", got)
	}
}

func TestUpdateReportsMissingCards(t *testing.T) {
	b := New()
	if b.Update("T99", "x", "", StatusDone) {
		t.Error("Update claimed to change a card that does not exist")
	}
}

func TestUpdateClearsTheNoteWhenUnblocked(t *testing.T) {
	b := New()
	id := b.Add("r1", "ada", "one")
	b.SetStatus(id, StatusBlocked, "it broke")
	b.Update(id, "", "", StatusDone)

	if got := b.Snapshot()[0].Note; got != "" {
		t.Errorf("note = %q, want it cleared", got)
	}
}
