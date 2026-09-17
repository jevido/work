package workbench

import (
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
)

func TestRestructureRefusals(t *testing.T) {
	// The guards, which are what stop a run being spawned to answer a question
	// that cannot be answered. Each of these costs nothing here and a minute
	// of somebody's time if it is let through.
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())

	t.Run("an empty request", func(t *testing.T) {
		if _, err := w.Restructure("tab1", ModeIdea, "   "); err == nil {
			t.Error("an empty request was accepted")
		}
	})

	t.Run("work mode has nothing to restructure", func(t *testing.T) {
		_, err := w.Restructure("tab1", ModeWork, "tidy this up")
		if err == nil {
			t.Fatal("work mode was accepted")
		}
		if !strings.Contains(err.Error(), ModeWork) {
			t.Errorf("error = %q, want it to name the mode", err)
		}
	})

	t.Run("a mode that is not a mode", func(t *testing.T) {
		if _, err := w.Restructure("tab1", "sideways", "tidy this up"); err == nil {
			t.Error("an unknown mode was accepted")
		}
	})
}

func TestProposalPrompt(t *testing.T) {
	state := StateBlock(sampleDoc(), "tab1", ModeIdea)

	t.Run("idea mode", func(t *testing.T) {
		got := proposalPrompt(&proposalRun{state: state, mode: ModeIdea}, "group these by area")

		// The request, the state, and the instruction that ties them together.
		for _, want := range []string{
			"board of ideas",
			// The shape the idea view draws, which is what the operations are
			// for: a proposal written against a flat outline builds one.
			"cluster head",
			// When to start over and when not to. A model told it may replace
			// the board and not told when will, on a request to tidy a branch.
			"starts with replace",
			"propose_restructure",
			"exactly once",
			"Name only the ids below",
			"n_a  A static handler in the API",
			"group these by area",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the prompt does not contain %q", want)
			}
		}

		// Prose instead of a tool call is unreviewable and unappliable, and it
		// is the obvious thing for a model to do if nobody says otherwise.
		if !strings.Contains(got, "prose cannot be reviewed") {
			t.Error("the prompt does not rule out answering in prose")
		}
	})

	t.Run("planning mode says what a plan is", func(t *testing.T) {
		planning := StateBlock(sampleDoc(), "tab1", ModePlanning)
		got := proposalPrompt(&proposalRun{state: planning, mode: ModePlanning}, "break this up")
		if !strings.Contains(got, "ordered list of tasks") {
			t.Errorf("planning mode is not described\n---\n%s", got[:200])
		}
		// It needs the ideas as well as the tasks, or "break this into tasks"
		// has nothing to break up.
		if !strings.Contains(got, "n_a  A static handler in the API") {
			t.Error("the planning prompt has no outline in it")
		}
	})

	t.Run("the request is last", func(t *testing.T) {
		// After the state, so the thing being asked is the freshest thing in
		// the prompt rather than a line buried above four hundred nodes.
		got := proposalPrompt(&proposalRun{state: state, mode: ModeIdea}, "UNIQUEMARKER")
		if !strings.HasSuffix(strings.TrimSpace(got), "UNIQUEMARKER") {
			t.Error("the request is not the last thing in the prompt")
		}
	})
}
