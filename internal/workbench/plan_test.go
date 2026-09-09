package workbench

import (
	"encoding/json"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
)

// noCards stands in for an empty board: no task ID is ever recognised.
func noCards(string) bool { return false }

// theseCards recognises exactly the given task IDs.
func theseCards(ids ...string) func(string) bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

func testRegistry() *agents.Registry {
	return agents.NewRegistry(
		agents.Agent{ID: "anton", Name: "Anton", Role: agents.RoleCoordinator},
		agents.Agent{ID: "jeff", Name: "Jeff", Role: agents.RoleSpecialist,
			Skillset: []string{"Go", "performance"}},
		agents.Agent{ID: "chris", Name: "Chris", Role: agents.RoleSpecialist,
			Skillset: []string{"UX", "Svelte"}},
	)
}

// TestNormaliseKeepsUsableSteps checks the happy path is left alone.
func TestNormaliseKeepsUsableSteps(t *testing.T) {
	p := Plan{Mode: ModeTeam, Steps: []PlanStep{
		{AgentID: "jeff", Task: "profile the renderer"},
		{AgentID: "chris", Task: "tidy the console"},
	}}
	p.normalise(testRegistry(), noCards)

	if p.Mode != ModeTeam {
		t.Errorf("mode = %q, want %q", p.Mode, ModeTeam)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("got %d steps, want 2: %+v", len(p.Steps), p.Steps)
	}
}

// TestNormaliseRejectsUnusableSteps is the important one: the plan is written by
// a model, so unknown agents, the coordinator delegating to himself, repeats and
// blank tasks all have to be survivable.
func TestNormaliseRejectsUnusableSteps(t *testing.T) {
	p := Plan{Mode: ModeTeam, Steps: []PlanStep{
		{AgentID: "jeff", Task: "  profile the renderer  "},
		{AgentID: "jeff", Task: "again"},
		{AgentID: "anton", Task: "delegate to myself"},
		{AgentID: "nobody", Task: "who?"},
		{AgentID: "chris", Task: "   "},
		{AgentID: "", Task: "nameless"},
	}}
	p.normalise(testRegistry(), noCards)

	if len(p.Steps) != 1 {
		t.Fatalf("got %d steps, want 1: %+v", len(p.Steps), p.Steps)
	}
	if got := p.Steps[0]; got.AgentID != "jeff" || got.Task != "profile the renderer" {
		t.Errorf("step = %+v, want jeff with a trimmed task", got)
	}
}

// TestNormaliseFallsBackToSelf covers a "team" plan with nothing to delegate,
// which must not leave the run waiting on zero specialists.
func TestNormaliseFallsBackToSelf(t *testing.T) {
	p := Plan{Mode: ModeTeam, Steps: []PlanStep{{AgentID: "ghost", Task: "x"}}}
	p.normalise(testRegistry(), noCards)

	if p.Mode != ModeSelf {
		t.Errorf("mode = %q, want %q", p.Mode, ModeSelf)
	}
	if len(p.Steps) != 0 {
		t.Errorf("got %d steps, want none", len(p.Steps))
	}
}

// TestNormalisePromotesSelfWithSteps covers the inverse: a "self" plan that
// nonetheless names specialists should honour the steps.
func TestNormalisePromotesSelfWithSteps(t *testing.T) {
	p := Plan{Mode: ModeSelf, Steps: []PlanStep{{AgentID: "chris", Task: "the console"}}}
	p.normalise(testRegistry(), noCards)

	if p.Mode != ModeTeam {
		t.Errorf("mode = %q, want %q", p.Mode, ModeTeam)
	}
}

// TestNormaliseCapsSteps guards the spend limit: every step is a Claude process.
func TestNormaliseCapsSteps(t *testing.T) {
	reg := agents.NewRegistry(
		agents.Agent{ID: "anton", Role: agents.RoleCoordinator},
		agents.Agent{ID: "a", Role: agents.RoleSpecialist},
		agents.Agent{ID: "b", Role: agents.RoleSpecialist},
		agents.Agent{ID: "c", Role: agents.RoleSpecialist},
		agents.Agent{ID: "d", Role: agents.RoleSpecialist},
		agents.Agent{ID: "e", Role: agents.RoleSpecialist},
	)
	p := Plan{Mode: ModeTeam, Steps: []PlanStep{
		{AgentID: "a", Task: "1"}, {AgentID: "b", Task: "2"},
		{AgentID: "c", Task: "3"}, {AgentID: "d", Task: "4"},
		{AgentID: "e", Task: "5"},
	}}
	p.normalise(reg, noCards)

	if len(p.Steps) != maxSteps {
		t.Errorf("got %d steps, want the cap of %d", len(p.Steps), maxSteps)
	}
}

// TestPlanSchemaEnumeratesSpecialists checks the model cannot name an agent that
// does not exist, and that the coordinator is not among the choices.
func TestPlanSchemaEnumeratesSpecialists(t *testing.T) {
	raw, err := planSchema(testRegistry())
	if err != nil {
		t.Fatalf("planSchema: %v", err)
	}

	var schema struct {
		Properties struct {
			Steps struct {
				Items struct {
					Properties struct {
						AgentID struct {
							Enum []string `json:"enum"`
						} `json:"agentId"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	got := schema.Properties.Steps.Items.Properties.AgentID.Enum
	if len(got) != 2 || got[0] != "jeff" || got[1] != "chris" {
		t.Errorf("agentId enum = %v, want [jeff chris]", got)
	}
}

// TestPlanSchemaNeedsSpecialists covers a registry with nobody to delegate to.
func TestPlanSchemaNeedsSpecialists(t *testing.T) {
	reg := agents.NewRegistry(agents.Agent{ID: "anton", Role: agents.RoleCoordinator})
	if _, err := planSchema(reg); err == nil {
		t.Fatal("planSchema succeeded with no specialists, want an error")
	}
}

// TestPlanPromptDescribesTheTeam checks the roster comes from the registry
// rather than from hand-written prose that can drift.
func TestPlanPromptDescribesTheTeam(t *testing.T) {
	got := planPrompt(testRegistry(), "make it faster", false, nil)

	for _, want := range []string{"Jeff", "id: jeff", "performance", "Chris", "id: chris", "make it faster"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "id: anton") {
		t.Error("prompt offers the coordinator as a delegate")
	}
	if strings.Contains(got, "follow-up") {
		t.Error("an opening request is described as a follow-up")
	}
}

// TestPlanPromptFlagsFollowUps checks the routing turn is told when the task
// arrives mid-conversation, since the turn itself has no history.
func TestPlanPromptFlagsFollowUps(t *testing.T) {
	got := planPrompt(testRegistry(), "now the other half", true, nil)

	if !strings.Contains(got, "follow-up") {
		t.Errorf("prompt does not mention the conversation:\n%s", got)
	}
}

// TestSynthesisPromptCarriesEveryOutcome checks a failed specialist still
// reaches synthesis, so a partial answer is possible.
func TestSynthesisPromptCarriesEveryOutcome(t *testing.T) {
	got := synthesisPrompt("ship the thing", []stepResult{
		{AgentName: "Jeff", Task: "profile it", Output: "it is the grid"},
		{AgentName: "Chris", Task: "polish it", Err: "claude exploded"},
	})

	for _, want := range []string{"ship the thing", "profile it", "it is the grid", "Chris failed: claude exploded"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
}

// TestNormaliseDropsInventedTaskIDs is the important guard on board editing:
// the plan is model-authored, so a reference to a card that does not exist must
// not silently write nowhere.
func TestNormaliseDropsInventedTaskIDs(t *testing.T) {
	p := Plan{Mode: ModeTeam, Steps: []PlanStep{
		{AgentID: "jeff", Task: "carry on", TaskID: "T2"},
		{AgentID: "chris", Task: "start fresh", TaskID: "T99"},
	}}
	p.normalise(testRegistry(), theseCards("T2"))

	if len(p.Steps) != 2 {
		t.Fatalf("got %d steps, want 2: %+v", len(p.Steps), p.Steps)
	}
	if p.Steps[0].TaskID != "T2" {
		t.Errorf("step 0 taskId = %q, want T2 kept", p.Steps[0].TaskID)
	}
	if p.Steps[1].TaskID != "" {
		t.Errorf("step 1 taskId = %q, want the invented ID dropped", p.Steps[1].TaskID)
	}
}

func TestNormaliseKeepsOnlyUsableUpdates(t *testing.T) {
	p := Plan{Mode: ModeSelf, Updates: []BoardUpdate{
		{TaskID: " T1 ", Status: board.StatusDone},
		{TaskID: "T2", AgentID: "chris"},
		{TaskID: "T3", Status: board.Status("nearly")}, // invalid status, no other change
		{TaskID: "T99", Status: board.StatusDone},      // no such card
		{TaskID: "", Status: board.StatusDone},         // no card named
		{TaskID: "T1", AgentID: "nobody"},              // unknown assignee, nothing left
	}}
	p.normalise(testRegistry(), theseCards("T1", "T2", "T3"))

	if len(p.Updates) != 2 {
		t.Fatalf("got %d updates, want 2: %+v", len(p.Updates), p.Updates)
	}
	if p.Updates[0].TaskID != "T1" || p.Updates[0].Status != board.StatusDone {
		t.Errorf("update 0 = %+v, want T1 to done with the ID trimmed", p.Updates[0])
	}
	if p.Updates[1].TaskID != "T2" || p.Updates[1].AgentID != "chris" {
		t.Errorf("update 1 = %+v, want T2 reassigned to chris", p.Updates[1])
	}
}

// TestNormaliseAllowsUpdatesWithoutSteps covers "Anton, close T3": bookkeeping
// with nothing to delegate must not be turned into a team run.
func TestNormaliseAllowsUpdatesWithoutSteps(t *testing.T) {
	p := Plan{Mode: ModeTeam, Updates: []BoardUpdate{
		{TaskID: "T1", Status: board.StatusDone},
	}}
	p.normalise(testRegistry(), theseCards("T1"))

	if p.Mode != ModeSelf {
		t.Errorf("mode = %q, want %q", p.Mode, ModeSelf)
	}
	if len(p.Updates) != 1 {
		t.Errorf("got %d updates, want the edit kept", len(p.Updates))
	}
}

// TestPlanPromptListsTheBoard checks Anton is shown the IDs the user will
// mention, since he cannot act on a task he cannot name.
func TestPlanPromptListsTheBoard(t *testing.T) {
	got := planPrompt(testRegistry(), "close T1", false, []board.Card{
		{ID: "T1", Status: board.StatusDoing, AgentID: "jeff", Title: "profile the renderer"},
	})

	for _, want := range []string{"T1", "doing", "jeff", "profile the renderer", "updates"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
}

func TestPlanPromptSaysWhenTheBoardIsEmpty(t *testing.T) {
	got := planPrompt(testRegistry(), "anything", false, nil)
	if !strings.Contains(got, "board is empty") {
		t.Errorf("prompt does not mention the empty board:\n%s", got)
	}
}
