package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
)

// Emitter delivers a named event with a payload to the frontend. main wires
// this to the Wails event manager; tests pass a fake.
type Emitter func(name string, data any)

// cancelledMessage marks an agent:finished event that came from a cancellation
// rather than a completed task. The frontend uses it to skip the "finished"
// flourish and send the agent straight back to wandering.
const cancelledMessage = "cancelled"

// Task identifies the work started by one Submit call.
type Task struct {
	// ID is the run ID. Cancel takes this.
	ID string `json:"id"`
	// Prompt is the user's request.
	Prompt string `json:"prompt"`
	// AgentID is the agent the request went to: the coordinator for a routed
	// run, or the named agent for a direct one.
	AgentID string `json:"agentId"`
	// Routed is true when Anton will decide who does the work.
	Routed bool `json:"routed"`
}

// AgentStatus is the frontend-visible state of one agent.
type AgentStatus struct {
	agents.Agent
	State  AgentState `json:"state"`
	TaskID string     `json:"taskId,omitempty"`
}

// Workbench coordinates agents and their Claude processes.
//
// One run is active at a time. That is a deliberate limit: the console shows a
// single conversation, and a second concurrent run would make both the output
// and the office unreadable. Within a run, specialists do work in parallel.
type Workbench struct {
	registry *agents.Registry
	runner   *claude.Runner
	emit     Emitter
	workDir  string

	nextRun atomic.Uint64

	mu      sync.Mutex
	state   map[string]AgentState
	current map[string]string // agentID -> taskID
	// active is the running run, or nil. Guarded by mu.
	active *run
}

// run is one top-level request, from prompt to final answer.
type run struct {
	id     string
	prompt string
	cancel context.CancelFunc
	nextID atomic.Uint64
}

// taskID mints a stable, readable ID for one Claude call inside the run.
func (r *run) taskID(suffix string) string {
	return r.id + "." + suffix + strconv.FormatUint(r.nextID.Add(1), 10)
}

// New returns a Workbench. workDir is the directory Claude runs in.
func New(reg *agents.Registry, runner *claude.Runner, emit Emitter, workDir string) *Workbench {
	w := &Workbench{
		registry: reg,
		runner:   runner,
		emit:     emit,
		workDir:  workDir,
		state:    make(map[string]AgentState),
		current:  make(map[string]string),
	}
	for _, a := range reg.All() {
		w.state[a.ID] = StateIdle
	}
	return w
}

// Agents returns every agent with its current state.
func (w *Workbench) Agents() []AgentStatus {
	all := w.registry.All()
	out := make([]AgentStatus, 0, len(all))

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, a := range all {
		out = append(out, AgentStatus{
			Agent:  a,
			State:  w.state[a.ID],
			TaskID: w.current[a.ID],
		})
	}
	return out
}

// Submit starts a run.
//
// An empty agentID gives the task to the coordinator, who routes it: he either
// answers himself or splits it between specialists. A named agentID skips
// routing and puts that one agent straight to work.
func (w *Workbench) Submit(agentID, prompt string) (Task, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Task{}, errors.New("workbench: empty prompt")
	}
	if _, err := w.runner.Available(); err != nil {
		return Task{}, fmt.Errorf("workbench: local claude CLI not found on PATH: %w", err)
	}

	routed := agentID == ""
	var lead agents.Agent
	if routed {
		c, ok := w.registry.Coordinator()
		if !ok {
			return Task{}, errors.New("workbench: no coordinator configured")
		}
		lead = c
	} else {
		a, ok := w.registry.Get(agentID)
		if !ok {
			return Task{}, fmt.Errorf("workbench: unknown agent %q", agentID)
		}
		lead = a
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		id:     "r" + strconv.FormatUint(w.nextRun.Add(1), 10),
		prompt: prompt,
		cancel: cancel,
	}

	w.mu.Lock()
	if w.active != nil {
		w.mu.Unlock()
		cancel()
		return Task{}, errors.New("workbench: a task is already running")
	}
	w.active = r
	w.mu.Unlock()

	w.emit(EventRunStarted, RunEvent{RunID: r.id, Prompt: prompt})

	go func() {
		defer cancel()
		var err error
		if routed {
			err = w.executeRouted(ctx, r, lead)
		} else {
			_, err = w.executeStep(ctx, r, lead, PhaseWork, prompt)
		}
		w.finishRun(ctx, r, err)
	}()

	return Task{ID: r.id, Prompt: prompt, AgentID: lead.ID, Routed: routed}, nil
}

// Cancel stops the run with the given ID. Cancelling an unknown or already
// finished run is not an error: the caller's intent is already satisfied.
func (w *Workbench) Cancel(runID string) error {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active != nil && active.id == runID {
		active.cancel()
	}
	return nil
}

// executeRouted runs the full coordinator flow: plan, delegate, synthesise.
func (w *Workbench) executeRouted(ctx context.Context, r *run, lead agents.Agent) error {
	plan, err := w.plan(ctx, r, lead)
	if err != nil {
		return err
	}
	w.emit(EventRunPlan, RunEvent{
		RunID:  r.id,
		Mode:   plan.Mode,
		Reason: plan.Reason,
		Steps:  plan.Steps,
	})

	// Anton kept it: one ordinary turn on the user's own words.
	if plan.Mode == ModeSelf {
		_, err = w.executeStep(ctx, r, lead, PhaseWork, r.prompt)
		return err
	}

	results, err := w.delegate(ctx, r, plan)
	if err != nil {
		return err
	}

	_, err = w.executeStep(
		ctx, r, lead, PhaseSynthesis, synthesisPrompt(r.prompt, results))
	return err
}

// plan asks the coordinator who should do the work.
//
// The planning turn is schema-constrained, so its output is an object rather
// than prose. Its text is deliberately not streamed to the console: the parsed
// plan is emitted instead, which is the part worth reading.
func (w *Workbench) plan(ctx context.Context, r *run, lead agents.Agent) (Plan, error) {
	schema, err := planSchema(w.registry)
	if err != nil {
		return Plan{}, err
	}

	model := lead.PlanModel
	if model == "" {
		model = lead.Model
	}

	taskID := r.taskID("plan")
	w.beginTask(r, lead.ID, taskID, PhasePlan)

	var structured json.RawMessage
	err = w.runner.Run(ctx, claude.Request{
		Prompt:             planPrompt(w.registry, r.prompt),
		Model:              model,
		AppendSystemPrompt: lead.SystemPrompt,
		WorkDir:            w.workDir,
		JSONSchema:         schema,
		// Routing is a judgement call on the text of the task, not an
		// investigation, so the planning turn gets no tools. It keeps the turn
		// fast and cheap, which matters because every run pays for it.
		AllowedTools: []string{},
	}, func(e claude.Event) {
		switch e.Kind {
		case claude.KindSession:
			w.emitClaude(EventClaudeSession, r, lead.ID, taskID, PhasePlan, ClaudeEvent{
				SessionID: e.SessionID, Model: e.Model,
			})
		case claude.KindResult:
			structured = e.Structured
			w.emitClaude(EventClaudeResult, r, lead.ID, taskID, PhasePlan, ClaudeEvent{
				CostUSD: e.CostUSD, DurationMS: e.DurationMS,
			})
		case claude.KindError:
			w.emitClaude(EventClaudeError, r, lead.ID, taskID, PhasePlan, ClaudeEvent{
				Message: e.Message,
			})
		}
	})

	w.endTask(r, lead.ID, taskID, PhasePlan, err)
	if err != nil {
		return Plan{}, err
	}
	if len(structured) == 0 {
		return Plan{}, errors.New("workbench: planning turn returned no plan")
	}

	var plan Plan
	if err := json.Unmarshal(structured, &plan); err != nil {
		return Plan{}, fmt.Errorf("workbench: parse plan: %w", err)
	}
	plan.normalise(w.registry)
	return plan, nil
}

// delegate runs every step of a plan at the same time and collects the answers.
//
// A specialist failing does not fail the run: its error is recorded and handed
// to synthesis, so a partial answer still reaches the user. Only cancellation
// stops the whole run.
func (w *Workbench) delegate(ctx context.Context, r *run, plan Plan) ([]stepResult, error) {
	results := make([]stepResult, len(plan.Steps))

	var wg sync.WaitGroup
	for i, step := range plan.Steps {
		agent, ok := w.registry.Get(step.AgentID)
		if !ok {
			continue
		}
		results[i] = stepResult{AgentID: agent.ID, AgentName: agent.Name, Task: step.Task}

		wg.Add(1)
		go func(i int, agent agents.Agent, task string) {
			defer wg.Done()
			output, err := w.executeStep(ctx, r, agent, PhaseWork, task)
			results[i].Output = output
			if err != nil && !errors.Is(err, context.Canceled) {
				results[i].Err = err.Error()
			}
		}(i, agent, step.Task)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// executeStep runs one Claude call as one agent and returns its final answer.
//
// Text streams to the console as it arrives, including for specialists, so a
// delegated run can be watched rather than waited for. Only the planning turn
// stays quiet, and it does not come through here.
func (w *Workbench) executeStep(
	ctx context.Context,
	r *run,
	agent agents.Agent,
	phase Phase,
	prompt string,
) (string, error) {
	taskID := r.taskID(agent.ID)
	w.beginTask(r, agent.ID, taskID, phase)

	var out strings.Builder
	working := false

	err := w.runner.Run(ctx, claude.Request{
		Prompt:             prompt,
		Model:              agent.Model,
		AppendSystemPrompt: agent.SystemPrompt,
		WorkDir:            w.workDir,
		AllowedTools:       agent.AllowedTools,
	}, func(e claude.Event) {
		// The first sign of real output promotes the agent from assigned to
		// working. The frontend uses this to seat them at their desk.
		if !working && e.Kind != claude.KindError {
			working = true
			w.setState(agent.ID, StateWorking)
			w.emitAgent(EventAgentWorking, r, agent.ID, taskID, phase, StateWorking, "")
		}

		switch e.Kind {
		case claude.KindSession:
			w.emitClaude(EventClaudeSession, r, agent.ID, taskID, phase, ClaudeEvent{
				SessionID: e.SessionID, Model: e.Model,
			})
		case claude.KindText:
			w.emitClaude(EventClaudeText, r, agent.ID, taskID, phase, ClaudeEvent{Text: e.Text})
		case claude.KindThinking:
			w.emitClaude(EventClaudeThinking, r, agent.ID, taskID, phase, ClaudeEvent{Text: e.Text})
		case claude.KindToolUse:
			w.emitClaude(EventClaudeTool, r, agent.ID, taskID, phase, ClaudeEvent{ToolName: e.ToolName})
		case claude.KindResult:
			out.WriteString(e.Result)
			w.emitClaude(EventClaudeResult, r, agent.ID, taskID, phase, ClaudeEvent{
				Text: e.Result, CostUSD: e.CostUSD, DurationMS: e.DurationMS,
			})
		case claude.KindError:
			w.emitClaude(EventClaudeError, r, agent.ID, taskID, phase, ClaudeEvent{Message: e.Message})
		}
	})

	w.endTask(r, agent.ID, taskID, phase, err)
	return out.String(), err
}

// beginTask marks an agent as assigned and tells the office to walk them over.
func (w *Workbench) beginTask(r *run, agentID, taskID string, phase Phase) {
	w.mu.Lock()
	w.state[agentID] = StateAssigned
	w.current[agentID] = taskID
	w.mu.Unlock()
	w.emitAgent(EventAgentAssigned, r, agentID, taskID, phase, StateAssigned, "")
}

// endTask reports how a single agent's turn ended and settles their state.
func (w *Workbench) endTask(r *run, agentID, taskID string, phase Phase, err error) {
	w.mu.Lock()
	delete(w.current, agentID)
	w.mu.Unlock()

	switch {
	case errors.Is(err, context.Canceled):
		w.emitAgent(EventAgentFinished, r, agentID, taskID, phase, StateIdle, cancelledMessage)
	case err != nil:
		w.emitAgent(EventAgentError, r, agentID, taskID, phase, StateError, err.Error())
	default:
		w.emitAgent(EventAgentFinished, r, agentID, taskID, phase, StateFinished, "")
	}

	// The stored state settles back to idle regardless of outcome: "finished"
	// and "error" are transitions the frontend animates, not resting states.
	w.setState(agentID, StateIdle)
}

// finishRun closes out a run and releases the workbench for the next one.
func (w *Workbench) finishRun(ctx context.Context, r *run, err error) {
	w.mu.Lock()
	if w.active == r {
		w.active = nil
	}
	w.mu.Unlock()

	cancelled := errors.Is(err, context.Canceled) || ctx.Err() != nil
	switch {
	case cancelled:
		w.emit(EventClaudeCancelled, ClaudeEvent{RunID: r.id})
		w.emit(EventRunFinished, RunEvent{RunID: r.id, Cancelled: true})
	case err != nil:
		w.emit(EventRunFinished, RunEvent{RunID: r.id, Message: err.Error()})
	default:
		w.emit(EventRunFinished, RunEvent{RunID: r.id})
	}
}

func (w *Workbench) setState(agentID string, s AgentState) {
	w.mu.Lock()
	w.state[agentID] = s
	w.mu.Unlock()
}

func (w *Workbench) emitAgent(
	name string, r *run, agentID, taskID string, phase Phase, s AgentState, msg string,
) {
	w.emit(name, AgentEvent{
		AgentID: agentID,
		TaskID:  taskID,
		RunID:   r.id,
		Phase:   phase,
		State:   s,
		Message: msg,
	})
}

func (w *Workbench) emitClaude(
	name string, r *run, agentID, taskID string, phase Phase, e ClaudeEvent,
) {
	e.RunID = r.id
	e.TaskID = taskID
	e.AgentID = agentID
	e.Phase = phase
	w.emit(name, e)
}
