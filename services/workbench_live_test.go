package services

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/workbench"
)

// These exercise the Wails binding surface itself -- the exact methods the
// frontend calls -- rather than the workbench underneath it.
//
// The ones named Live need a real server and skip without one:
//
//	docker compose up -d
//	WORK_LIVE_SERVER=http://127.0.0.1:8080 \
//	WORK_LIVE_SIGNUP_TOKEN=<WORK_SIGNUP_TOKEN> \
//	    go test ./services -run Live -v
//
// The rest run always, because what they check -- that Work with no
// workspace is the Work that shipped before any of this -- has to hold on a
// machine that has never heard of a server.
func liveServer(t *testing.T) (serverURL, signupToken string) {
	t.Helper()
	serverURL = os.Getenv("WORK_LIVE_SERVER")
	signupToken = os.Getenv("WORK_LIVE_SIGNUP_TOKEN")
	if serverURL == "" || signupToken == "" {
		t.Skip("set WORK_LIVE_SERVER and WORK_LIVE_SIGNUP_TOKEN to run against a real server")
	}
	return serverURL, signupToken
}

// newService builds the service the way main.go does, with its own config
// home so one test cannot see another's workspace.
func newService(t *testing.T, withTransport bool) (*WorkbenchService, *workbench.Workbench, string) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	projectDir := t.TempDir()
	wb := workbench.New(agents.Default(), claude.NewRunner(""), func(string, any) {}, projectDir)
	if withTransport {
		wb.UseOps(&workbench.HTTPClient{})
	}
	return NewWorkbenchService(wb), wb, projectDir
}

// TestNoWorkspaceIsTheOldBehaviour is the guarantee the whole feature is
// built around: a machine that has not joined a workspace must be the
// application that shipped before workspaces existed.
//
// Every workspace binding is called here with none joined. None of them may
// panic, none may half-succeed, and none may leave anything on disk.
func TestNoWorkspaceIsTheOldBehaviour(t *testing.T) {
	svc, wb, projectDir := newService(t, false)

	if svc.Workspaces() {
		t.Error("a build with no transport offers workspaces")
	}
	if svc.Workspace() != nil {
		t.Errorf("Workspace() = %+v, want nil", svc.Workspace())
	}
	if status := svc.SyncStatus(); status.Joined {
		t.Errorf("SyncStatus() = %+v, want Joined false", status)
	}
	if doc := svc.WorkspaceDocument(); doc.Tree != nil || doc.Detached != nil || doc.Cursor != 0 {
		t.Errorf("WorkspaceDocument() = %+v, want empty", doc)
	}
	if cards := svc.WorkspaceCards("anything"); cards != nil {
		t.Errorf("WorkspaceCards() = %+v, want nil", cards)
	}

	// Agents run where Work was started, as they always have.
	if got := wb.WorkDir(); got != projectDir {
		t.Errorf("WorkDir() = %q, want the startup folder %q", got, projectDir)
	}

	// Everything that would need a workspace refuses, and says why.
	if _, err := svc.WorkspaceKeys(); err == nil {
		t.Error("WorkspaceKeys succeeded with no workspace")
	}
	if _, err := svc.NewTab("api"); err == nil {
		t.Error("NewTab succeeded with no workspace")
	}
	if err := svc.ActivateTab("nope"); err == nil {
		t.Error("ActivateTab succeeded with no workspace")
	}
	if err := svc.CloseTab("nope"); err == nil {
		t.Error("CloseTab succeeded with no workspace")
	}
	if _, err := svc.JoinWorkspace("https://example.invalid", "wk_x"); !errors.Is(err, workbench.ErrNoTransport) {
		t.Errorf("JoinWorkspace error = %v, want ErrNoTransport", err)
	}
	if _, err := svc.CreateWorkspace("https://example.invalid", "a-signup-token-long-enough", "x"); !errors.Is(err, workbench.ErrNoTransport) {
		t.Errorf("CreateWorkspace error = %v, want ErrNoTransport", err)
	}

	// Neither of these has anything to do, and neither may panic doing it.
	svc.SyncNow()
	if err := svc.LeaveWorkspace(); err != nil {
		t.Errorf("LeaveWorkspace with none joined: %v", err)
	}

	// The parts of the service that were here before still work, untouched.
	if len(svc.Agents()) == 0 {
		t.Error("the built-in team is missing")
	}
	if svc.Board() != nil && len(svc.Board()) != 0 {
		t.Errorf("Board() = %+v, want empty", svc.Board())
	}
	svc.ClearConversation()
	if svc.GetConfigPath() != "" {
		t.Errorf("GetConfigPath() = %q, want empty on a first run", svc.GetConfigPath())
	}

	// And none of it wrote a config file. A session that joined nothing has
	// nothing to remember.
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "work", "config.json")); !os.IsNotExist(err) {
		t.Errorf("a plain session left a config file behind (%v)", err)
	}
}

// TestNoWorkspaceWithATransportStillDoesNothing separates the two halves of
// "no workspace": a build that can reach a server but has not joined one is
// still the old application.
func TestNoWorkspaceWithATransportStillDoesNothing(t *testing.T) {
	svc, wb, projectDir := newService(t, true)

	if !svc.Workspaces() {
		t.Error("a build with a transport says it cannot do workspaces")
	}
	if svc.Workspace() != nil {
		t.Error("a transport on its own joined something")
	}
	if svc.SyncStatus().Joined {
		t.Error("a transport on its own reports a joined workspace")
	}
	if wb.WorkDir() != projectDir {
		t.Errorf("WorkDir() = %q, want %q", wb.WorkDir(), projectDir)
	}
	if _, err := svc.NewTab("api"); err == nil {
		t.Error("NewTab succeeded without joining")
	}
}

// TestBindingArgumentsAreChecked covers the service's own job. The webview is
// untrusted input, and these are the only lines in the package that are not
// a forward to the workbench.
func TestBindingArgumentsAreChecked(t *testing.T) {
	svc, _, _ := newService(t, true)

	if _, err := svc.NewTab("   "); err == nil {
		t.Error("a blank tab name was accepted")
	}
	if _, err := svc.NewTab(strings.Repeat("x", maxTabNameBytes+1)); err == nil {
		t.Error("an oversized tab name was accepted")
	}
	if err := svc.ActivateTab("  "); err == nil {
		t.Error("a blank tab id was accepted")
	}
	if err := svc.CloseTab(""); err == nil {
		t.Error("an empty tab id was accepted")
	}
	if _, err := svc.JoinWorkspace("", "wk_x"); err == nil {
		t.Error("an empty server url was accepted")
	}
	if _, err := svc.JoinWorkspace("https://example.invalid", "   "); err == nil {
		t.Error("a blank write key was accepted")
	}
	if _, err := svc.CreateWorkspace("https://example.invalid", "tok", ""); err == nil {
		t.Error("an empty workspace name was accepted")
	}
	if _, err := svc.CreateWorkspace("ftp://example.invalid", "a-signup-token-long-enough", "x"); err == nil {
		t.Error("a non-HTTP server url was accepted")
	}
}

// TestLiveBindingsCreateJoinAndBindATab drives the frontend's own entry
// points against a real server, in the order a person would.
func TestLiveBindingsCreateJoinAndBindATab(t *testing.T) {
	serverURL, signupToken := liveServer(t)

	svc, wb, startDir := newService(t, true)

	view, err := svc.CreateWorkspace(serverURL, signupToken, "live/bindings")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if !strings.HasPrefix(view.ID, "ws_") {
		t.Errorf("workspace id = %q, want a ws_ id", view.ID)
	}
	if view.Name != "live/bindings" {
		t.Errorf("name = %q", view.Name)
	}
	if view.Actor == "" {
		t.Error("this machine has no actor id")
	}
	if len(view.Tabs) != 0 {
		t.Errorf("a fresh workspace has %d tabs", len(view.Tabs))
	}

	keys, err := svc.WorkspaceKeys()
	if err != nil {
		t.Fatalf("WorkspaceKeys: %v", err)
	}
	if !strings.HasPrefix(keys.WriteKey, "wk_") || !strings.HasPrefix(keys.ReadKey, "rk_") {
		t.Fatalf("keys = %+v, want a wk_ and an rk_", keys)
	}

	// The keys are handed over when asked for and never ride along on the
	// payload the frontend fetches as a matter of course.
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	if strings.Contains(string(encoded), keys.WriteKey) || strings.Contains(string(encoded), keys.ReadKey) {
		t.Error("a key is in the WorkspaceView payload")
	}
	statusJSON, err := json.Marshal(svc.SyncStatus())
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(statusJSON), keys.WriteKey) {
		t.Error("the write key is in the SyncStatus payload")
	}

	// A tab, and the per-machine folder it means here.
	tab, err := svc.NewTab("api")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if err := svc.ActivateTab(tab.ID); err == nil {
		t.Error("an unbound tab was activated")
	}
	if wb.WorkDir() != startDir {
		t.Errorf("a refused activation moved the working folder to %q", wb.WorkDir())
	}

	projectDir := t.TempDir()
	if err := wb.BindTab(tab.ID, projectDir); err != nil {
		t.Fatalf("BindTab: %v", err)
	}
	if err := svc.ActivateTab(tab.ID); err != nil {
		t.Fatalf("ActivateTab: %v", err)
	}
	if wb.WorkDir() != projectDir {
		t.Fatalf("WorkDir() = %q, want the tab's folder %q", wb.WorkDir(), projectDir)
	}

	bound := svc.Workspace()
	if len(bound.Tabs) != 1 || !bound.Tabs[0].Bound || bound.Tabs[0].Dir != projectDir {
		t.Fatalf("tabs = %+v, want one bound to %q", bound.Tabs, projectDir)
	}
	if bound.ActiveTab != tab.ID {
		t.Errorf("active tab = %q, want %q", bound.ActiveTab, tab.ID)
	}

	svc.SyncNow()

	// Leaving puts agents back where Work started, and forgets the
	// workspace without touching what has not been pushed.
	if err := svc.LeaveWorkspace(); err != nil {
		t.Fatalf("LeaveWorkspace: %v", err)
	}
	if svc.Workspace() != nil {
		t.Error("Workspace() is still set after leaving")
	}
	if svc.SyncStatus().Joined {
		t.Error("SyncStatus() still reports joined after leaving")
	}
	if wb.WorkDir() != startDir {
		t.Errorf("after leaving, agents run in %q, want the startup folder %q", wb.WorkDir(), startDir)
	}
}

// TestLiveBindingsRejectABadServer checks the errors the frontend has to be
// able to tell apart: a server that is not there, and a key that will never
// work.
func TestLiveBindingsRejectABadServer(t *testing.T) {
	serverURL, signupToken := liveServer(t)
	svc, _, _ := newService(t, true)

	if _, err := svc.JoinWorkspace("http://127.0.0.1:1", "wk_nothing_here"); err == nil {
		t.Error("joining a server that is not listening succeeded")
	}
	if _, err := svc.CreateWorkspace(serverURL, "wrong-token-but-long-enough-to-pass", "x"); err == nil {
		t.Error("creating a workspace with the wrong signup token succeeded")
	}
	if svc.Workspace() != nil {
		t.Error("a failed join left a workspace behind")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "work", "config.json")); !os.IsNotExist(err) {
		t.Error("a failed join wrote a config file")
	}

	// And the real one still works, so the checks above are about the input
	// rather than about the server being broken.
	if _, err := svc.CreateWorkspace(serverURL, signupToken, "live/rejects"); err != nil {
		t.Fatalf("CreateWorkspace with the right token: %v", err)
	}
}
