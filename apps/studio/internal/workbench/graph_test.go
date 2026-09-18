package workbench

import (
	"testing"
)

// link and group are what task 02 will do from the keyboard. Written as edits
// here because that is what they are: a node with a type, and a field on a
// member. No new op kind anywhere.
func link(from, to string) Edit {
	return Edit{
		Kind: "create-node",
		Node: "edge-" + from + "-" + to,
		Fields: map[string]any{
			FieldType: TypeEdge,
			FieldFrom: from,
			FieldTo:   to,
		},
	}
}

func TestLinksAreNodes(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())
	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: "create-node", Node: "a", Fields: map[string]any{FieldType: TypeIdea, FieldText: "a static handler"}},
		{Kind: "create-node", Node: "b", Fields: map[string]any{FieldType: TypeIdea, FieldText: "where the files come from"}},
		link("a", "b"),
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	t.Run("both ends see it", func(t *testing.T) {
		// One relationship, and which end was drawn first is an accident of
		// who made it.
		for _, end := range []struct{ node, other string }{{"a", "b"}, {"b", "a"}} {
			links := w.LinksFrom(tab, end.node)
			if len(links) != 1 {
				t.Fatalf("%s has %d links, want 1", end.node, len(links))
			}
			if links[0].Other != end.other {
				t.Errorf("%s links to %q, want %q", end.node, links[0].Other, end.other)
			}
			if links[0].OtherText == "" {
				t.Errorf("%s's link carries no text for the far end", end.node)
			}
			if links[0].Dangling {
				t.Errorf("%s's link is reported dangling with both ends alive", end.node)
			}
		}
	})

	t.Run("it is not a line in the outline", func(t *testing.T) {
		block := StateBlock(w.WorkspaceDocument(), tab, ModeOrientation)
		if contains(block, "edge-a-b") {
			t.Errorf("the edge is in the outline:\n%s", block)
		}
	})

	t.Run("deleting one end leaves it dangling, not broken", func(t *testing.T) {
		if _, err := w.ApplyEdits(tab, []Edit{{Kind: "delete-node", Node: "b"}}); err != nil {
			t.Fatal(err)
		}
		links := w.LinksFrom(tab, "a")
		if len(links) != 1 {
			t.Fatalf("the link went with the node: %d links", len(links))
		}
		if !links[0].Dangling {
			t.Error("a link to a deleted node is not reported as dangling")
		}
		// It still says what it linked, which is more than the node it pointed
		// at can say for itself.
		if links[0].Other != "b" {
			t.Errorf("the dangling link forgot what it pointed at: %q", links[0].Other)
		}
	})

	t.Run("unlinking is deleting the edge", func(t *testing.T) {
		if _, err := w.ApplyEdits(tab, []Edit{{Kind: "delete-node", Node: "edge-a-b"}}); err != nil {
			t.Fatal(err)
		}
		if links := w.LinksFrom(tab, "a"); len(links) != 0 {
			t.Errorf("the link survived its own delete: %+v", links)
		}
	})
}

func TestRegionsAreMembershipOnMembers(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())
	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: "create-node", Node: "r1", Fields: map[string]any{FieldType: TypeRegion, FieldText: "Networking"}},
		{Kind: "create-node", Node: "m1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "one", FieldRegion: "r1"}},
		{Kind: "create-node", Node: "m2", Fields: map[string]any{FieldType: TypeIdea, FieldText: "two", FieldRegion: "r1"}},
	}); err != nil {
		t.Fatalf("group: %v", err)
	}

	if got := w.RegionMembers(tab, "r1"); len(got) != 2 {
		t.Errorf("members = %v, want two", got)
	}
	region, ok := w.RegionOf(tab, "m1")
	if !ok {
		t.Fatal("a member is not in its region")
	}
	if region.Name != "Networking" {
		t.Errorf("region name = %q", region.Name)
	}

	t.Run("a region is not a line in the outline", func(t *testing.T) {
		// Its name appears beside the lines that are in it, which is the point
		// of showing it at all. What must not appear is the region as a line of
		// its own -- "r1  Networking" in the id-first column.
		block := StateBlock(w.WorkspaceDocument(), tab, ModeOrientation)
		if contains(block, "r1  Networking") {
			t.Errorf("the region is a line in the outline:\n%s", block)
		}
		if !contains(block, "[in r1, Networking]") {
			t.Errorf("a member does not say which region it is in:\n%s", block)
		}
	})

	t.Run("a deleted region is no region at all", func(t *testing.T) {
		// The field stays on the member -- nothing rewrites other people's
		// nodes to tidy up -- and reads as nothing, which is what it means.
		if _, err := w.ApplyEdits(tab, []Edit{{Kind: "delete-node", Node: "r1"}}); err != nil {
			t.Fatal(err)
		}
		if _, ok := w.RegionOf(tab, "m1"); ok {
			t.Error("a member still claims a deleted region")
		}
	})
}

// The case a list on the region would lose. Two people adding different members
// write two different nodes' fields, which do not collide at all.
func TestTwoPeopleAddToOneRegion(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())
	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: "create-node", Node: "r1", Fields: map[string]any{FieldType: TypeRegion, FieldText: "Networking"}},
		{Kind: "create-node", Node: "m1", Fields: map[string]any{FieldType: TypeIdea, FieldText: "one"}},
		{Kind: "create-node", Node: "m2", Fields: map[string]any{FieldType: TypeIdea, FieldText: "two"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Two separate edits, as two people would make them.
	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: "set-fields", Node: "m1", Fields: map[string]any{FieldRegion: "r1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: "set-fields", Node: "m2", Fields: map[string]any{FieldRegion: "r1"}},
	}); err != nil {
		t.Fatal(err)
	}

	if got := w.RegionMembers(tab, "r1"); len(got) != 2 {
		t.Errorf("members = %v, want both -- a list on the region would have kept one", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && containsSub(haystack, needle)
}

// A link on the line it belongs to, in the block Claude reads. A model that
// cannot see a link cannot be asked to remove one, and cannot avoid proposing
// one that is already there.
func TestStateBlockShowsRelations(t *testing.T) {
	w, tab := joinedWorkbench(t, newFakeOps())
	if _, err := w.ApplyEdits(tab, []Edit{
		{Kind: "create-node", Node: "a", Fields: map[string]any{FieldType: TypeIdea, FieldText: "a static handler"}},
		{Kind: "create-node", Node: "b", Fields: map[string]any{FieldType: TypeIdea, FieldText: "where the files come from"}},
		link("a", "b"),
	}); err != nil {
		t.Fatal(err)
	}

	block := StateBlock(w.WorkspaceDocument(), tab, ModeOrientation)
	if !contains(block, "a  a static handler  <-> b") {
		t.Errorf("the link is not on the line:\n%s", block)
	}
	if !contains(block, "b  where the files come from  <-> a") {
		t.Errorf("the link is not on the other line:\n%s", block)
	}
	// And the notation is explained, because a model reading `<->` with no
	// legend is a model guessing.
	if !contains(block, "`<->` is a link") {
		t.Errorf("the block does not say what the notation means:\n%s", block)
	}

	t.Run("a dangling link is still named", func(t *testing.T) {
		// Removing it is the only useful thing left to do with one.
		if _, err := w.ApplyEdits(tab, []Edit{{Kind: "delete-node", Node: "b"}}); err != nil {
			t.Fatal(err)
		}
		block := StateBlock(w.WorkspaceDocument(), tab, ModeOrientation)
		if !contains(block, "<-> b (gone)") {
			t.Errorf("a dangling link is not shown:\n%s", block)
		}
	})
}
