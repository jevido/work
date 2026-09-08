// Package workbench owns runtime state: which agents exist, what they are
// doing, and which Claude processes are alive.
//
// It is the only place that knows about both agents and Claude, and it owns
// delegation: routing a request, running the specialists, and bringing their
// answers back together. The frontend never learns positions or animation state
// from here; it only receives the semantic events below and animates locally.
package workbench

import (
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/changes"
)

// Event names emitted to the frontend. Keep these in sync with
// frontend/src/lib/bridge/events.ts.
const (
	// EventAgentAssigned means an agent has been given a task but has not
	// started yet. The renderer walks them to their desk.
	EventAgentAssigned = "agent:assigned"
	// EventAgentWorking means the agent is actively producing output.
	EventAgentWorking = "agent:working"
	// EventAgentFinished means the agent completed its task.
	EventAgentFinished = "agent:finished"
	// EventAgentError means the agent's task failed.
	EventAgentError = "agent:error"

	// EventClaudeSession carries the Claude session ID and model for a task.
	EventClaudeSession = "claude:session"
	// EventClaudeText carries a chunk of streamed assistant text.
	EventClaudeText = "claude:text"
	// EventClaudeThinking carries a chunk of streamed thinking text.
	EventClaudeThinking = "claude:thinking"
	// EventClaudeTool announces a tool call, with the arguments the agent chose.
	EventClaudeTool = "claude:tool"
	// EventClaudeToolResult carries what a tool call returned.
	EventClaudeToolResult = "claude:tool-result"
	// EventClaudeResult carries the final result of a task.
	EventClaudeResult = "claude:result"
	// EventClaudeError carries a failure message.
	EventClaudeError = "claude:error"
	// EventClaudeCancelled reports that the user stopped the task. It is
	// deliberately not an error: nothing went wrong.
	EventClaudeCancelled = "claude:cancelled"

	// EventRunStarted announces a new top-level request from the user.
	EventRunStarted = "run:started"
	// EventRunPlan carries Anton's routing decision for a run.
	EventRunPlan = "run:plan"
	// EventRunFinished closes a run, successfully or not.
	EventRunFinished = "run:finished"
	// EventRunChanges lists the files a run touched, with their diffs, so the
	// work can be reviewed and reverted.
	EventRunChanges = "run:changes"

	// EventBoardUpdated carries the whole task board after any change. The
	// board is small and changes a handful of times per run, so publishing a
	// snapshot is cheaper than reconciling deltas and cannot drift.
	EventBoardUpdated = "board:updated"
)

// Phase says which part of a run a task belongs to. The console groups output
// by phase so a delegated run reads as a sequence rather than an interleaved
// mess.
type Phase string

const (
	// PhasePlan is Anton deciding who should do the work.
	PhasePlan Phase = "plan"
	// PhaseWork is an agent doing its share.
	PhaseWork Phase = "work"
	// PhaseSynthesis is Anton turning the specialists' answers into one.
	PhaseSynthesis Phase = "synthesis"
)

// AgentState is the coarse-grained lifecycle state of an agent. The renderer
// maps each of these onto a visual behaviour; it invents nothing else.
type AgentState string

const (
	StateIdle     AgentState = "idle"
	StateAssigned AgentState = "assigned"
	StateWorking  AgentState = "working"
	StateFinished AgentState = "finished"
	StateError    AgentState = "error"
)

// AgentEvent is the payload for every agent:* event.
type AgentEvent struct {
	AgentID string     `json:"agentId"`
	TaskID  string     `json:"taskId"`
	RunID   string     `json:"runId"`
	Phase   Phase      `json:"phase"`
	State   AgentState `json:"state"`
	Message string     `json:"message,omitempty"`
}

// ClaudeEvent is the payload for every claude:* event.
type ClaudeEvent struct {
	TaskID    string `json:"taskId"`
	RunID     string `json:"runId"`
	Phase     Phase  `json:"phase"`
	AgentID   string `json:"agentId"`
	Text      string `json:"text,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Model     string `json:"model,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	// ToolID pairs a tool call with its result.
	ToolID string `json:"toolId,omitempty"`
	// ToolInput is the tool's arguments as JSON text, so the frontend can show
	// what an agent actually asked for -- which file, which command.
	ToolInput string `json:"toolInput,omitempty"`
	// ToolResult is the tool's output, already truncated.
	ToolResult string `json:"toolResult,omitempty"`
	// ToolFailed marks a rejected or failed tool call.
	ToolFailed bool    `json:"toolFailed,omitempty"`
	Message    string  `json:"message,omitempty"`
	CostUSD    float64 `json:"costUsd,omitempty"`
	// DurationMS is the wall-clock duration reported by the Claude CLI.
	DurationMS int64 `json:"durationMs,omitempty"`
}

// RunEvent is the payload for every run:* event.
type RunEvent struct {
	RunID string `json:"runId"`
	// AgentID is the coordinator whose decision EventRunPlan carries.
	AgentID string `json:"agentId,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	// Message carries a failure reason on EventRunFinished, or is empty on a
	// clean finish.
	Message string `json:"message,omitempty"`
	// Cancelled marks a run the user stopped.
	Cancelled bool `json:"cancelled,omitempty"`
	// Mode, Reason, Steps and Updates are set on EventRunPlan.
	Mode    PlanMode      `json:"mode,omitempty"`
	Reason  string        `json:"reason,omitempty"`
	Steps   []PlanStep    `json:"steps,omitempty"`
	Updates []BoardUpdate `json:"updates,omitempty"`
}

// BoardEvent is the payload for board:updated.
type BoardEvent struct {
	Cards []board.Card `json:"cards"`
}

// ChangesEvent is the payload for run:changes.
type ChangesEvent struct {
	RunID string `json:"runId"`
	// Changes is empty when the run touched nothing, or when the working
	// directory is not a git repository.
	Changes []changes.Change `json:"changes"`
	// Tracked is false when Work cannot tell what changed, because the working
	// directory is not a git working tree.
	Tracked bool `json:"tracked"`
}
