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

	// EventChatStarted announces a question put to the coordinator on the side
	// channel, which is answered alongside a run rather than instead of it.
	EventChatStarted = "chat:started"
	// EventChatFinished closes a side-channel answer, successfully or not.
	EventChatFinished = "chat:finished"

	// EventBoardUpdated carries the whole task board after any change. The
	// board is small and changes a handful of times per run, so publishing a
	// snapshot is cheaper than reconciling deltas and cannot drift.
	EventBoardUpdated = "board:updated"

	// EventWorkspaceChanged announces that the workspace itself changed:
	// joined, created, left, or its tabs edited. It carries the whole
	// workspace for the same reason board:updated carries the whole board --
	// it is tiny and it changes rarely.
	EventWorkspaceChanged = "workspace:changed"
	// EventWorkspaceSync reports where the second stage stands: online,
	// syncing, offline or rejected, and how much is queued. This is the
	// offline indicator's only source.
	//
	// It carries no document. Sync state changes on every poll and the
	// document changes when someone edits something, so putting a merged tree
	// on this payload would re-encode the workspace every few seconds to say
	// that nothing happened. The frontend calls WorkspaceDocument when the
	// cursor on this event moves.
	EventWorkspaceSync = "workspace:sync"
	// EventWorkspaceConflict reports a write of this machine's that is no
	// longer in the document: a field a newer edit replaced, or one aimed at a
	// line that has since been deleted.
	//
	// Emitted only for writes this replica made. A field somebody else
	// overwrote that this machine never touched is not a loss, it is the
	// document moving, and reporting it would produce a note every time
	// anybody typed anything.
	EventWorkspaceConflict = "workspace:conflict"
)

// The workspace events above are new names rather than new fields on the
// existing payloads, and that is deliberate. Work with no workspace joined
// must behave exactly as it did before this existed -- so the events it
// already emits are untouched, byte for byte, and everything the second stage
// adds arrives under a name the old frontend never listens for.

// Not every event the frontend listens for is declared here: update:available
// belongs to internal/update, next to the code that decides when to send it,
// as any later package's events should. This package is where the run's own
// events live, not a registry of all of them. The list the frontend needs is
// in frontend/src/lib/bridge/events.ts.

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
	// PhaseChat is the coordinator answering on the side channel, while a run
	// is in flight. It is grouped separately in the console because it is not
	// part of the work: it is a question about it.
	PhaseChat Phase = "chat"
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

// WorkspaceEvent is the payload for workspace:changed.
type WorkspaceEvent struct {
	// Workspace is the workspace as this machine now sees it, or nil after
	// leaving one.
	Workspace *WorkspaceView `json:"workspace"`
	// Status is where sync stands, so a join does not need a second round
	// trip to draw the indicator.
	Status Status `json:"status"`
}

// SyncEvent is the payload for workspace:sync.
type SyncEvent struct {
	Status Status `json:"status"`
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

// ConflictEvent is one write of this machine's that did not survive.
//
// It names a node and a field and never an actor. The merge needs an actor as a
// tiebreak and the transport needs one to tell its own ops from everyone
// else's; neither reason reaches here. A note that could say who would be a
// note somebody writes "changed by …" into, in a workspace whose design is that
// nobody can be named -- so the payload simply has no room for it.
type ConflictEvent struct {
	// Node is the line this happened to.
	Node string `json:"node"`
	// Field is which field lost. Empty when the node was deleted, because then
	// it is not one field that is gone.
	Field string `json:"field,omitempty"`
	// Yours is the value this machine wrote, rendered as text. This is what an
	// undo puts back and what the note quotes.
	Yours string `json:"yours"`
	// Now is what the document says instead. Empty when the node is deleted.
	Now string `json:"now,omitempty"`
	// Deleted is true when the line is gone rather than changed. A delete is
	// permanent by design, so there is nothing to undo -- only the text to
	// keep, which is why Yours is still here.
	Deleted bool `json:"deleted,omitempty"`
}
