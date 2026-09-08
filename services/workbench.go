// Package services holds the thin adapters the frontend can call.
//
// Nothing here contains logic worth testing on its own; it validates arguments
// and forwards to internal/workbench.
package services

import (
	"errors"
	"strings"

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

// Submit hands a prompt to an agent. An empty agentID goes to the coordinator.
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

// Cancel stops a running task.
func (s *WorkbenchService) Cancel(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("taskId is empty")
	}
	return s.wb.Cancel(taskID)
}
