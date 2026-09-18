package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every mode on offer has to be a value the CLI accepts, spelled the way the
// CLI spells it: a typo here is a run that dies on an unknown flag value.
func TestPermissionChoicesAreCLIModes(t *testing.T) {
	// The modes claude --help lists. Anything not in here is not a mode.
	valid := map[PermissionMode]bool{
		"acceptEdits": true, "auto": true, "bypassPermissions": true,
		"default": true, "dontAsk": true, "plan": true,
	}
	choices := PermissionChoices()
	if len(choices) == 0 {
		t.Fatal("no choices")
	}
	seen := make(map[PermissionMode]bool, len(choices))
	for _, c := range choices {
		if !valid[c.ID] {
			t.Errorf("%q is not a CLI permission mode", c.ID)
		}
		if seen[c.ID] {
			t.Errorf("%q offered twice", c.ID)
		}
		seen[c.ID] = true
		if c.Label == "" || c.Detail == "" {
			t.Errorf("%q has nothing to show: %+v", c.ID, c)
		}
	}
	// The default has to be one of them, or the toggle opens on nothing.
	if !seen[DefaultPermission] {
		t.Errorf("default %q is not on offer", DefaultPermission)
	}
	// Exactly one mode is the dangerous one, and it is the bypass.
	var dangerous []PermissionMode
	for _, c := range choices {
		if c.Dangerous {
			dangerous = append(dangerous, c.ID)
		}
	}
	if len(dangerous) != 1 || dangerous[0] != PermissionAll {
		t.Errorf("dangerous modes = %v, want [%s]", dangerous, PermissionAll)
	}
}

// An empty mode is a config file written before the field existed, not a
// corrupt one.
func TestParsePermissionMode(t *testing.T) {
	if got, err := ParsePermissionMode(""); err != nil || got != DefaultPermission {
		t.Errorf("ParsePermissionMode(\"\") = %q, %v", got, err)
	}
	if got, err := ParsePermissionMode("  acceptEdits  "); err != nil || got != PermissionEdit {
		t.Errorf("ParsePermissionMode(padded) = %q, %v", got, err)
	}
	// A real CLI mode that Work does not offer is still refused: they all mean
	// "nobody can answer, so nothing happens" in a headless run.
	for _, mode := range []string{"default", "dontAsk", "auto", "nonsense"} {
		if _, err := ParsePermissionMode(mode); err == nil {
			t.Errorf("ParsePermissionMode(%q) accepted", mode)
		}
	}
}

// The dangerous mode is honoured only once the CLI's own disclaimer has been
// accepted, and the flag that records it lives in the CLI's settings file.
func TestBypassAccepted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	// No settings file at all: not accepted, and not a crash.
	if BypassAccepted() {
		t.Error("accepted with no settings file")
	}

	write := func(v any) {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A settings file that says nothing about it is not acceptance.
	write(map[string]any{"model": "sonnet"})
	if BypassAccepted() {
		t.Error("accepted from an unrelated settings file")
	}

	write(map[string]any{"skipDangerousModePermissionPrompt": true})
	if !BypassAccepted() {
		t.Error("not accepted when the flag is set")
	}

	// Unparseable is unknown, and unknown reads as not accepted: the warning
	// it produces is cheaper than a run that silently does nothing.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if BypassAccepted() {
		t.Error("accepted from a corrupt settings file")
	}
}

// The mode has to reach the process, which is the whole point of it: a mode
// held in Go and never passed on the command line is an agent that still
// cannot act. Run against a fake CLI that records what it was called with.
func TestRunPassesPermissionMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	recorded := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + recorded + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	err := NewRunner(bin).Run(context.Background(), Request{
		Prompt:         "do the thing",
		PermissionMode: string(PermissionEdit),
	}, func(Event) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatalf("the CLI was not called: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !hasFlag(args, "--permission-mode", string(PermissionEdit)) {
		t.Errorf("args = %v, want --permission-mode %s", args, PermissionEdit)
	}
}

// An empty mode passes no flag at all, which inherits the user's own Claude
// configuration. Nothing in Work asks for that any more -- the workbench
// always has a mode -- but the runner is the layer that must not invent one.
func TestRunWithoutPermissionModePassesNoFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	recorded := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + recorded + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := NewRunner(bin).Run(context.Background(), Request{Prompt: "x"}, func(Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range strings.Split(string(data), "\n") {
		if arg == "--permission-mode" {
			t.Error("passed --permission-mode with no mode set")
		}
	}
}

// hasFlag reports whether args carries flag immediately followed by value.
func hasFlag(args []string, flag, value string) bool {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
