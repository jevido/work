package workbench

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

// Task is a unit of work handed to an agent.
type Task struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	AgentID  string `json:"agentId"`
	Assigned bool   `json:"assigned"`
}

// AgentStatus is the frontend-visible state of one agent.
type AgentStatus struct {
	agents.Agent
	State  AgentState `json:"state"`
	TaskID string     `json:"taskId,omitempty"`
}

// Workbench coordinates agents and their Claude processes.
//
// Only one task per agent runs at a time. That is a deliberate limit for the
// first milestone: it keeps the state machine and the visualisation honest.
type Workbench struct {
	registry *agents.Registry
	runner   *claude.Runner
	emit     Emitter
	workDir  string

	nextID atomic.Uint64

	mu      sync.Mutex
	state   map[string]AgentState
	current map[string]string             // agentID -> taskID
	cancels map[string]context.CancelFunc // taskID -> cancel
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
		cancels:  make(map[string]context.CancelFunc),
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

// Submit gives a prompt to an agent and starts it.
//
// agentID may be empty, in which case the coordinator takes it. This is where
// Anton will eventually decide the team; for now the coordinator does the work
// himself and delegation is explicit.
func (w *Workbench) Submit(agentID, prompt string) (Task, error) {
	if prompt == "" {
		return Task{}, errors.New("workbench: empty prompt")
	}
	if agentID == "" {
		c, ok := w.registry.Coordinator()
		if !ok {
			return Task{}, errors.New("workbench: no coordinator configured")
		}
		agentID = c.ID
	}
	agent, ok := w.registry.Get(agentID)
	if !ok {
		return Task{}, fmt.Errorf("workbench: unknown agent %q", agentID)
	}
	if _, err := w.runner.Available(); err != nil {
		return Task{}, fmt.Errorf("workbench: local claude CLI not found on PATH: %w", err)
	}

	taskID := "t" + strconv.FormatUint(w.nextID.Add(1), 10)
	ctx, cancel := context.WithCancel(context.Background())

	w.mu.Lock()
	if s := w.state[agentID]; s == StateAssigned || s == StateWorking {
		w.mu.Unlock()
		cancel()
		return Task{}, fmt.Errorf("workbench: %s is already busy", agent.Name)
	}
	w.state[agentID] = StateAssigned
	w.current[agentID] = taskID
	w.cancels[taskID] = cancel
	w.mu.Unlock()

	w.emitAgent(EventAgentAssigned, agentID, taskID, StateAssigned, "")

	go w.run(ctx, agent, taskID, prompt)

	return Task{ID: taskID, Prompt: prompt, AgentID: agentID, Assigned: true}, nil
}

// Cancel stops a running task. Cancelling an unknown or finished task is not an
// error: the caller's intent is already satisfied.
func (w *Workbench) Cancel(taskID string) error {
	w.mu.Lock()
	cancel := w.cancels[taskID]
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// run executes one task and translates Claude's stream into semantic events.
func (w *Workbench) run(ctx context.Context, agent agents.Agent, taskID, prompt string) {
	working := false
	finishedCleanly := false

	err := w.runner.Run(ctx, claude.Request{
		Prompt:             prompt,
		Model:              agent.Model,
		AppendSystemPrompt: agent.SystemPrompt,
		WorkDir:            w.workDir,
		AllowedTools:       agent.AllowedTools,
	}, func(e claude.Event) {
		// The first sign of real output promotes the agent from assigned to
		// working. The frontend uses this to seat them at the desk.
		if !working && e.Kind != claude.KindError {
			working = true
			w.setState(agent.ID, StateWorking)
			w.emitAgent(EventAgentWorking, agent.ID, taskID, StateWorking, "")
		}

		switch e.Kind {
		case claude.KindSession:
			w.emit(EventClaudeSession, ClaudeEvent{
				TaskID: taskID, AgentID: agent.ID,
				SessionID: e.SessionID, Model: e.Model,
			})
		case claude.KindText:
			w.emit(EventClaudeText, ClaudeEvent{TaskID: taskID, AgentID: agent.ID, Text: e.Text})
		case claude.KindThinking:
			w.emit(EventClaudeThinking, ClaudeEvent{TaskID: taskID, AgentID: agent.ID, Text: e.Text})
		case claude.KindToolUse:
			w.emit(EventClaudeTool, ClaudeEvent{TaskID: taskID, AgentID: agent.ID, ToolName: e.ToolName})
		case claude.KindResult:
			finishedCleanly = true
			w.emit(EventClaudeResult, ClaudeEvent{
				TaskID: taskID, AgentID: agent.ID,
				Text: e.Result, CostUSD: e.CostUSD, DurationMS: e.DurationMS,
			})
		case claude.KindError:
			w.emit(EventClaudeError, ClaudeEvent{TaskID: taskID, AgentID: agent.ID, Message: e.Message})
		}
	})

	w.mu.Lock()
	delete(w.cancels, taskID)
	delete(w.current, agent.ID)
	w.mu.Unlock()

	switch {
	case errors.Is(err, context.Canceled):
		w.emit(EventClaudeCancelled, ClaudeEvent{TaskID: taskID, AgentID: agent.ID})
		w.emitAgent(EventAgentFinished, agent.ID, taskID, StateIdle, cancelledMessage)
	case err != nil:
		w.emit(EventClaudeError, ClaudeEvent{
			TaskID: taskID, AgentID: agent.ID, Message: err.Error(),
		})
		w.emitAgent(EventAgentError, agent.ID, taskID, StateError, err.Error())
	default:
		msg := ""
		if !finishedCleanly {
			msg = "claude exited without a result"
		}
		w.emitAgent(EventAgentFinished, agent.ID, taskID, StateFinished, msg)
	}

	// The stored state settles back to idle regardless of outcome: "finished"
	// and "error" are transitions the frontend animates, not resting states.
	// Anything reading Agents() later should see an agent ready for work.
	w.setState(agent.ID, StateIdle)
}

func (w *Workbench) setState(agentID string, s AgentState) {
	w.mu.Lock()
	w.state[agentID] = s
	w.mu.Unlock()
}

func (w *Workbench) emitAgent(name, agentID, taskID string, s AgentState, msg string) {
	w.emit(name, AgentEvent{AgentID: agentID, TaskID: taskID, State: s, Message: msg})
}
