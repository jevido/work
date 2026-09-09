package workbench

import (
	"encoding/json"
	"fmt"
	"strings"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
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
	// TaskID names an existing board card to run instead of opening a new one,
	// so "put the renderer work back on T4" continues that task rather than
	// duplicating it.
	TaskID string `json:"taskId,omitempty"`
	// Files are the paths this step owns for as long as it runs. Steps run at
	// the same time in one working tree, so this is what stops two agents
	// editing the same file: a step whose files are taken waits for them. A
	// directory or a glob claims everything under it. Empty means the step
	// needs nothing of its own, which is right for reading and answering.
	Files []string `json:"files,omitempty"`
}

// BoardUpdate is Anton editing a card without running anything: closing a task
// you say is finished, renaming one, moving it to someone else.
type BoardUpdate struct {
	TaskID  string       `json:"taskId"`
	Status  board.Status `json:"status,omitempty"`
	Title   string       `json:"title,omitempty"`
	AgentID string       `json:"agentId,omitempty"`
}

// Plan is Anton's answer to "who should do this?", plus any bookkeeping he
// wants to do on the board while he is there.
type Plan struct {
	Mode    PlanMode      `json:"mode"`
	Reason  string        `json:"reason"`
	Steps   []PlanStep    `json:"steps"`
	Updates []BoardUpdate `json:"updates"`
}

// maxSteps caps how many specialists one task can occupy. Every step is a
// Claude process, so this is a spend and concurrency limit as much as a
// design one.
const maxSteps = 4

// normalise makes a model-authored plan safe to execute: it drops unknown or
// repeated agents, drops empty tasks, caps the step count, and falls back to
// ModeSelf when nothing usable is left. The frontend and the runner can then
// trust the plan without re-checking it.
func (p *Plan) normalise(reg Registry, known func(taskID string) bool) {
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
		taskID := strings.TrimSpace(step.TaskID)
		// A card Anton invented does not exist, so drop the reference and let
		// the step open a fresh card instead of silently writing nowhere.
		if taskID != "" && (known == nil || !known(taskID)) {
			taskID = ""
		}
		seen[id] = true
		kept = append(kept, PlanStep{
			AgentID: id,
			Task:    task,
			TaskID:  taskID,
			// Overlapping claims are not dropped here. Two steps that both
			// want a file are still both wanted work; delegate runs them one
			// after the other instead of throwing one away.
			Files: cleanPaths(step.Files),
		})
		if len(kept) == maxSteps {
			break
		}
	}
	p.Steps = kept

	updates := p.Updates[:0]
	for _, u := range p.Updates {
		u.TaskID = strings.TrimSpace(u.TaskID)
		u.Title = strings.TrimSpace(u.Title)
		u.AgentID = strings.TrimSpace(u.AgentID)
		if u.TaskID == "" || known == nil || !known(u.TaskID) {
			continue
		}
		if u.AgentID != "" {
			if _, ok := reg.Get(u.AgentID); !ok {
				u.AgentID = ""
			}
		}
		if u.Status != "" && !u.Status.Valid() {
			u.Status = ""
		}
		// An update that changes nothing is not worth carrying.
		if u.Status == "" && u.Title == "" && u.AgentID == "" {
			continue
		}
		updates = append(updates, u)
	}
	p.Updates = updates

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
						"taskId": map[string]any{
							"type": "string",
							"description": "An existing board task this continues, " +
								"e.g. T3. Omit to open a new task.",
						},
						"files": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
							"description": "Repo-relative paths this step will edit, " +
								"which it owns while it runs. A directory or a glob " +
								"claims everything under it. No two steps may name " +
								"the same file. Empty only if the step edits nothing.",
						},
					},
					"required": []string{"agentId", "task", "files"},
				},
			},
			"updates": map[string]any{
				"type": "array",
				"description": "Edits to existing board tasks that need no work " +
					"run: closing one, renaming one, reassigning one.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"taskId": map[string]any{
							"type":        "string",
							"description": "The task to change, e.g. T3.",
						},
						"status": map[string]any{
							"type": "string",
							"enum": []string{
								string(board.StatusTodo), string(board.StatusDoing),
								string(board.StatusDone), string(board.StatusBlocked),
							},
						},
						"title":   map[string]any{"type": "string"},
						"agentId": map[string]any{"type": "string", "enum": ids},
					},
					"required": []string{"taskId"},
				},
			},
		},
		"required": []string{"mode", "reason", "steps", "updates"},
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
func planPrompt(reg Registry, task string, followUp bool, cards []board.Card) string {
	var b strings.Builder
	b.WriteString("Route this task.\n\nYour specialists:\n")
	for _, a := range reg.All() {
		if a.Role == agents.RoleCoordinator {
			continue
		}
		fmt.Fprintf(&b, "- %s (id: %s) — %s\n", a.Name, a.ID, a.Blurb())
	}
	b.WriteString("\nChoose \"self\" with no steps when the task is small, ")
	b.WriteString("general, or outside every specialty, and you should simply answer it. ")
	b.WriteString("Choose \"team\" when the task genuinely splits along their specialties, ")
	b.WriteString("and give each one a self-contained task that does not depend on ")
	b.WriteString("another specialist's answer, since they work at the same time. ")
	b.WriteString("Do not delegate for the sake of it.\n")

	b.WriteString("\nThey share one working tree and they work at the same time, ")
	b.WriteString("so split the task by file, not only by topic. Give every step a ")
	b.WriteString("\"files\" list naming the paths it will edit, and do not let two ")
	b.WriteString("steps name the same file, the same directory, or overlapping ")
	b.WriteString("globs. Prefer whole files or whole directories over guesses at ")
	b.WriteString("which lines somebody needs.\n")
	b.WriteString("When the work cannot be cut along file lines -- two halves of one ")
	b.WriteString("file, or a rename that reaches everywhere -- give the whole of it ")
	b.WriteString("to one specialist rather than splitting it. Two agents in one ")
	b.WriteString("file is worse than one agent doing more.\n")
	b.WriteString("A step that only reads or only answers can leave \"files\" empty. ")
	b.WriteString("A step whose files are taken waits for them and its card shows ")
	b.WriteString("as blocked until they are free, so an overlap you leave in costs ")
	b.WriteString("the run wall-clock time rather than losing an edit.\n")

	b.WriteString("\nYou own the task board. It is the user's window into what ")
	b.WriteString("you have assigned, and they refer to tasks by ID.\n")
	if len(cards) == 0 {
		b.WriteString("The board is empty.\n")
	} else {
		b.WriteString("The board:\n")
		for _, c := range cards {
			fmt.Fprintf(&b, "- %s [%s] %s: %s\n", c.ID, c.Status, c.AgentID, c.Title)
		}
		b.WriteString("\nUse \"updates\" to close, rename or reassign a task the ")
		b.WriteString("user mentions, and give a step a \"taskId\" when it ")
		b.WriteString("continues one of these rather than starting something new. ")
		b.WriteString("Only touch a task that already exists above.\n")
	}

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
		if r.Waited != "" {
			fmt.Fprintf(&b,
				"%s had to queue behind %s for the files, so they ran one after "+
					"the other rather than at the same time.\n",
				r.AgentName, r.Waited)
		}
		if r.BlockedBy != "" {
			fmt.Fprintf(&b,
				"%s reported being blocked by %s on %s.\n",
				r.AgentName, r.BlockedBy, strings.Join(r.BlockedOn, ", "))
			if r.Retried {
				fmt.Fprintf(&b,
					"The files came free and %s was given the work again; "+
						"what follows is that second attempt.\n", r.AgentName)
			} else {
				fmt.Fprintf(&b,
					"%s did not get another run at it, so this share of the "+
						"task is unfinished and you should say so.\n", r.AgentName)
			}
		}
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
	// Waited names the agent this step queued behind before it could start,
	// because its files were already taken.
	Waited string
	// BlockedBy names the agent this step stopped for after it had started:
	// the specialist reported it could not finish because somebody else was
	// in a file it needed. BlockedOn are the files it named.
	BlockedBy string
	BlockedOn []string
	// Retried marks a step that was blocked, waited, and ran again. Its Output
	// is the second attempt.
	Retried bool
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

// blockedMarker is the line a specialist ends its reply with when it cannot
// finish because somebody else is in a file it needs.
//
// A specialist's turn is prose, not schema-constrained like Anton's, so a
// sentinel line is the only channel it has. It is read from the end of the
// reply and only the paths are trusted: who holds them is answered by the
// claims table, which knows, rather than by the agent, which is guessing.
const blockedMarker = "BLOCKED:"

// maxBlockedRetries is how many times one step may report itself blocked,
// wait, and run again. One is enough for the case this exists for -- a file
// held by a colleague who is about to finish -- and it bounds a pair of agents
// who would otherwise take turns blocking each other for the whole run.
const maxBlockedRetries = 1

// stepPrompt is the task as the specialist receives it: their share of the
// work, the files that are theirs while they run, and the way out if they find
// somebody else in one.
//
// held lists what other agents are holding right now. It is a snapshot taken
// as the step starts, so it is advice rather than a guarantee -- which is
// exactly why the marker exists as well.
func stepPrompt(task string, files, held []string) string {
	var b strings.Builder
	b.WriteString(task)

	if len(files) > 0 {
		b.WriteString("\n\nThese files are yours for this task, and nobody else ")
		b.WriteString("is in them:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\nOther specialists are working in this tree at the same ")
		b.WriteString("time, so do not edit anything outside that list. If the ")
		b.WriteString("task turns out to need a file that is not yours, say so ")
		b.WriteString("rather than taking it.\n")
	} else {
		b.WriteString("\n\nNo files are reserved for you on this task. Other ")
		b.WriteString("specialists are editing this tree right now, so read ")
		b.WriteString("freely but do not write without saying which file you need.\n")
	}

	if len(held) > 0 {
		b.WriteString("\nHeld by other specialists right now:\n")
		for _, f := range held {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	b.WriteString("\nIf you cannot finish because a file you need is held by ")
	b.WriteString("someone else, stop there. Report what you did get done, then ")
	b.WriteString("end your reply with one final line naming only the paths you ")
	b.WriteString("are waiting on:\n\n    ")
	b.WriteString(blockedMarker)
	b.WriteString(" path/one.go path/two.ts\n\n")
	b.WriteString("Anton watches for that line. He will hold your task until the ")
	b.WriteString("files are free and then hand it back to you, so stopping is ")
	b.WriteString("cheaper than working around it. Do not use that line for ")
	b.WriteString("anything else: not for a question, not for a missing file, ")
	b.WriteString("not for work you simply chose not to do.\n")
	return b.String()
}

// retryPrompt hands a blocked step back once its files are free.
func retryPrompt(task string, files, freed []string) string {
	var b strings.Builder
	b.WriteString("You stopped this task because these files were held by ")
	b.WriteString("another specialist:\n")
	for _, f := range freed {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	b.WriteString("\nThey are free now and they are yours. Pick the task back up ")
	b.WriteString("from where you stopped and finish it. Re-read the files before ")
	b.WriteString("you edit them: somebody else has been in them since you looked, ")
	b.WriteString("so what you remember of them is out of date.\n\n")
	b.WriteString("The task, again:\n")
	b.WriteString(stepPrompt(task, files, nil))
	return b.String()
}

// blockedPaths reads a specialist's reply for the blocked marker and returns
// the paths it named. Nothing found means the step ran to a normal end.
//
// Only the last marker in the reply counts, and only when it is the last
// non-empty line: an agent explaining the convention mid-answer, or quoting a
// previous turn, is not reporting itself blocked.
func blockedPaths(output string) []string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		line = strings.Trim(line, "`*_ ")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(line), blockedMarker) {
			return nil
		}
		rest := strings.TrimSpace(line[len(blockedMarker):])
		if rest == "" {
			return nil
		}
		// Written as a list as often as a space-separated line.
		rest = strings.NewReplacer(",", " ", ";", " ").Replace(rest)
		return cleanPaths(strings.Fields(rest))
	}
	return nil
}
