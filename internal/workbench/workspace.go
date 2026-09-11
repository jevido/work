package workbench

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/config"
)

// WorkspaceView is the workspace as the frontend is allowed to see it.
//
// It is not config.Workspace: that struct holds the keys, and a key is the one
// thing here that must not be handed out with every routine status payload.
// Whoever is inviting a colleague asks for one by name; see Workbench.Keys.
type WorkspaceView struct {
	ServerURL string    `json:"serverUrl"`
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	Tabs      []TabView `json:"tabs"`
	ActiveTab string    `json:"activeTab,omitempty"`
}

// TabView is one tab and this machine's binding for it.
type TabView struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Dir is this machine's folder for the tab, empty when unbound.
	Dir string `json:"dir,omitempty"`
	// Bound is false when the tab has no folder here yet, or when the folder
	// it had has since gone. Either way the answer is the same: the tab
	// cannot run anything until someone points it at a project.
	Bound bool `json:"bound"`
}

// Keys are a workspace's credentials, handed over only when asked for.
type Keys struct {
	// WriteKey can append ops and read the log.
	WriteKey string `json:"writeKey,omitempty"`
	// ReadKey can only read, and is what goes into the web viewer. It is
	// empty on a machine that joined with a write key rather than creating
	// the workspace: the server hands both out once and cannot show them
	// again.
	ReadKey string `json:"readKey,omitempty"`
}

// UseOps wires the transport. It is called once, from main, before the window
// opens; a nil client leaves every workspace call returning ErrNoTransport,
// which is the shape of a build with no server to talk to.
func (w *Workbench) UseOps(client OpsClient) {
	w.ops = client
}

// HasOps reports whether a workspace transport is available at all. The UI
// uses it to decide whether to offer workspaces, rather than showing controls
// that can only fail.
func (w *Workbench) HasOps() bool { return w.ops != nil }

// UseWorkspace restores a workspace saved in the config and starts syncing it.
//
// Startup calls this, and it is deliberately forgiving: a workspace that
// cannot be restored is logged by the caller and Work opens anyway, in exactly
// the state it would have been in without one. Losing the ability to sync is
// not a reason to lose the ability to work.
func (w *Workbench) UseWorkspace(ws *config.Workspace) error {
	if ws == nil || ws.ID == "" {
		return nil
	}
	if w.ops == nil {
		return ErrNoTransport
	}
	// Restored, not chosen: this came out of the config file, so writing it
	// straight back would rewrite the config on every launch to say what it
	// already said.
	return w.adopt(ws, false)
}

// StartSync starts the workspace's push/pull loop, if one is joined.
//
// The lifetime comes from the caller's context rather than from here, so the
// loop ends with the app instead of having to be shut down separately. See
// services.WorkbenchService.ServiceStartup.
func (w *Workbench) StartSync(ctx context.Context) {
	w.syncCtx.Store(&ctx)
	if s := w.sync.Load(); s != nil {
		s.start(ctx)
	}
}

// CreateWorkspace makes a workspace on a server and joins it.
//
// signupToken is the server's own WORK_SIGNUP_TOKEN, not a workspace key --
// there is no workspace to authenticate against yet. This call is the only
// time the server will ever show the keys, so both are stored here and there
// is no way to ask for them again later.
func (w *Workbench) CreateWorkspace(ctx context.Context, serverURL, signupToken, name string) (*WorkspaceView, error) {
	serverURL, err := cleanServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("workbench: empty workspace name")
	}
	if signupToken = strings.TrimSpace(signupToken); signupToken == "" {
		return nil, errors.New("workbench: empty signup token")
	}
	if w.ops == nil {
		return nil, ErrNoTransport
	}

	created, err := w.ops.Create(ctx, serverURL, signupToken, name)
	if err != nil {
		return nil, fmt.Errorf("workbench: create workspace: %w", err)
	}
	if created.Workspace.ID == "" || created.WriteKey == "" {
		return nil, errors.New("workbench: server returned a workspace with no id or key")
	}

	actor, err := mintID()
	if err != nil {
		return nil, err
	}
	ws := &config.Workspace{
		ServerURL: serverURL,
		ID:        created.Workspace.ID,
		Name:      cmp.Or(created.Workspace.Name, name),
		WriteKey:  created.WriteKey,
		ReadKey:   created.ReadKey,
		Actor:     actor,
	}
	if err := w.adopt(ws, true); err != nil {
		return nil, err
	}
	return w.Workspace(), nil
}

// JoinWorkspace joins an existing workspace with a key.
//
// There is no workspace ID to supply: the key identifies the workspace, which
// is why no workspace ID appears in any path on the server. The key is checked
// against the server before anything is written to disk, so a typo is an error
// on this call rather than a workspace that looks joined and quietly fails to
// push an hour later.
//
// A read key is refused. It would join, and it would pull, and then the first
// thing the user did would fail to push -- a state that is worse than not
// joining, because it looks like it worked.
func (w *Workbench) JoinWorkspace(ctx context.Context, serverURL, writeKey string) (*WorkspaceView, error) {
	serverURL, err := cleanServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	writeKey = strings.TrimSpace(writeKey)
	if writeKey == "" {
		return nil, errors.New("workbench: empty write key")
	}
	if w.ops == nil {
		return nil, ErrNoTransport
	}

	meta, err := w.ops.Meta(ctx, serverURL, writeKey)
	if err != nil {
		return nil, fmt.Errorf("workbench: join workspace: %w", err)
	}
	if meta.Access != "write" {
		return nil, fmt.Errorf("workbench: that key has %s access; joining needs a write key", cmp.Or(meta.Access, "no"))
	}
	if meta.Workspace.ID == "" {
		return nil, errors.New("workbench: server returned a workspace with no id")
	}

	actor, err := mintID()
	if err != nil {
		return nil, err
	}
	ws := &config.Workspace{
		ServerURL: serverURL,
		ID:        meta.Workspace.ID,
		Name:      meta.Workspace.Name,
		WriteKey:  writeKey,
		Actor:     actor,
	}
	if err := w.adopt(ws, true); err != nil {
		return nil, err
	}
	return w.Workspace(), nil
}

// adopt installs a workspace: opens its outbox, replays it, starts its sync
// loop and points agents at the active tab's folder.
//
// save is false when the workspace came from the config file and is therefore
// already there. Anything already joined is closed first; its outbox stays on
// disk, so rejoining picks up ops that never made it out.
func (w *Workbench) adopt(ws *config.Workspace, save bool) error {
	s, err := newSync(w.ops, ws.ServerURL, ws.WriteKey, ws.ID, ws.Actor, w.emit)
	if err != nil {
		return err
	}
	s.onRemote = w.absorb

	w.wsMu.Lock()
	old := w.sync.Swap(s)
	w.ws = ws
	dir := w.tabDirLocked(ws.ActiveTab)
	w.wsMu.Unlock()

	// Outside the lock, deliberately. close waits for the sync loop to stop,
	// and that loop calls absorb, which wants wsMu -- closing while holding
	// it would have each side waiting for the other. See Sync.close.
	if old != nil {
		old.close()
	}

	w.useWorkDir(dir)

	if ctx := w.syncCtx.Load(); ctx != nil {
		s.start(*ctx)
	}

	if save {
		if err := w.persist(); err != nil {
			return err
		}
	}
	w.publishWorkspace()
	return nil
}

// LeaveWorkspace stops syncing and goes back to plain local Work.
//
// The outbox on disk is left alone. Leaving is usually "not right now" rather
// than "never again", and throwing away unsent work to make the button feel
// decisive is not a trade worth making. Rejoining the same workspace resumes
// from where it stopped.
func (w *Workbench) LeaveWorkspace() error {
	w.wsMu.Lock()
	joined := w.ws != nil
	old := w.sync.Swap(nil)
	w.ws = nil
	w.wsMu.Unlock()

	// Leaving what was never joined is not an error and is not an event
	// either: it must not touch the working folder, and it must not create a
	// config file to record the absence of a workspace. A session that
	// joined nothing leaves nothing behind.
	if !joined {
		return nil
	}

	// Outside the lock, for the reason adopt gives.
	if old != nil {
		old.close()
	}

	// Back to the folder Work was started in, which is where a session with
	// no workspace has always run.
	w.useWorkDir(w.baseDir)

	if err := config.Update(func(c *config.Config) { c.Workspace = nil }); err != nil {
		return err
	}
	w.publishWorkspace()
	return nil
}

// Workspace returns the joined workspace, or nil.
func (w *Workbench) Workspace() *WorkspaceView {
	w.wsMu.Lock()
	defer w.wsMu.Unlock()
	return w.viewLocked()
}

// Keys returns the joined workspace's credentials.
//
// Separate from Workspace on purpose. These are what let anyone holding them
// write to, or read, the workspace -- so they travel when someone asks for an
// invitation to show, and never as a field on a status payload that goes to
// the frontend several times a run.
func (w *Workbench) Keys() (Keys, error) {
	w.wsMu.Lock()
	defer w.wsMu.Unlock()
	if w.ws == nil {
		return Keys{}, errors.New("workbench: no workspace joined")
	}
	return Keys{WriteKey: w.ws.WriteKey, ReadKey: w.ws.ReadKey}, nil
}

// SyncStatus is where the second stage stands. With no workspace joined it
// reports the zero Status, whose Joined is false -- which is the whole answer.
func (w *Workbench) SyncStatus() Status {
	s := w.sync.Load()
	if s == nil {
		return Status{}
	}
	return s.status()
}

// SyncNow asks for a push and a pull immediately rather than at the next
// poll. It is what the retry button on an offline indicator does, and it is
// also how a workspace left in SyncRejected is retried once the key is fixed.
func (w *Workbench) SyncNow() {
	if s := w.sync.Load(); s != nil {
		s.nudge()
	}
}

// WorkspaceDocument is the merged workspace: every tab and every card, from
// every machine.
//
// It is pulled rather than pushed. The document changes when someone edits
// something, the sync status changes on every poll, and putting a whole
// merged tree on a three-second event would mean re-encoding the workspace
// for the overwhelmingly common answer of "nothing happened".
func (w *Workbench) WorkspaceDocument() Document {
	s := w.sync.Load()
	if s == nil {
		return Document{}
	}
	return s.document()
}

// WorkspaceCards is one tab's cards as the whole workspace sees them --
// this machine's and everyone else's.
//
// Deliberately not merged into Board. The local board is what this machine's
// runs are driving; folding a colleague's cards into it would have the
// workbench trying to schedule work it is not running.
func (w *Workbench) WorkspaceCards(tabID string) []board.Card {
	s := w.sync.Load()
	if s == nil {
		return nil
	}
	return cardsFromDocument(s.document(), strings.TrimSpace(tabID))
}

// NewTab creates a tab and tells the workspace about it.
//
// The tab has no folder yet. Binding is a separate, local step, because the
// folder is per-machine: whoever creates the tab picks their own, and everyone
// else picks theirs when they first open it.
func (w *Workbench) NewTab(name string) (TabView, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return TabView{}, errors.New("workbench: empty tab name")
	}

	s := w.sync.Load()
	if s == nil {
		return TabView{}, errors.New("workbench: no workspace joined")
	}
	id, err := mintID()
	if err != nil {
		return TabView{}, err
	}
	// The tab is in the document before it is in the config, so a crash
	// between the two leaves a tab the next start learns about from the log
	// rather than one only this machine ever knew existed.
	batch, err := tabOps(s, id, name, false)
	if err != nil {
		return TabView{}, err
	}
	if err := s.apply(batch); err != nil {
		return TabView{}, err
	}

	w.wsMu.Lock()
	if w.ws == nil {
		w.wsMu.Unlock()
		return TabView{}, errors.New("workbench: no workspace joined")
	}
	w.ws.SetTab(config.Tab{ID: id, Name: name})
	w.wsMu.Unlock()

	if err := w.persist(); err != nil {
		return TabView{}, err
	}
	w.publishWorkspace()
	return TabView{ID: id, Name: name}, nil
}

// BindTab points a tab at a project folder on this machine.
//
// The folder is checked here rather than at run time: a tab bound to something
// that is not a directory would fail on the first prompt, with an error about
// Claude rather than about the tab.
func (w *Workbench) BindTab(tabID, dir string) error {
	tabID = strings.TrimSpace(tabID)
	dir = strings.TrimSpace(dir)
	if tabID == "" {
		return errors.New("workbench: empty tab id")
	}
	if dir == "" {
		return errors.New("workbench: empty folder")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("workbench: bind tab: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workbench: %s is not a folder", dir)
	}

	w.wsMu.Lock()
	if w.ws == nil {
		w.wsMu.Unlock()
		return errors.New("workbench: no workspace joined")
	}
	tab, ok := w.ws.Tab(tabID)
	if !ok {
		w.wsMu.Unlock()
		return fmt.Errorf("workbench: unknown tab %q", tabID)
	}
	tab.Dir = dir
	w.ws.SetTab(tab)
	active := w.ws.ActiveTab == tabID
	w.wsMu.Unlock()

	if active {
		w.useWorkDir(dir)
	}
	if err := w.persist(); err != nil {
		return err
	}
	w.publishWorkspace()
	return nil
}

// ActivateTab makes a tab the one agents run in.
//
// It is refused while a run is in flight. The working directory is an argument
// to Claude processes that have already started and to the working-tree
// snapshot the review is diffed against, so moving it mid-run would produce a
// review of one project against the changes made in another.
func (w *Workbench) ActivateTab(tabID string) error {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return errors.New("workbench: empty tab id")
	}

	w.mu.Lock()
	busy := w.active != nil
	w.mu.Unlock()
	if busy {
		return errors.New("workbench: stop the run before switching tabs")
	}

	w.wsMu.Lock()
	if w.ws == nil {
		w.wsMu.Unlock()
		return errors.New("workbench: no workspace joined")
	}
	tab, ok := w.ws.Tab(tabID)
	if !ok {
		w.wsMu.Unlock()
		return fmt.Errorf("workbench: unknown tab %q", tabID)
	}
	if tab.Dir == "" {
		w.wsMu.Unlock()
		return fmt.Errorf("workbench: tab %q has no folder on this machine", tabID)
	}
	w.ws.ActiveTab = tabID
	w.wsMu.Unlock()

	w.useWorkDir(tab.Dir)

	if err := w.persist(); err != nil {
		return err
	}
	w.publishWorkspace()

	// The board this machine already holds belongs to the tab that is now
	// active, so it is republished under it. Without this, cards made before
	// any tab was active would never reach the workspace at all.
	w.publishBoard()
	return nil
}

// CloseTab retires a tab for everyone. The ops it produced stay in the log,
// so what happened in it remains readable.
func (w *Workbench) CloseTab(tabID string) error {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return errors.New("workbench: empty tab id")
	}

	s := w.sync.Load()
	if s == nil {
		return errors.New("workbench: no workspace joined")
	}
	batch, err := tabOps(s, tabID, "", true)
	if err != nil {
		return err
	}
	if err := s.apply(batch); err != nil {
		return err
	}

	w.wsMu.Lock()
	if w.ws != nil {
		w.ws.Tabs = deleteTab(w.ws.Tabs, tabID)
		if w.ws.ActiveTab == tabID {
			w.ws.ActiveTab = ""
		}
	}
	w.wsMu.Unlock()

	// Back to the startup folder: the tab whose folder agents were running in
	// no longer exists.
	w.useWorkDir(w.baseDir)

	if err := w.persist(); err != nil {
		return err
	}
	w.publishWorkspace()
	return nil
}

// absorb reconciles this machine's tab list with the merged document.
//
// It runs on the sync loop after ops from other machines land, and it is the
// only thing the workbench takes from a peer's ops. A tab a colleague created
// is adopted unbound -- this machine knows it exists and what it is called,
// and asks for a folder the first time anyone tries to work in it. A tab a
// colleague retired goes.
//
// Nothing else is folded in. A peer's cards are theirs; see WorkspaceCards.
func (w *Workbench) absorb(s *Sync) {
	tabs := documentTabs(s)

	w.wsMu.Lock()
	if w.ws == nil {
		w.wsMu.Unlock()
		return
	}
	changed := false
	for id, name := range tabs {
		switch existing, ok := w.ws.Tab(id); {
		case !ok:
			w.ws.SetTab(config.Tab{ID: id, Name: name})
			changed = true
		case name != "" && existing.Name != name:
			existing.Name = name
			w.ws.SetTab(existing)
			changed = true
		}
	}
	for _, t := range w.ws.Tabs {
		if _, ok := tabs[t.ID]; ok {
			continue
		}
		// Not in the document. Either a peer retired it, or this machine made
		// it while offline and the create op has not been pushed yet -- and
		// the second case must not delete it. An unpushed tab is one whose
		// create op is still in the outbox.
		if s.pendingNode(t.ID) {
			continue
		}
		w.ws.Tabs = deleteTab(w.ws.Tabs, t.ID)
		if w.ws.ActiveTab == t.ID {
			w.ws.ActiveTab = ""
		}
		changed = true
	}
	w.wsMu.Unlock()

	if !changed {
		return
	}
	if err := w.persist(); err != nil {
		s.setErr(err)
	}
	w.publishWorkspace()
}

// shareBoard is stage two for the board: the diff between what this machine's
// board says and what the workspace already holds, queued as ops.
//
// The nil check is the whole cost with no workspace joined -- one atomic load
// per board publish, which happens a handful of times per run. Nothing on the
// streaming path reaches here at all.
func (w *Workbench) shareBoard(cards []board.Card) {
	s := w.sync.Load()
	if s == nil {
		return
	}

	w.wsMu.Lock()
	var tab string
	if w.ws != nil {
		tab = w.ws.ActiveTab
	}
	w.wsMu.Unlock()

	batch, err := boardOps(s, tab, cards)
	if err != nil {
		s.setErr(err)
		s.publish()
		return
	}
	if len(batch) == 0 {
		return
	}
	// The error is already on the sync status by the time apply returns; the
	// board itself has moved regardless, which is the point of doing the
	// local stage first.
	_ = s.apply(batch)
}

// persist writes the joined workspace back to the config.
func (w *Workbench) persist() error {
	w.wsMu.Lock()
	var snapshot *config.Workspace
	if w.ws != nil {
		clone := *w.ws
		clone.Tabs = append([]config.Tab(nil), w.ws.Tabs...)
		snapshot = &clone
	}
	w.wsMu.Unlock()

	return config.Update(func(c *config.Config) { c.Workspace = snapshot })
}

// publishWorkspace tells the frontend what the workspace looks like now.
func (w *Workbench) publishWorkspace() {
	w.wsMu.Lock()
	view := w.viewLocked()
	w.wsMu.Unlock()

	w.emit(EventWorkspaceChanged, WorkspaceEvent{
		Workspace: view,
		Status:    w.SyncStatus(),
	})
}

// viewLocked builds the frontend's view of the workspace. Caller holds wsMu.
func (w *Workbench) viewLocked() *WorkspaceView {
	if w.ws == nil {
		return nil
	}
	tabs := make([]TabView, 0, len(w.ws.Tabs))
	for _, t := range w.ws.Tabs {
		// A folder can be deleted or unmounted between runs, and a tab that
		// points at one is no more usable than a tab that was never bound --
		// so it is reported the same way rather than failing at run time.
		tabs = append(tabs, TabView{
			ID:    t.ID,
			Name:  t.Name,
			Dir:   t.Dir,
			Bound: t.Dir != "" && isDir(t.Dir),
		})
	}
	return &WorkspaceView{
		ServerURL: w.ws.ServerURL,
		ID:        w.ws.ID,
		Name:      w.ws.Name,
		Actor:     w.ws.Actor,
		Tabs:      tabs,
		ActiveTab: w.ws.ActiveTab,
	}
}

// tabDirLocked is the folder for a tab, or the startup folder when the tab is
// unknown or unbound. Caller holds wsMu.
func (w *Workbench) tabDirLocked(tabID string) string {
	if tabID == "" || w.ws == nil {
		return w.baseDir
	}
	tab, ok := w.ws.Tab(tabID)
	if !ok || tab.Dir == "" || !isDir(tab.Dir) {
		return w.baseDir
	}
	return tab.Dir
}

// useWorkDir points agents at a folder.
func (w *Workbench) useWorkDir(dir string) {
	if dir == "" {
		return
	}
	w.mu.Lock()
	w.workDir = dir
	w.mu.Unlock()
}

// deleteTab removes a tab by ID, preserving order.
func deleteTab(tabs []config.Tab, id string) []config.Tab {
	out := tabs[:0]
	for _, t := range tabs {
		if t.ID == id {
			continue
		}
		out = append(out, t)
	}
	return out
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// mintID returns a random identifier for an actor, a tab, a node or an op.
//
// Random rather than sequential because these have to be unique across
// machines that have never spoken to each other: two people creating a tab
// while both offline must not produce the same ID, and there is nothing to
// coordinate on. 128 bits makes that collision not worth thinking about, and
// 32 hex characters is well inside the server's 128-character limit.
func mintID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("workbench: mint id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// cleanServerURL validates the server address the user typed.
func cleanServerURL(raw string) (string, error) {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", errors.New("workbench: empty server url")
	}
	// http:// is allowed for a server on a local network, but the default has
	// to be https: the write key is a bearer credential, and sending it in
	// clear because the user left the scheme off is not a default to have.
	if !strings.Contains(raw, "://") {
		return "https://" + raw, nil
	}
	if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
		return "", fmt.Errorf("workbench: unsupported server url %q", raw)
	}
	return raw, nil
}
