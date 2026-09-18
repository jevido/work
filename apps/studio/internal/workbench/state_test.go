package workbench

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"dev.jevido/work/packages/ops"
)

// node builds a tree node with the fields a state block reads.
func node(id string, fields map[string]any, children ...ops.TreeNode) ops.TreeNode {
	encoded := map[string]json.RawMessage{}
	for name, value := range fields {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		encoded[name] = raw
	}
	return ops.TreeNode{
		Node:     ops.Node{ID: id, Fields: encoded},
		Children: children,
	}
}

func idea(id, text string, children ...ops.TreeNode) ops.TreeNode {
	return node(id, map[string]any{FieldType: TypeIdea, FieldText: text}, children...)
}

func task(id, text, status, from string) ops.TreeNode {
	return node(id, map[string]any{
		FieldType:              TypeTask,
		FieldText:              text,
		FieldStatus:            status,
		ops.FieldExtractedFrom: from,
	})
}

// A tab root holding a small outline and two tasks extracted from it.
func sampleDoc() Document {
	return Document{Tree: []ops.TreeNode{
		node("tab1", map[string]any{FieldText: "Work"},
			node("n_root", map[string]any{FieldType: TypeIdea, FieldText: "Serve the viewer", ops.FieldTaskID: "t_1"},
				idea("n_a", "A static handler in the API"),
				idea("n_b", "Where the files come from"),
			),
			idea("n_c", "The image builds the viewer"),
			task("t_1", "Mount the static handler", TaskDone, "n_root"),
			task("t_2", "Read WORK_SITE_DIR at startup", TaskDoing, "n_gone"),
		),
	}}
}

func TestStateBlockIdea(t *testing.T) {
	got := StateBlock(sampleDoc(), "tab1", ModeOrientation)

	t.Run("every live idea is named, indented by depth", func(t *testing.T) {
		for _, want := range []string{
			"n_root  Serve the viewer",
			"  n_a  A static handler in the API",
			"  n_b  Where the files come from",
			"n_c  The image builds the viewer",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the block does not contain %q\n---\n%s", want, got)
			}
		}
	})

	t.Run("an idea that already became a task says so", func(t *testing.T) {
		// Which is what stops it being proposed for extraction a second time.
		if !strings.Contains(got, "already a task, t_1") {
			t.Errorf("n_root does not say it is already a task\n---\n%s", got)
		}
	})

	t.Run("tasks are not in the outline", func(t *testing.T) {
		// They are the plan. Listing them here would invite a set-text on a
		// task from a question about ideas.
		if strings.Contains(got, "Mount the static handler") {
			t.Errorf("a task is in the outline block\n---\n%s", got)
		}
	})

	t.Run("nothing about placement, merge or who", func(t *testing.T) {
		for _, forbidden := range []string{"position", "clock", "actor", "collapsed", "deleted"} {
			if strings.Contains(strings.ToLower(got), forbidden) {
				t.Errorf("the block mentions %q\n---\n%s", forbidden, got)
			}
		}
	})
}

func TestStateBlockPlanning(t *testing.T) {
	got := StateBlock(sampleDoc(), "tab1", ModeIsolation)

	t.Run("tasks are numbered, in order, with their status", func(t *testing.T) {
		for _, want := range []string{
			"1  t_1  done",
			"2  t_2  doing",
			"Mount the static handler",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the block does not contain %q\n---\n%s", want, got)
			}
		}
	})

	t.Run("a task names the idea it came from", func(t *testing.T) {
		if !strings.Contains(got, "from n_root  Serve the viewer") {
			t.Errorf("t_1 does not name its origin\n---\n%s", got)
		}
	})

	t.Run("an origin that is gone says so rather than going quiet", func(t *testing.T) {
		// The link back is the spine of the whole thing; a broken one is worth
		// seeing rather than rendering as no link at all.
		if !strings.Contains(got, "from n_gone, which is gone") {
			t.Errorf("t_2's missing origin is not reported\n---\n%s", got)
		}
	})

	t.Run("the outline is underneath, because that is what gets broken up", func(t *testing.T) {
		if !strings.Contains(got, "n_a  A static handler in the API") {
			t.Errorf("isolation mode has no outline in it\n---\n%s", got)
		}
	})
}

func TestStateBlockUnknownTab(t *testing.T) {
	got := StateBlock(sampleDoc(), "nope", ModeOrientation)
	if !strings.Contains(got, "nope") || !strings.Contains(got, "nothing to restructure") {
		t.Errorf("an unknown tab gives %q", got)
	}
}

func TestStateBlockEmptyTab(t *testing.T) {
	doc := Document{Tree: []ops.TreeNode{node("tab1", map[string]any{FieldText: "Work"})}}
	got := StateBlock(doc, "tab1", ModeOrientation)
	if !strings.Contains(got, "(empty)") {
		t.Errorf("an empty tab gives %q", got)
	}
}

// The budget takes whole levels, so what goes missing is always the bottom of
// the tree. A depth-first cut would leave a parent whose remaining children are
// absent, which reads as "those were deleted".
func TestStateBlockBudgetIsBreadthFirst(t *testing.T) {
	// One root with StateBudget children, each with a child of its own. The
	// grandchildren are what must be dropped.
	var children []ops.TreeNode
	for i := 0; i < StateBudget; i++ {
		id := "n" + strconv.Itoa(i)
		children = append(children, idea(id, "level one "+id, idea(id+"_deep", "level two")))
	}
	doc := Document{Tree: []ops.TreeNode{node("tab1", nil, children...)}}

	got := StateBlock(doc, "tab1", ModeOrientation)

	if strings.Contains(got, "level two") {
		t.Error("a grandchild survived a full budget of children")
	}
	if !strings.Contains(got, "level one n0") || !strings.Contains(got, "level one n"+strconv.Itoa(StateBudget-1)) {
		t.Error("the first level was not rendered whole")
	}
	if !strings.Contains(got, "more not shown") {
		t.Errorf("truncation is silent\n---\n%s", got[len(got)-200:])
	}
	// Silence about the truncation is the failure worth guarding: a model told
	// the list is partial, but not that it must stay inside it, proposes edits
	// to what it inferred.
	if !strings.Contains(got, "Do not propose anything about a node that is not listed") {
		t.Error("the truncation note does not say to stay inside the list")
	}
}

func TestStateBlockFlattensText(t *testing.T) {
	// A line is a node. Text with a newline in it would break the one
	// invariant this format has.
	doc := Document{Tree: []ops.TreeNode{node("tab1", nil, idea("n1", "first\nsecond\t\tthird"))}}
	got := StateBlock(doc, "tab1", ModeOrientation)
	if !strings.Contains(got, "n1  first second third") {
		t.Errorf("text was not flattened: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.Contains(line, "second") && !strings.Contains(line, "n1") {
			t.Errorf("a node spans two lines: %q", line)
		}
	}
}

func TestStateBlockEmptyTextIsSaid(t *testing.T) {
	doc := Document{Tree: []ops.TreeNode{node("tab1", nil, idea("n1", ""))}}
	got := StateBlock(doc, "tab1", ModeOrientation)
	if !strings.Contains(got, "n1  (no text)") {
		t.Errorf("an empty line renders as %q", got)
	}
}
