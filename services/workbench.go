// Package services holds the thin adapters the frontend can call.
//
// Nothing here contains logic worth testing on its own; it validates arguments
// and forwards to internal/workbench.
package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/changes"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/workbench"
)

// maxPromptBytes bounds what the frontend may send. The webview is untrusted
// input like any other client.
const maxPromptBytes = 128 << 10 // 128 KiB

// maxTabNameBytes bounds a tab label. The server caps identifiers, not
// labels, so this is the app's own limit: a name is read off a tab strip.
const maxTabNameBytes = 200

// joinTimeout bounds the round trips that create or join a workspace.
//
// These are the only workspace calls the user waits on -- everything after
// them happens on the sync loop -- so the deadline is generous enough for a
// slow link and short enough that a wrong server address is a message rather
// than a spinner that never resolves.
const joinTimeout = 30 * time.Second

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

// Permissions is what agents are allowed to do, the modes that could be
// chosen instead, and whether the local CLI will honour the dangerous one.
//
// One call rather than three: the toggle needs all of it to draw a single row,
// and a mode without its label or its warning is not something a person can
// choose between.
type Permissions struct {
	// Mode is the mode in force now.
	Mode claude.PermissionMode `json:"mode"`
	// Choices are the modes on offer, least powerful first.
	Choices []claude.PermissionChoice `json:"choices"`
	// BypassAccepted is whether the local Claude CLI has had its
	// skip-permissions disclaimer accepted. When it has not, the CLI silently
	// ignores the dangerous mode, so the UI says so rather than letting a run
	// come back having done nothing.
	BypassAccepted bool `json:"bypassAccepted"`
	// AcceptCommand is the one command that accepts it, for the UI to show.
	AcceptCommand string `json:"acceptCommand"`
}

// Permissions returns the current mode and everything needed to describe it.
func (s *WorkbenchService) Permissions() Permissions {
	return Permissions{
		Mode:           s.wb.PermissionMode(),
		Choices:        claude.PermissionChoices(),
		BypassAccepted: claude.BypassAccepted(),
		AcceptCommand:  claude.AcceptBypassCommand,
	}
}

// SetPermissionMode changes what agents may do, for this session and the next.
//
// It answers with the whole setting rather than nothing, so the toggle draws
// from what the backend actually holds instead of assuming its own click won.
// An unknown mode is an error and changes nothing.
func (s *WorkbenchService) SetPermissionMode(mode string) (Permissions, error) {
	if _, err := s.wb.SetPermissionMode(mode); err != nil {
		return s.Permissions(), err
	}
	return s.Permissions(), nil
}

// ApplyProposalsWithoutReview reports whether Claude's proposed restructurings
// apply themselves.
//
// Off by default. The panel is the only approval gate this app has -- when
// Claude edits files it writes to disk and the app only gets to look
// afterwards, but here the app owns the write, so it can ask first.
func (s *WorkbenchService) ApplyProposalsWithoutReview() bool {
	return s.wb.ApplyProposalsWithoutReview()
}

// SetApplyProposalsWithoutReview changes it, and answers with what the backend
// now holds rather than nothing -- so the control draws from the setting
// instead of assuming its own click won.
func (s *WorkbenchService) SetApplyProposalsWithoutReview(without bool) (bool, error) {
	if err := s.wb.SetApplyProposalsWithoutReview(without); err != nil {
		return s.wb.ApplyProposalsWithoutReview(), err
	}
	return s.wb.ApplyProposalsWithoutReview(), nil
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

// Chat asks the coordinator a question without starting a run, so the input
// stays usable while specialists are working.
func (s *WorkbenchService) Chat(prompt, mode string) (workbench.Task, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return workbench.Task{}, errors.New("prompt is empty")
	}
	if len(prompt) > maxPromptBytes {
		return workbench.Task{}, errors.New("prompt is too large")
	}
	return s.wb.Chat(prompt, mode)
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

// ServiceStartup hands the workbench the app's lifetime, so a joined
// workspace's push/pull loop ends with the window rather than having to be
// shut down separately -- the same arrangement UpdateService uses for its
// poll.
//
// With no workspace joined this does nothing at all, which is the point:
// every line below is inert until someone joins one, and Work behaves exactly
// as it always has.
func (s *WorkbenchService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.wb.StartSync(ctx)
	return nil
}

// Workspaces reports whether this build can talk to a workspace server at
// all. The UI hides the workspace controls when it cannot, rather than
// offering buttons whose only outcome is an error.
func (s *WorkbenchService) Workspaces() bool {
	return s.wb.HasOps()
}

// Workspace returns the joined workspace, or null on a machine that has not
// joined one. Null is the ordinary case.
//
// The keys are not on it. Asking for those is WorkspaceKeys, so a credential
// never rides along on a payload the frontend fetches as a matter of course.
func (s *WorkbenchService) Workspace() *workbench.WorkspaceView {
	return s.wb.Workspace()
}

// WorkspaceKeys returns the workspace's write key, and its read key if this
// machine is the one that created it. This is what an invitation is made of.
func (s *WorkbenchService) WorkspaceKeys() (workbench.Keys, error) {
	return s.wb.Keys()
}

// CreateLocalWorkspace makes a workspace that lives only on this machine and
// opens it. Nothing is sent anywhere and no server is involved, which is why
// it takes neither an address nor a token.
//
// This is what setup calls. Work has always run perfectly well without a
// server, and the version of that where you also get tabs, a shared outline
// and a board is this.
func (s *WorkbenchService) CreateLocalWorkspace(name string) (*workbench.WorkspaceView, error) {
	return s.wb.CreateLocalWorkspace(name)
}

// CreateWorkspace makes a workspace on a server and joins it.
//
// signupToken is the server's own signup token, not a workspace key: there is
// no workspace to authenticate against yet. It is empty against a server that
// does not gate creation, which is the default.
func (s *WorkbenchService) CreateWorkspace(serverURL, signupToken, name string) (*workbench.WorkspaceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	defer cancel()
	return s.wb.CreateWorkspace(ctx, serverURL, signupToken, name)
}

// JoinWorkspace joins an existing workspace with its write key. The key
// identifies the workspace, so there is no ID to supply.
func (s *WorkbenchService) JoinWorkspace(serverURL, writeKey string) (*workbench.WorkspaceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	defer cancel()
	return s.wb.JoinWorkspace(ctx, serverURL, writeKey)
}

// MintKey issues another key for the joined workspace: "read" for a link to
// send somebody, "write" for another machine to join with.
//
// There is no call that reads a key back, because the server keeps only hashes
// of them. Asking for one is asking for a new one, and the keys already in use
// keep working.
func (s *WorkbenchService) MintKey(access string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	defer cancel()
	return s.wb.MintKey(ctx, access)
}

// LeaveWorkspace stops syncing and returns Work to running purely locally.
// Ops that never reached the server are kept, not discarded.
func (s *WorkbenchService) LeaveWorkspace() error {
	return s.wb.LeaveWorkspace()
}

// SyncStatus is where the second stage stands: online, syncing, offline or
// rejected, how many ops are waiting, and how far behind the server this
// machine is. Joined is false when no workspace has been joined.
//
// The frontend does not need to poll this -- workspace:sync carries the same
// value whenever it changes. This is for the first paint.
func (s *WorkbenchService) SyncStatus() workbench.Status {
	return s.wb.SyncStatus()
}

// SyncNow pushes and pulls immediately instead of waiting for the next poll.
// It is the retry behind an offline indicator, and the way back from a
// rejected key once it has been replaced.
func (s *WorkbenchService) SyncNow() {
	s.wb.SyncNow()
}

// WorkspaceDocument is the merged workspace: every tab, and every card from
// every machine that has joined.
//
// Fetched rather than pushed, because it changes when someone edits something
// and not on the poll. Call it when the cursor on a workspace:sync event
// moves.
func (s *WorkbenchService) WorkspaceDocument() workbench.Document {
	return s.wb.WorkspaceDocument()
}

// WorkspaceCards is one tab's cards as the whole workspace sees them. Board
// remains this machine's own board; this is everyone's.
func (s *WorkbenchService) WorkspaceCards(tabID string) []board.Card {
	return s.wb.WorkspaceCards(tabID)
}

// NextTask is the first task on the active tab's plan that is still to do.
//
// The whole task rather than its id: the caller wants to show it, and a second
// round trip to ask what an id means is a frame of empty button.
//
// The second return is false when there is nothing to offer -- an empty plan,
// no workspace, a tab this machine has bound to no folder, or a plan where
// everything is finished. None of those is an error.
func (s *WorkbenchService) NextTask() (workbench.PlanTask, bool) {
	return s.wb.NextTask()
}

// StartNextTask runs the next task on the active tab's plan.
//
// The order planning holds is the order work takes: this is what makes that
// true rather than decorative. Refused, with a reason that distinguishes them,
// when there is no workspace, no folder for this tab on this machine, or
// nothing left to do.
func (s *WorkbenchService) StartNextTask() (workbench.Task, error) {
	return s.wb.StartNextTask()
}

// Restructure asks Claude to propose changes to a tab's outline or plan.
//
// Returns when the run has started, not when it has answered. The proposal
// itself never comes back through this call: Claude answers by calling a tool,
// the tool call goes past on the stream the console is already reading, and the
// review panel picks it up there. Nothing is applied until somebody presses
// Apply.
//
// mode is "idea" or "planning". They ask different questions of the same
// document -- reorganise, or break into tasks -- and are given different state
// to answer from.
func (s *WorkbenchService) Restructure(tabID, mode, request string) (workbench.Task, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return workbench.Task{}, errors.New("tabId is empty")
	}
	return s.wb.Restructure(tabID, mode, request)
}

// ApplyWorkspaceEdits writes idea and planning edits into a tab's document and
// hands back the merged result.
//
// This is the call the outline did not have. Before it, the frontend had its
// own replica -- its own actor, its own Lamport clock, its own outbox in
// localStorage -- which made two writers out of one machine, and the outline
// could not leave the webview it was typed in. Now the edit is described here
// and everything only this machine may mint is minted once, by the Sync that
// owns the workspace: the op ID, the actor, the clock.
//
// It returns the whole document rather than nothing, so the caller sees its
// own edit merged without a second round trip that could race the sync loop.
// An edit that reached this call is on disk before it returns; reaching the
// server is the second stage and happens afterwards, or offline, later.
//
// ErrNoTransport comes back on a machine that has joined no workspace, which
// is the ordinary case and not an error to show: there is no shared document
// to write to, and the caller keeps its own.
func (s *WorkbenchService) ApplyWorkspaceEdits(tabID string, edits []workbench.Edit) (workbench.Document, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return workbench.Document{}, errors.New("tabId is empty")
	}
	if len(edits) == 0 {
		return workbench.Document{}, errors.New("edits is empty")
	}
	return s.wb.ApplyEdits(tabID, edits)
}

// NewTab creates a workspace tab. It has no folder yet -- see BindTabFolder.
func (s *WorkbenchService) NewTab(name string) (workbench.TabView, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return workbench.TabView{}, errors.New("name is empty")
	}
	if len(name) > maxTabNameBytes {
		return workbench.TabView{}, errors.New("name is too long")
	}
	return s.wb.NewTab(name)
}

// ActivateTab makes a tab the one agents run in, which means running them in
// the folder this machine bound to it. Refused while a run is in flight.
func (s *WorkbenchService) ActivateTab(tabID string) error {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return errors.New("tabId is empty")
	}
	return s.wb.ActivateTab(tabID)
}

// CloseTab retires a tab for everyone in the workspace.
func (s *WorkbenchService) CloseTab(tabID string) error {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return errors.New("tabId is empty")
	}
	return s.wb.CloseTab(tabID)
}

// BindTabFolder asks the user which project on this machine a tab means,
// using the platform's own folder picker, and remembers the answer.
//
// The folder is per-machine and never leaves it: the workspace knows the tab,
// not where anybody keeps their code. A cancelled dialog returns an empty
// path and no error, exactly as SelectConfigFolder does.
func (s *WorkbenchService) BindTabFolder(tabID string) (string, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return "", errors.New("tabId is empty")
	}

	app := application.Get()
	if app == nil {
		return "", errors.New("no application")
	}

	dialog := app.Dialog.OpenFile().
		SetTitle("Choose the project folder for this tab").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(false).
		SetDirectory(s.wb.WorkDir())
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

	if err := s.wb.BindTab(tabID, path); err != nil {
		return "", err
	}
	return path, nil
}
