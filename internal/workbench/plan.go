package workbench

import (
	"encoding/json"
	"fmt"
	"strings"

	"dev.jevido/work/internal/agents"
)

// PlanMode is Anton's decision about how to approach a task.
type PlanMode string

const (
	// ModeSelf means Anton handles the task himself.
	ModeSelf PlanMode = "self"
	// ModeTeam means Anton splits the task between specialists.
	ModeTeam PlanMode = "team"
)

// PlanStep is one specialist's share of the work.
type PlanStep struct {
	AgentID string `json:"agentId"`
	Task    string `json:"task"`
}

// Plan is Anton's answer to "who should do this?".
type Plan struct {
	Mode   PlanMode   `json:"mode"`
	Reason string     `json:"reason"`
	Steps  []PlanStep `json:"steps"`
}

// maxSteps caps how many specialists one task can occupy. Every step is a
// Claude process, so this is a spend and concurrency limit as much as a
// design one.
const maxSteps = 4

// normalise makes a model-authored plan safe to execute: it drops unknown or
// repeated agents, drops empty tasks, caps the step count, and falls back to
// ModeSelf when nothing usable is left. The frontend and the runner can then
// trust the plan without re-checking it.
func (p *Plan) normalise(reg Registry) {
	seen := make(map[string]bool, len(p.Steps))
	kept := p.Steps[:0]

	for _, step := range p.Steps {
		id := strings.TrimSpace(step.AgentID)
		task := strings.TrimSpace(step.Task)
		if id == "" || task == "" || seen[id] {
			continue
		}
		agent, ok := reg.Get(id)
		if !ok || agent.Role == agents.RoleCoordinator {
			continue
		}
		seen[id] = true
		kept = append(kept, PlanStep{AgentID: id, Task: task})
		if len(kept) == maxSteps {
			break
		}
	}
	p.Steps = kept

	if len(p.Steps) == 0 {
		p.Mode = ModeSelf
		return
	}
	p.Mode = ModeTeam
}

// Registry is the subset of the agent registry a plan needs. Declaring it here
// keeps plan handling testable without constructing a whole workbench.
type Registry interface {
	Get(id string) (agents.Agent, bool)
	All() []agents.Agent
}

// planSchema builds the JSON schema Anton's planning turn must satisfy. The
// agent IDs are an enum drawn from the registry, so the model cannot name a
// specialist that does not exist.
func planSchema(reg Registry) (string, error) {
	ids := specialistIDs(reg)
	if len(ids) == 0 {
		return "", fmt.Errorf("workbench: no specialists to delegate to")
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"type": "string",
				"enum": []string{string(ModeSelf), string(ModeTeam)},
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "One or two sentences on why this split.",
			},
			"steps": map[string]any{
				"type":     "array",
				"maxItems": maxSteps,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agentId": map[string]any{"type": "string", "enum": ids},
						"task": map[string]any{
							"type":        "string",
							"description": "The self-contained task for this specialist.",
						},
					},
					"required": []string{"agentId", "task"},
				},
			},
		},
		"required": []string{"mode", "reason", "steps"},
	}

	out, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("workbench: build plan schema: %w", err)
	}
	return string(out), nil
}

// planPrompt describes the team to Anton and asks him to route the task. The
// roster is generated from the registry, so adding an agent changes the prompt
// without anyone editing prose.
//
// followUp marks a request that arrives mid-conversation. The routing turn
// itself is stateless, so it is told this much rather than being handed the
// history: it changes how a terse "and now the other half" should be read.
func planPrompt(reg Registry, task string, followUp bool) string {
	var b strings.Builder
	b.WriteString("Route this task.\n\nYour specialists:\n")
	for _, a := range reg.All() {
		if a.Role == agents.RoleCoordinator {
			continue
		}
		fmt.Fprintf(&b, "- %s (id: %s) — %s\n", a.Name, a.ID, strings.Join(a.Specialties, ", "))
	}
	b.WriteString("\nChoose \"self\" with no steps when the task is small, ")
	b.WriteString("general, or outside every specialty, and you should simply answer it. ")
	b.WriteString("Choose \"team\" when the task genuinely splits along their specialties, ")
	b.WriteString("and give each one a self-contained task that does not depend on ")
	b.WriteString("another specialist's answer, since they work at the same time. ")
	b.WriteString("Do not delegate for the sake of it.\n")
	if followUp {
		b.WriteString("\nThis is a follow-up in an ongoing conversation, so the ")
		b.WriteString("task may lean on what was already discussed. The agent who ")
		b.WriteString("answers it can see that history; you cannot.\n")
	}
	b.WriteString("\nThe task:\n")
	b.WriteString(task)
	return b.String()
}

// synthesisPrompt asks Anton to turn the specialists' separate answers into one.
func synthesisPrompt(task string, results []stepResult) string {
	var b strings.Builder
	b.WriteString("You delegated a task and the specialists have reported back. ")
	b.WriteString("Give the user one answer: resolve any disagreement, say what to do, ")
	b.WriteString("and do not merely summarise who said what.\n\nThe original task:\n")
	b.WriteString(task)
	b.WriteString("\n")

	for _, r := range results {
		fmt.Fprintf(&b, "\n--- %s was asked: %s\n", r.AgentName, r.Task)
		if r.Err != "" {
			fmt.Fprintf(&b, "%s failed: %s\n", r.AgentName, r.Err)
			continue
		}
		if strings.TrimSpace(r.Output) == "" {
			fmt.Fprintf(&b, "%s returned nothing.\n", r.AgentName)
			continue
		}
		fmt.Fprintf(&b, "%s replied:\n%s\n", r.AgentName, r.Output)
	}
	return b.String()
}

// stepResult is what one specialist produced, ready for synthesis.
type stepResult struct {
	AgentID   string
	AgentName string
	Task      string
	Output    string
	Err       string
}

func specialistIDs(reg Registry) []string {
	all := reg.All()
	ids := make([]string, 0, len(all))
	for _, a := range all {
		if a.Role == agents.RoleCoordinator {
			continue
		}
		ids = append(ids, a.ID)
	}
	return ids
}
