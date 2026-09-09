package claude

// What agents are allowed to do, and why the choice is Work's to make.
//
// Work runs the CLI with -p, so there is nobody at a keyboard to answer a
// permission prompt: a mode that asks does not pause, it refuses. Leaving the
// flag off inherits whatever the user's own Claude installation allows, which
// reads as the cautious thing to do and in practice shipped agents that could
// read a project, could change nothing in it, and said so on every single run.
//
// So Work picks a mode and the user picks which. The CLI offers six; three are
// worth offering here, because without a person to ask, "ask first" (default),
// "let a classifier decide" (auto) and "deny anything not pre-approved"
// (dontAsk) all arrive at the same dead end.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PermissionMode is the value passed to the CLI as --permission-mode. The
// constants are the CLI's own spellings, not Work's: this is the one place the
// two vocabularies meet, and translating them would only add a table to get
// wrong.
type PermissionMode string

const (
	// PermissionRead lets agents read the project and answer about it. The CLI
	// calls this plan mode: reads and searches go through, edits and shell
	// commands are refused before they run, whatever the agent intended.
	PermissionRead PermissionMode = "plan"
	// PermissionEdit lets agents do the work: file edits and commands go
	// through without asking. This is the mode Work is built around -- the
	// change review exists precisely because edits land without a prompt --
	// and it is the default.
	PermissionEdit PermissionMode = "acceptEdits"
	// PermissionAll turns the checks off entirely, including the ones that
	// would have stopped something destructive. The CLI will not honour it
	// until its disclaimer has been accepted once; see BypassAccepted.
	PermissionAll PermissionMode = "bypassPermissions"
)

// DefaultPermission is what Work runs in until the user says otherwise.
const DefaultPermission = PermissionEdit

// PermissionChoice is one mode as the frontend shows it. The labels live here
// rather than in the UI so that a mode is described once, next to the flag
// value it sends.
type PermissionChoice struct {
	ID    PermissionMode `json:"id"`
	Label string         `json:"label"`
	// Detail says what the mode actually permits, in the terms of what an
	// agent will and will not be able to do.
	Detail string `json:"detail"`
	// Dangerous marks the mode that removes the last check. The UI asks twice
	// for this one; nothing else about it is different.
	Dangerous bool `json:"dangerous,omitempty"`
}

// PermissionChoices lists the modes, least powerful first. The order is the
// order they are offered in.
func PermissionChoices() []PermissionChoice {
	return []PermissionChoice{
		{
			ID:     PermissionRead,
			Label:  "Read only",
			Detail: "Agents read the project and answer. Edits and commands are refused before they run.",
		},
		{
			ID:     PermissionEdit,
			Label:  "Edit files",
			Detail: "Agents change files and run commands without asking. Every file they touch is listed in the change review, with a revert.",
		},
		{
			ID:        PermissionAll,
			Label:     "No limits",
			Detail:    "Every permission check off, including the ones that stop something destructive. Nothing asks, and the change review is the only record.",
			Dangerous: true,
		},
	}
}

// ParsePermissionMode validates a mode that came from outside Go: the config
// file or the frontend. Empty means "unset", which is the default rather than
// an error -- a config file written before this existed is not corrupt.
func ParsePermissionMode(s string) (PermissionMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultPermission, nil
	}
	for _, c := range PermissionChoices() {
		if PermissionMode(s) == c.ID {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("claude: unknown permission mode %q", s)
}

// BypassAccepted reports whether the local CLI will actually honour
// PermissionAll.
//
// It requires its own disclaimer to have been accepted once, interactively,
// and when it has not been it drops the mode rather than refusing the run: the
// agents come back unable to act with nothing on screen to explain why. This
// reads the flag that accepting leaves behind, so Work can explain it instead.
//
// An unreadable or absent settings file reads as not accepted, which is the
// useful way round. The line it produces says the acceptance could not be
// confirmed, and showing that to somebody who has already accepted costs them
// one sentence; the reverse costs them a run that quietly did nothing.
func BypassAccepted() bool {
	dir := configDir()
	if dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return false
	}
	var settings struct {
		SkipDangerousModePermissionPrompt bool `json:"skipDangerousModePermissionPrompt"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}
	return settings.SkipDangerousModePermissionPrompt
}

// AcceptBypassCommand is what to run, once, to accept that disclaimer. Work
// does not run it: it is an interactive confirmation about turning off the
// user's own safety checks, and a workbench clicking through it on their
// behalf is exactly what it is there to prevent.
const AcceptBypassCommand = "claude --dangerously-skip-permissions"

// configDir is the Claude CLI's own configuration directory, honouring the
// environment variable it reads itself so a user who has moved it is not told
// they have not accepted something they have.
func configDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
