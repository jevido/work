package workbench

import (
	"strings"

	"dev.jevido/work/internal/ops"
)

// Reading the parts of the document that are not the tree.
//
// Idea mode is called a mindmap, and a mindmap is not a tree: ideas hang in
// structure, and they also point at each other across branches and gather into
// regions. The tree is what the outline draws; this is what it draws on top.
//
// None of it is a new op kind. See [TypeEdge] for why that was a deliberate
// refusal rather than a shortcut.

// Link is one edge, as somebody reading a row needs it.
type Link struct {
	// Edge is the node that is the link, which is what a delete names.
	Edge string `json:"edge"`
	// Other is the node at the far end.
	Other string `json:"other"`
	// OtherText is what that node says, so a row can show the link without a
	// second lookup for every one of them.
	OtherText string `json:"otherText"`
	// Dangling is true when the far end has been deleted. The link is still
	// worth showing: it says what it linked, which is more than the node it
	// pointed at can say for itself.
	Dangling bool `json:"dangling,omitempty"`
	// Text is why the two are linked, written on the edge itself.
	//
	// On the edge rather than on either end, because that is what it is about:
	// "these two, because" is not a fact about either line on its own. Usually
	// empty -- most links say enough by existing.
	Text string `json:"text,omitempty"`
}

// Region is a named set of nodes.
type Region struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// graph indexes a tab's nodes once, so the three readers below do not each walk
// the document.
type graph struct {
	// text is every live node's text, by id.
	text map[string]string
	// alive is every node the tree still holds.
	alive map[string]bool
	// kind is each node's type.
	kind map[string]string
	// edges are the link nodes in this tab.
	edges []ops.Node
	// members is region id to the nodes in it.
	members map[string][]string
	// regionOf is node id to the region it is in.
	regionOf map[string]string
	// guidelines and parties are the two vocabularies, id to name.
	guidelines map[string]string
	parties    map[string]string
	// guidedBy and wantedBy are card id to the words on it, in document order.
	guidedBy map[string][]string
	wantedBy map[string][]string
}

func readGraph(doc Document, tab string) *graph {
	g := &graph{
		text:       map[string]string{},
		alive:      map[string]bool{},
		kind:       map[string]string{},
		members:    map[string][]string{},
		regionOf:   map[string]string{},
		guidelines: map[string]string{},
		parties:    map[string]string{},
		guidedBy:   map[string][]string{},
		wantedBy:   map[string][]string{},
	}

	// The joins are collected on the way past and resolved afterwards: a join
	// can sit above the word it names in the tree, and reading it in one pass
	// would drop whichever half had not been walked yet.
	type join struct{ from, to string }
	var guided, wanted []join

	var walk func(nodes []ops.TreeNode)
	walk = func(nodes []ops.TreeNode) {
		for _, n := range nodes {
			g.text[n.ID] = strings.TrimSpace(fieldString(n.Node, FieldText))
			g.alive[n.ID] = true
			kind := fieldString(n.Node, FieldType)
			if kind == "" {
				// An absent type reads as an idea: it is what every node
				// written before any of this existed says.
				kind = TypeIdea
			}
			g.kind[n.ID] = kind

			switch kind {
			case TypeEdge:
				g.edges = append(g.edges, n.Node)
			case TypeGuideline:
				g.guidelines[n.ID] = g.text[n.ID]
			case TypeParty:
				g.parties[n.ID] = g.text[n.ID]
			case TypeGuided:
				guided = append(guided, join{fieldString(n.Node, FieldFrom), fieldString(n.Node, FieldTo)})
			case TypeInterest:
				wanted = append(wanted, join{fieldString(n.Node, FieldFrom), fieldString(n.Node, FieldTo)})
			default:
				if region := fieldString(n.Node, FieldRegion); region != "" {
					g.members[region] = append(g.members[region], n.ID)
					g.regionOf[n.ID] = region
				}
			}
			walk(n.Children)
		}
	}
	for _, root := range doc.Tree {
		if root.ID == tab {
			walk(root.Children)
		}
	}

	// A join whose word has been deleted is dropped rather than shown as a
	// blank: a card claiming a guideline nobody can name is noise in a block
	// that is read to decide what to change.
	for _, j := range guided {
		if name, ok := g.guidelines[j.to]; ok && name != "" {
			g.guidedBy[j.from] = append(g.guidedBy[j.from], j.to)
		}
	}
	for _, j := range wanted {
		if name, ok := g.parties[j.to]; ok && name != "" {
			g.wantedBy[j.from] = append(g.wantedBy[j.from], j.to)
		}
	}
	return g
}

// LinksFrom is every edge touching a node, in either direction.
//
// Either direction, because an edge between two branches is one relationship
// and both ends of it should show the same thing. Which end was drawn first is
// an accident of who made it.
func (w *Workbench) LinksFrom(tab, node string) []Link {
	s := w.sync.Load()
	if s == nil {
		return nil
	}
	return readGraph(s.document(), tab).linksFor(node)
}

func (g *graph) linksFor(node string) []Link {
	var out []Link
	for _, edge := range g.edges {
		from := fieldString(edge, FieldFrom)
		to := fieldString(edge, FieldTo)

		var other string
		switch node {
		case from:
			other = to
		case to:
			other = from
		default:
			continue
		}
		if other == "" || other == node {
			// An edge to nothing, or a node linked to itself. Neither says
			// anything, and drawing them would be drawing noise.
			continue
		}
		out = append(out, Link{
			Edge:      edge.ID,
			Other:     other,
			OtherText: g.text[other],
			Dangling:  !g.alive[other],
			Text:      fieldString(edge, FieldText),
		})
	}
	return out
}

// RegionOf is the region a node is in, if any.
func (w *Workbench) RegionOf(tab, node string) (Region, bool) {
	s := w.sync.Load()
	if s == nil {
		return Region{}, false
	}
	g := readGraph(s.document(), tab)
	id := g.regionOf[node]
	if id == "" || !g.alive[id] {
		// A region that has been deleted is not a region a node is in. The
		// field stays on the member -- nothing rewrites other people's nodes
		// to tidy up -- and reads as nothing, which is what it now means.
		return Region{}, false
	}
	return Region{ID: id, Name: g.text[id]}, true
}

// RegionMembers is the nodes in a region, in document order.
func (w *Workbench) RegionMembers(tab, region string) []string {
	s := w.sync.Load()
	if s == nil {
		return nil
	}
	return readGraph(s.document(), tab).members[region]
}

// isOutlineNode reports whether a node is a line the outline draws.
//
// By what it is rather than by what it is not. "Everything except a task" was
// right when there were two types and became wrong the moment there were four,
// silently -- an edge would have been drawn as a line with no text in it.
func isOutlineNode(node ops.Node) bool {
	switch fieldString(node, FieldType) {
	case "", TypeIdea:
		return true
	default:
		return false
	}
}
