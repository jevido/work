package workbench

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/ops"
)

// TestZeroIsTheFirstDigit pins the one constant that had to be spelled out by
// hand, because Go will not let a constant string be indexed at compile time.
func TestZeroIsTheFirstDigit(t *testing.T) {
	if digits[0] != zero {
		t.Fatalf("zero is %q but digits starts with %q", zero, digits[0])
	}
	if base != len(digits) {
		t.Fatalf("base is %d but the alphabet is %d long", base, len(digits))
	}
}

// TestAppendingKeepsKeysShort is the property the whole scheme exists for.
//
// The midpoint scheme this replaced lengthened a key by about a character
// every five appends, so an outline written top to bottom crossed
// ops.MaxPositionLen somewhere past 1275 lines and the op was refused. The
// assertion is therefore on the shape of the growth, not on any particular
// key: a key after N appends is logarithmic in N, so the cap is not a limit on
// how long a list may be.
func TestAppendingKeepsKeysShort(t *testing.T) {
	const appends = 20000

	key := ""
	prev := ""
	longest := 0
	for i := range appends {
		key = between(key, "")
		if key <= prev {
			t.Fatalf("append %d produced %q, which does not sort after %q", i, key, prev)
		}
		longest = max(longest, len(key))
		prev = key
	}

	// base-62 with a length-carrying head: 62 keys at two bytes, 62^2 at
	// three, 62^3 at four. 20000 is inside the four-byte range.
	if want := 4; longest != want {
		t.Fatalf("%d appends reached %d bytes, want %d (final key %q)", appends, longest, want, key)
	}
	if longest > ops.MaxPositionLen {
		t.Fatalf("%d appends reached %d bytes, over the %d cap", appends, longest, ops.MaxPositionLen)
	}

	// The case that was actually broken, called out on its own so a
	// regression names it.
	if got := len(keyAfterAppends(5000)); got > 4 {
		t.Fatalf("5000 appends gave a %d byte key, want no more than 4", got)
	}
}

func keyAfterAppends(n int) string {
	key := ""
	for range n {
		key = between(key, "")
	}
	return key
}

// TestInsertingBetweenOnePairGrowsBounded covers the one case that does
// lengthen a key: dropping a card between the same two neighbours over and
// over. Base 62 gives about six insertions per byte, so the growth is linear
// with a very small constant rather than free -- which is fine, because
// nothing does this thousands of times in one place.
func TestInsertingBetweenOnePairGrowsBounded(t *testing.T) {
	const inserts = 200

	lo := between("", "")
	hi := between(lo, "")
	for i := range inserts {
		next := between(lo, hi)
		if next <= lo || next >= hi {
			t.Fatalf("insert %d produced %q, which is not strictly between %q and %q", i, next, lo, hi)
		}
		hi = next
	}

	// Six insertions per byte plus the integer part it hangs off.
	if limit := inserts/5 + 4; len(hi) > limit {
		t.Fatalf("%d insertions gave a %d byte key, want no more than %d", inserts, len(hi), limit)
	}
	if len(hi) > ops.MaxPositionLen {
		t.Fatalf("%d insertions gave a %d byte key, over the %d cap", inserts, len(hi), ops.MaxPositionLen)
	}
}

// TestBetweenIsAlwaysStrictlyBetween exercises the three entry points against
// each other -- append, prepend, and insert at a random gap -- and checks the
// only invariant that matters after every single one: the list is still
// sorted, and no two keys collide.
//
// Deterministic seed, because a sort key bug that only reproduces on some runs
// is a sort key bug nobody fixes.
func TestBetweenIsAlwaysStrictlyBetween(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	keys := []string{}

	for step := range 4000 {
		var before, after string
		switch i := rng.Intn(len(keys) + 1); {
		case len(keys) == 0:
		case i == 0:
			after = keys[0]
		case i == len(keys):
			before = keys[len(keys)-1]
		default:
			before, after = keys[i-1], keys[i]
		}

		key := between(before, after)
		if before != "" && key <= before {
			t.Fatalf("step %d: %q does not sort after %q", step, key, before)
		}
		if after != "" && key >= after {
			t.Fatalf("step %d: %q does not sort before %q", step, key, after)
		}
		if len(key) > ops.MaxPositionLen {
			t.Fatalf("step %d: %q is %d bytes, over the %d cap", step, key, len(key), ops.MaxPositionLen)
		}

		at, _ := slices.BinarySearch(keys, key)
		keys = slices.Insert(keys, at, key)
		if !slices.IsSorted(keys) {
			t.Fatalf("step %d: inserting %q left the list unsorted", step, key)
		}
	}

	if len(slices.Compact(slices.Clone(keys))) != len(keys) {
		t.Fatal("two keys collided")
	}
}

// TestMatchesTheTypeScriptImplementation pins the port against the source it
// was ported from.
//
// Both ends write positions into the same document, so a key the desktop mints
// has to be a key the frontend can insert next to and vice versa. These vectors
// came out of frontend/src/lib/workspace/position.ts. Checked as a table rather
// than by running node, so the Go suite has no opinion about whether a
// TypeScript toolchain is installed -- if that file's algorithm changes, this
// fails and the two get reconciled deliberately.
func TestMatchesTheTypeScriptImplementation(t *testing.T) {
	pairs := []struct{ before, after, want string }{
		{"", "", "a0"},
		{"a0", "", "a1"},
		{"a0", "a1", "a0V"},
		{"a0", "a2", "a1"},
		{"Zz", "", "a0"},
		{"", "a0", "Zz"},
		{"", "Zz", "Zy"},
		{"a0V", "a1", "a0l"},
		{"az", "", "b00"},
		{"b00", "", "b01"},
		{"a1", "a1V", "a1G"},
		// Unparseable bounds, where both implementations fall back to a plain
		// base-62 midpoint.
		{"000001", "000002", "000001V"},
		{"000005", "a0", "I"},
	}
	for _, p := range pairs {
		if got := between(p.before, p.after); got != p.want {
			t.Errorf("between(%q, %q) = %q, want %q", p.before, p.after, got, p.want)
		}
	}

	// Append checkpoints, including the two places the integer part grows.
	checkpoints := map[int]string{0: "a0", 61: "az", 62: "b00", 3843: "byz", 19999: "c4BZ"}
	key := ""
	for i := range 20000 {
		key = between(key, "")
		if want, ok := checkpoints[i]; ok && key != want {
			t.Errorf("append %d = %q, want %q", i, key, want)
		}
	}

	// Prepend checkpoints, which walk the integer part the other way.
	key = ""
	for i := range 500 {
		key = between("", key)
		if i == 1 && key != "Zz" {
			t.Errorf("prepend 1 = %q, want %q", key, "Zz")
		}
		if i == 499 && key != "Ysx" {
			t.Errorf("prepend 499 = %q, want %q", key, "Ysx")
		}
	}
}

// TestLegacyDecimalKeysSortBeforeNewOnes is the one place this deliberately
// does something the TypeScript does not.
//
// Before this scheme, a card's key was fmt.Sprintf("%06d", index) -- six ASCII
// decimal digits. Those are not well-formed order keys, so appending after one
// lands in the fallback, and the fallback is the converging midpoint this
// whole file exists to avoid.
//
// Every well-formed key begins with a letter and every letter sorts above
// every digit, so "a0" is above all six-digit decimal keys. Appending after
// one therefore returns "a0" and the old numbering becomes a prefix of the new
// -- no migration, and no move-node per existing card to perform one.
func TestLegacyDecimalKeysSortBeforeNewOnes(t *testing.T) {
	legacy := []string{"000000", "000001", "000002", "000007", "000123", "999999"}

	for _, old := range legacy {
		got := between(old, "")
		if got != firstKey {
			t.Errorf("between(%q, \"\") = %q, want %q", old, got, firstKey)
		}
		if got <= old {
			t.Errorf("%q does not sort after the legacy key %q", got, old)
		}
	}

	// And appending continues normally from there, rather than converging the
	// way the fallback would have.
	keys := append([]string(nil), legacy...)
	key := legacy[len(legacy)-1]
	for range 2000 {
		key = between(key, "")
		keys = append(keys, key)
	}
	if !slices.IsSorted(keys) {
		t.Fatal("legacy and new keys do not form one sorted list")
	}
	if len(key) > 4 {
		t.Fatalf("appending after a legacy key gave a %d byte key: %q", len(key), key)
	}
}

// TestSamePositionIsATieNotACorruption covers two replicas choosing the same
// key for different nodes, which nothing prevents and nothing needs to.
//
// The merge orders siblings by position and then by node ID, so both machines
// render the same order. It only means neither card inserted "before" the
// other.
func TestSamePositionIsATieNotACorruption(t *testing.T) {
	shared := between("", "")

	// The same two creates, merged in opposite orders, as two replicas that
	// have not heard of each other would each see them.
	left := ops.Op{ID: "o1", Kind: ops.KindCreateNode, Actor: "m1", Clock: 1, Node: "c_m1_T1", Parent: "tab1", Position: shared}
	right := ops.Op{ID: "o2", Kind: ops.KindCreateNode, Actor: "m2", Clock: 1, Node: "c_m2_T1", Parent: "tab1", Position: shared}
	root := ops.Op{ID: "o0", Kind: ops.KindCreateNode, Actor: "m1", Clock: 1, Node: "tab1"}

	order := func(batch ...ops.Op) []string {
		var state ops.State
		if _, err := state.ApplyAll(batch); err != nil {
			t.Fatalf("ApplyAll: %v", err)
		}
		var got []string
		for _, node := range state.Tree() {
			if node.ID != "tab1" {
				continue
			}
			for _, child := range node.Children {
				got = append(got, child.ID)
			}
		}
		return got
	}

	first := order(root, left, right)
	second := order(root, right, left)
	if !slices.Equal(first, second) {
		t.Fatalf("merge order depends on arrival: %v then %v", first, second)
	}
	if want := []string{"c_m1_T1", "c_m2_T1"}; !slices.Equal(first, want) {
		t.Fatalf("tied positions ordered %v, want %v (by node ID)", first, want)
	}
}

/* -------------------------------------------------------------------------- */
/* What the board does with the keys                                          */
/* -------------------------------------------------------------------------- */

// mergeRemote merges ops into the document without queueing them for push,
// which is what arriving from a peer looks like to everything downstream. It
// is the state half of Sync.pull.
func mergeRemote(t *testing.T, s *Sync, batch ...ops.Op) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.state.ApplyAll(batch); err != nil {
		t.Fatalf("merge: %v", err)
	}
}

func publish(t *testing.T, s *Sync, tab string, cards []board.Card) []ops.Op {
	t.Helper()
	batch, err := boardOps(s, tab, cards)
	if err != nil {
		t.Fatalf("boardOps: %v", err)
	}
	if err := s.apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return batch
}

func testCards(n int) []board.Card {
	out := make([]board.Card, n)
	for i := range out {
		out[i] = board.Card{ID: fmt.Sprintf("T%d", i+1), Title: fmt.Sprintf("card %d", i+1), Status: board.StatusTodo}
	}
	return out
}

func kinds(batch []ops.Op) []string {
	var out []string
	for _, op := range batch {
		out = append(out, string(op.Kind))
	}
	return out
}

// TestAddingACardMovesNoOtherCard is the cost this scheme was chosen for.
//
// Under the old index-derived keys a card's position was recomputed from its
// slice index on every publish. That was harmless while the board only ever
// appended -- but it meant the board asserted the order of every sibling on
// every publish, which is the machinery that produced a move-node per
// following sibling the moment anything inserted anywhere but the end.
func TestAddingACardMovesNoOtherCard(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	publish(t, s, "tab1", testCards(20))

	batch := publish(t, s, "tab1", testCards(21))
	if got := kinds(batch); len(got) != 1 || got[0] != string(ops.KindCreateNode) {
		t.Fatalf("adding the 21st card emitted %v, want one create-node", got)
	}
}

// TestCardsKeepTheOrderTheyWereAddedIn is the property the old scheme bought
// with those moves, and it has to survive without them.
func TestCardsKeepTheOrderTheyWereAddedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	// One card at a time, as a run actually produces them.
	for n := 1; n <= 40; n++ {
		publish(t, s, "tab1", testCards(n))
	}

	var got []string
	for _, card := range cardsFromDocument(s.document(), "tab1") {
		got = append(got, card.Title)
	}
	var want []string
	for _, card := range testCards(40) {
		want = append(want, card.Title)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cards read back as %v, want %v", got, want)
	}
}

// TestAStatusChangeDoesNotTouchPositions is the common publish: one field of
// one card, and nothing else on the wire.
func TestAStatusChangeDoesNotTouchPositions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	cards := testCards(10)
	publish(t, s, "tab1", cards)

	cards[3].Status = board.StatusDoing
	batch := publish(t, s, "tab1", cards)
	if got := kinds(batch); len(got) != 1 || got[0] != string(ops.KindSetFields) {
		t.Fatalf("a status change emitted %v, want one set-fields", got)
	}
}

// TestAPeerReorderSurvivesTheNextPublish is the bug the old scheme had and
// this one does not.
//
// Positions are shared. Someone else -- a colleague, or the outline reading
// the same document -- may move one of this machine's cards within the tab.
// Recomputing the key from the local board index meant the very next publish
// moved it straight back, so the reorder lasted until the next status change.
func TestAPeerReorderSurvivesTheNextPublish(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	cards := testCards(3)
	publish(t, s, "tab1", cards)

	// A peer drags the first card to the end.
	last, _ := s.node(cardNode("m1", "T3"))
	mergeRemote(t, s, ops.Op{
		ID:       "peer1",
		Kind:     ops.KindMoveNode,
		Actor:    "m2",
		Clock:    9999,
		Node:     cardNode("m1", "T1"),
		Parent:   "tab1",
		Position: between(last.Position, ""),
	})

	reordered := []string{"card 2", "card 3", "card 1"}
	titles := func() []string {
		var out []string
		for _, card := range cardsFromDocument(s.document(), "tab1") {
			out = append(out, card.Title)
		}
		return out
	}
	if got := titles(); !slices.Equal(got, reordered) {
		t.Fatalf("after the peer move the order is %v, want %v", got, reordered)
	}

	// A publish that changes nothing must write nothing...
	if batch := publish(t, s, "tab1", cards); len(batch) != 0 {
		t.Fatalf("an unchanged board emitted %v", kinds(batch))
	}
	// ...and a publish that changes something must not drag the card back.
	cards[1].Status = board.StatusDone
	publish(t, s, "tab1", cards)
	if got := titles(); !slices.Equal(got, reordered) {
		t.Fatalf("a status change undid the peer's reorder: %v, want %v", got, reordered)
	}
}

// TestANewCardLandsAfterEveryonesCards checks that a card appended locally
// goes after the whole tab, not after the last card this machine made. Two
// people working in one tab is the case the workspace exists for.
func TestANewCardLandsAfterEveryonesCards(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	publish(t, s, "tab1", testCards(2))

	mine, _ := s.node(cardNode("m1", "T2"))
	fields, err := jsonFields(map[string]any{FieldKind: KindCard, FieldTitle: "theirs", FieldOrigin: "m2"})
	if err != nil {
		t.Fatalf("jsonFields: %v", err)
	}
	mergeRemote(t, s, ops.Op{
		ID:       "peer1",
		Kind:     ops.KindCreateNode,
		Actor:    "m2",
		Clock:    9999,
		Node:     cardNode("m2", "T1"),
		Parent:   "tab1",
		Position: between(mine.Position, ""),
		Fields:   fields,
	})

	publish(t, s, "tab1", testCards(3))

	var got []string
	for _, card := range cardsFromDocument(s.document(), "tab1") {
		got = append(got, card.Title)
	}
	want := []string{"card 1", "card 2", "theirs", "card 3"}
	if !slices.Equal(got, want) {
		t.Fatalf("order is %v, want %v", got, want)
	}
}

// TestSwitchingTabsAppendsToTheNewTab covers the one move the board still
// emits: the active tab changed with cards still on it, so the cards belong
// somewhere else now and need a key that means something among their new
// siblings.
func TestSwitchingTabsAppendsToTheNewTab(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	cards := testCards(2)
	publish(t, s, "tab1", cards)

	batch := publish(t, s, "tab2", cards)
	var moves []string
	for _, op := range batch {
		if op.Kind != ops.KindMoveNode {
			continue
		}
		if op.Parent != "tab2" {
			t.Fatalf("move went to %q, want tab2", op.Parent)
		}
		moves = append(moves, op.Position)
	}
	if len(moves) != 2 {
		t.Fatalf("switching tabs emitted %d moves, want 2: %v", len(moves), kinds(batch))
	}
	if !slices.IsSorted(moves) || moves[0] == moves[1] {
		t.Fatalf("the two cards moved to %v, which does not keep their order", moves)
	}

	var got []string
	for _, card := range cardsFromDocument(s.document(), "tab2") {
		got = append(got, card.Title)
	}
	if want := []string{"card 1", "card 2"}; !slices.Equal(got, want) {
		t.Fatalf("tab2 reads as %v, want %v", got, want)
	}
}

// TestPositionsStayUnderTheProtocolCap runs a long board through the real
// publish path and checks every key an op carried, because the cap is enforced
// by ops.Op.Validate and a key over it is a refused op, not a long string.
func TestPositionsStayUnderTheProtocolCap(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newTestSync(t, newFakeOps(), "m1")

	longest := 0
	for n := 1; n <= 400; n++ {
		for _, op := range publish(t, s, "tab1", testCards(n)) {
			if err := op.Validate(); err != nil {
				t.Fatalf("op %s: %v", op.ID, err)
			}
			longest = max(longest, len(op.Position))
		}
	}
	if longest > 3 {
		t.Fatalf("400 cards reached a %d byte key, want no more than 3", longest)
	}
}
