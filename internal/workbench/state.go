package workbench

import (
	"fmt"
	"strings"

	"dev.jevido/work/internal/ops"
)

// The three modes, as the frontend names them. They are here because a
// restructuring is asked for in one of them and the answer differs, so the
// string crosses the bridge and a typo on either side has to be a compile
// error on this one.
const (
	ModeIdea     = "idea"
	ModePlanning = "planning"
	ModeWork     = "work"
)

// StateBudget is how many nodes one state block will name.
//
// A cap rather than a guess at a token limit: this is read by a model with a
// context window that changes between releases, and the failure it guards
// against is not "too long" but "silently partial". Everything past the budget
// is said to have been dropped, and the model is told not to touch what it
// cannot see.
const StateBudget = 400

// stateTextLen is how much of one line is shown. A restructuring is about where
// lines sit, not about their prose, and a paragraph pasted into an outline row
// would otherwise eat the budget on its own.
const stateTextLen = 160

// StateBlock renders a tab's live nodes for a model to reason about.
//
// mode is [ModeIdea] or [ModePlanning] and changes what is rendered, not how.
// Two renderings rather than one, because the two questions are different:
// "reorganise this branch" needs the tree, and "break this into tasks" needs to
// know which tasks already exist or it proposes the same extraction twice.
//
// What is deliberately absent is position. Claude says structure; the app
// decides placement, the same way the office is told who is doing what and
// never where to stand. A sort key handed to a model comes back as a sort key
// computed against a document that has since moved, and the result is not an
// error -- it is a line quietly in the wrong place. So a proposal names a
// neighbour instead, and this block gives it neighbours to name: the order of
// the lines here is the order they sit in.
//
// Also absent: clocks, actors, tombstones and collapse state. A clock or an
// actor is a merge detail and an invitation to reason about who did what, in a
// workspace whose whole design is that nobody can. A tombstone is a node that
// is gone, and offering one up is offering a node to edit that cannot be
// edited. Collapse is what somebody's screen looks like, not what is true.
func StateBlock(doc Document, tab, mode string) string {
	var root *ops.TreeNode
	for i := range doc.Tree {
		if doc.Tree[i].ID == tab {
			root = &doc.Tree[i]
			break
		}
	}
	if root == nil {
		return "The workspace has no tab " + tab + ", so there is nothing to restructure."
	}

	if mode == ModePlanning {
		return planBlock(doc, *root, tab)
	}
	return outlineBlock(*root)
}

// outlineBlock is the tree, indented, ids first.
//
// Flat text rather than JSON. It is read by a model, the indentation is the
// parent link, and a thousand-node outline rendered as JSON is mostly
// punctuation -- punctuation that costs context and carries nothing the shape
// of the text does not already say.
func outlineBlock(root ops.TreeNode) string {
	return "The outline as it stands. The id is on the left; it is what an operation names.\n\n" + outlineBody(root)
}

// outlineBody is the lines alone, so planning mode can introduce them in its
// own words rather than repeating a sentence the reader has just read.
func outlineBody(root ops.TreeNode) string {
	keep, dropped := budget(root, func(n ops.TreeNode) bool { return !isTask(n.Node) })

	var b strings.Builder
	render(&b, root.Children, 0, keep, func(b *strings.Builder, n ops.TreeNode, depth int) {
		fmt.Fprintf(b, "%s%s  %s", strings.Repeat("  ", depth), n.ID, oneLine(fieldString(n.Node, FieldText)))
		// "This idea already became a task" is exactly what stops it being
		// proposed for extraction a second time.
		if task := fieldString(n.Node, ops.FieldTaskID); task != "" {
			fmt.Fprintf(b, "  -> already a task, %s", task)
		}
		b.WriteString("\n")
	})

	if root.Children == nil {
		b.WriteString("(empty)\n")
	}
	writeDropped(&b, dropped)
	return b.String()
}

// planBlock is the tasks, in order, each with the idea it came from.
func planBlock(doc Document, root ops.TreeNode, tab string) string {
	tasks := tabTasks(doc, tab)

	// The ideas, by id, so a task can name the one it came from in words
	// rather than only by id. A task whose origin was deleted says so: the
	// link is the spine of the whole thing and a broken one is worth seeing.
	text := map[string]string{}
	var index func([]ops.TreeNode)
	index = func(nodes []ops.TreeNode) {
		for _, n := range nodes {
			text[n.ID] = oneLine(fieldString(n.Node, FieldText))
			index(n.Children)
		}
	}
	index(root.Children)

	var b strings.Builder
	b.WriteString("The plan as it stands, in order. The id is what an operation names.\n\n")
	if len(tasks) == 0 {
		b.WriteString("(no tasks yet)\n")
	}

	shown := tasks
	dropped := 0
	if len(shown) > StateBudget {
		dropped = len(shown) - StateBudget
		shown = shown[:StateBudget]
	}

	for i, task := range shown {
		status := fieldString(task, FieldStatus)
		if status == "" {
			status = TaskTodo
		}
		fmt.Fprintf(&b, "%d  %s  %-5s  %s", i+1, task.ID, status, oneLine(fieldString(task, FieldText)))
		if from := fieldString(task, ops.FieldExtractedFrom); from != "" {
			if idea, alive := text[from]; alive {
				fmt.Fprintf(&b, "  <- from %s  %s", from, idea)
			} else {
				fmt.Fprintf(&b, "  <- from %s, which is gone", from)
			}
		}
		b.WriteString("\n")
	}

	writeDropped(&b, dropped)

	// The outline underneath the plan, because "break this region into tasks"
	// is a question about the outline that answers in tasks. Without it the
	// model has ids for tasks and none for the ideas it is meant to read.
	b.WriteString("\nThe outline those tasks came out of. The id is on the left; it is what an\noperation names.\n\n")
	b.WriteString(outlineBody(root))
	return b.String()
}

// budget decides which nodes are named, breadth-first.
//
// Breadth-first on purpose. Cutting a depth-first walk at a count stops in the
// middle of a branch, which renders a parent whose remaining children are
// simply absent -- and a model reading that has been told those were deleted.
// Taking whole levels instead means what is missing is always the bottom of
// the tree, which is what the note at the end then says.
func budget(root ops.TreeNode, include func(ops.TreeNode) bool) (map[string]bool, int) {
	keep := map[string]bool{}
	queue := append([]ops.TreeNode(nil), root.Children...)
	dropped := 0

	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if !include(n) {
			continue
		}
		if len(keep) >= StateBudget {
			dropped++
			continue
		}
		keep[n.ID] = true
		queue = append(queue, n.Children...)
	}
	return keep, dropped
}

func render(b *strings.Builder, nodes []ops.TreeNode, depth int, keep map[string]bool, line func(*strings.Builder, ops.TreeNode, int)) {
	for _, n := range nodes {
		if !keep[n.ID] {
			continue
		}
		line(b, n, depth)
		render(b, n.Children, depth+1, keep, line)
	}
}

func writeDropped(b *strings.Builder, dropped int) {
	if dropped == 0 {
		return
	}
	// Said plainly, and with the instruction attached. A model that knows the
	// list is partial but not that it must stay inside it will cheerfully
	// propose an edit to something it inferred.
	fmt.Fprintf(b, "\n… %d more not shown. Do not propose anything about a node that is not listed above.\n", dropped)
}

// oneLine flattens a field for a line-per-node rendering.
//
// A node's text is whatever somebody typed, and somebody typed a newline into
// it at some point. Left alone it would break the one invariant this format
// has, which is that a line is a node.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "(no text)"
	}
	if len(s) > stateTextLen {
		return s[:stateTextLen] + "…"
	}
	return s
}
