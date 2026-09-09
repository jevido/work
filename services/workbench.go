// Package services holds the thin adapters the frontend can call.
//
// Nothing here contains logic worth testing on its own; it validates arguments
// and forwards to internal/workbench.
package services

import (
	"errors"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/changes"
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

// AgentProfile returns what one agent's folder says about them: the skills in
// skills/, their PERSONALITY.md and the line Anton routes them on. The profile
// view asks for this when it opens, rather than reading it out of the roster,
// because the roster is re-sent constantly and this is not small.
func (s *WorkbenchService) AgentProfile(agentID string) (agents.Profile, error) {
	return s.wb.AgentProfile(strings.TrimSpace(agentID))
}

// GetConfigPath returns the folder Work loads agents from, or an empty string
// if the user has not picked one yet. An empty result is the first-run signal.
func (s *WorkbenchService) GetConfigPath() string {
	return s.wb.ConfigRoot()
}

// SelectConfigFolder asks the user for a config folder with the platform's own
// folder picker, then loads the team from it and remembers the choice.
//
// A cancelled dialog returns an empty path and no error: the user declining is
// not a failure, and the caller can tell the two apart by the empty string.
func (s *WorkbenchService) SelectConfigFolder() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("no application")
	}

	dialog := app.Dialog.OpenFile().
		SetTitle("Choose the folder Work keeps your agents in").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		// Empty on a first run, which the platform reads as "no preference".
		SetDirectory(s.wb.ConfigRoot())
	// Parenting the picker on the office window makes it modal over Work
	// rather than a loose window the user can lose behind it.
	if window := app.Window.Current(); window != nil {
		dialog = dialog.AttachToWindow(window)
	}

	path, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	if _, err := s.wb.SetConfigRoot(path); err != nil {
		return "", err
	}
	return path, nil
}

// ReloadAgents rescans the config folder and rebuilds the team, so an agent
// folder added outside Work shows up without a restart.
func (s *WorkbenchService) ReloadAgents() ([]workbench.AgentStatus, error) {
	return s.wb.ReloadAgents()
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

// Changes lists the files the last run touched, with their diffs.
func (s *WorkbenchService) Changes() []changes.Change {
	return s.wb.Changes()
}

// Revert undoes one of those file changes, restoring the content the run
// started with rather than the last commit.
func (s *WorkbenchService) Revert(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is empty")
	}
	return s.wb.Revert(path)
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
