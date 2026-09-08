// Package board holds the task board: what Anton has decided needs doing, who
// it belongs to, and how far along it is.
//
// The board is not a separate planning system. Anton already creates and
// assigns tasks when he routes a request, so the board shows those assignments
// and tracks them as they run. Nothing here costs an extra Claude call.
package board

import "sync"

// Status is the column a card sits in.
type Status string

const (
	// StatusTodo is assigned but not started.
	StatusTodo Status = "todo"
	// StatusDoing is being worked on right now.
	StatusDoing Status = "doing"
	// StatusDone finished cleanly.
	StatusDone Status = "done"
	// StatusBlocked failed and needs attention.
	StatusBlocked Status = "blocked"
)

// Card is one task on the board.
type Card struct {
	ID string `json:"id"`
	// RunID ties the card to the request that created it.
	RunID string `json:"runId"`
	// AgentID is who the task is assigned to.
	AgentID string `json:"agentId"`
	// Title is the task itself, in Anton's words.
	Title string `json:"title"`
	// Note carries a failure reason for a blocked card.
	Note   string `json:"note,omitempty"`
	Status Status `json:"status"`
}

// Board is an ordered set of cards, safe for concurrent use.
//
// It is deliberately tiny and snapshot-based: a run adds a handful of cards and
// changes each one two or three times, so publishing the whole board on every
// change costs nothing and removes any chance of the frontend drifting out of
// sync with the backend.
type Board struct {
	mu    sync.Mutex
	cards []Card
	seq   int
}

// New returns an empty board.
func New() *Board {
	return &Board{}
}

// Add puts a new card on the board and returns its ID. Order of addition is
// preserved, so the columns read in the order Anton assigned the work.
func (b *Board) Add(runID, agentID, title string) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq++
	id := runID + "-c" + itoa(b.seq)
	b.cards = append(b.cards, Card{
		ID:      id,
		RunID:   runID,
		AgentID: agentID,
		Title:   title,
		Status:  StatusTodo,
	})
	return id
}

// SetStatus moves a card to another column. A note is kept only for a blocked
// card, and is cleared when the card moves on.
func (b *Board) SetStatus(id string, status Status, note string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i := range b.cards {
		if b.cards[i].ID != id {
			continue
		}
		b.cards[i].Status = status
		if status == StatusBlocked {
			b.cards[i].Note = note
		} else {
			b.cards[i].Note = ""
		}
		return
	}
}

// Snapshot returns a copy of every card, in order.
func (b *Board) Snapshot() []Card {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]Card, len(b.cards))
	copy(out, b.cards)
	return out
}

// Clear empties the board, for when a new conversation begins.
func (b *Board) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cards = nil
	b.seq = 0
}

// itoa avoids pulling strconv in for one small job.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
