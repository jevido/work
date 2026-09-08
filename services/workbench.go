// Package services holds the thin adapters the frontend can call.
//
// Nothing here contains logic worth testing on its own; it validates arguments
// and forwards to internal/workbench.
package services

import (
	"errors"
	"strings"

	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/workbench"
)

// maxPromptBytes bounds what the frontend may send. The webview is untrusted
// input like any other client.
const maxPromptBytes = 128 << 10 // 128 KiB

// WorkbenchService exposes the agent workbench to the frontend.
type WorkbenchService struct {
	wb *workbench.Workbench
}

// NewWorkbenchService wires the service to a workbench.
func NewWorkbenchService(wb *workbench.Workbench) *WorkbenchService {
	return &WorkbenchService{wb: wb}
}

// ServiceName names the service for logs and generated bindings.
func (s *WorkbenchService) ServiceName() string { return "WorkbenchService" }

// Agents returns the team and each member's current state.
func (s *WorkbenchService) Agents() []workbench.AgentStatus {
	return s.wb.Agents()
}

// Submit starts a run. An empty agentID gives the task to the coordinator, who
// decides whether to answer it himself or split it between specialists.
func (s *WorkbenchService) Submit(agentID, prompt string) (workbench.Task, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return workbench.Task{}, errors.New("prompt is empty")
	}
	if len(prompt) > maxPromptBytes {
		return workbench.Task{}, errors.New("prompt is too large")
	}
	return s.wb.Submit(strings.TrimSpace(agentID), prompt)
}

// Board returns the task board: what Anton has assigned and how far along it is.
func (s *WorkbenchService) Board() []board.Card {
	return s.wb.Board()
}

// ClearConversation starts a new conversation: the agents forget the previous
// exchange, and the next request opens a fresh session for each of them.
func (s *WorkbenchService) ClearConversation() {
	s.wb.ClearConversation()
}

// Cancel stops a run, including any specialists working inside it.
func (s *WorkbenchService) Cancel(runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("runId is empty")
	}
	return s.wb.Cancel(runID)
}
