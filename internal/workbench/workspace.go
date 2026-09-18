package workbench

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
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
	// A local workspace needs no transport: there is no server in it to
	// reach. Requiring one here would mean a build without a transport, or a
	// machine whose transport failed to wire, opened to no workspace at all --
	// losing the tabs and the outline of somebody who never asked for a
	// server in the first place.
	if w.ops == nil && ws.ServerURL != "" {
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

// CreateLocalWorkspace makes a workspace that lives only on this machine.
//
// It is what setup does, and it is the reason opening Work for the first time
// puts you in something you can use rather than in front of a form. Tabs, the
// outline, the board and the merge all work; what is missing is the loop that
// talks to a server, because there is no server in it. See Sync.local.
//
// The ID is minted here rather than handed out by anyone. It never leaves this
// machine -- it names a folder under the state directory and nothing else --
// so there is nobody to collide with and nothing to coordinate.
//
// A first tab comes with it, bound to the folder Work was started in. A
// workspace whose only tab has no folder can run no agents, which is the same
// dead end from one step further along.
func (w *Workbench) CreateLocalWorkspace(name string) (*WorkspaceView, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("workbench: empty workspace name")
	}

	id, err := mintID()
	if err != nil {
		return nil, err
	}
	actor, err := mintID()
	if err != nil {
		return nil, err
	}
	tab, err := w.firstTab(name)
	if err != nil {
		return nil, err
	}
	tabID := tab.ID

	ws := &config.Workspace{
		ID:        "local_" + id,
		Name:      name,
		Actor:     actor,
		Tabs:      []config.Tab{tab},
		ActiveTab: tab.ID,
	}
	if err := w.adopt(ws, true); err != nil {
		return nil, err
	}

	w.writeFirstTab(tabID, name)
	w.publishWorkspace()
	return w.Workspace(), nil
}

// firstTab is the tab a newly made workspace opens with.
//
// Every workspace gets one, however it was made. A workspace with no tabs is
// joined, syncing and completely blank: there is nowhere to type, nothing
// across the top, and the window is the one it shows when nothing is joined at
// all -- so "created" and "not created" look identical. CreateWorkspace used
// to do exactly that, and the only reason it was not noticed sooner is that
// the local path, which setup uses, had this and the shared one did not.
//
// Bound to the folder Work was started in, which is the folder the app has
// always run agents in with no workspace at all. That keeps making a workspace
// to one question. A colleague who joins later gets this tab from the document
// unbound, and binds it to their own checkout, which is right: a tab's folder
// is one person's and is never sent anywhere.
func (w *Workbench) firstTab(name string) (config.Tab, error) {
	id, err := mintID()
	if err != nil {
		return config.Tab{}, err
	}
	return config.Tab{ID: id, Name: name, Dir: w.baseDir}, nil
}

// writeFirstTab puts a newly made workspace's tab into its document.
//
// Separate from the config entry above because they fail differently. The
// config entry is the tab: without it there is no tab. This is the copy that
// travels, so that a workspace shared with a server carries its tab rather
// than arriving empty -- and a failure here is worth swallowing, because the
// tab is in the config, it is on screen, and it works. The document is rebuilt
// from the journal, so what is lost is a tab missing from a share that has not
// happened yet.
//
// adopt has already installed the Sync by the time this is called, so it is
// the same path NewTab takes.
func (w *Workbench) writeFirstTab(id, name string) {
	s := w.sync.Load()
	if s == nil {
		return
	}
	if batch, err := tabOps(s, id, name, false); err == nil {
		_ = s.apply(batch)
	}
}

// CreateWorkspace makes a workspace on a server and joins it.
//
// signupToken is the server's own WORK_SIGNUP_TOKEN, not a workspace key --
// there is no workspace to authenticate against yet, and it is empty against
// the ordinary server, which does not gate creation. This call is the only
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
	// An empty signup token is allowed and is the common case: a server with
	// no WORK_SIGNUP_TOKEN set has open signup, and the empty bearer is what
	// tells it so. A server that does gate creation answers 401, which is a
	// better place for a missing token to be refused than here -- this side
	// cannot know which kind of server it is talking to.
	signupToken = strings.TrimSpace(signupToken)
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
	// The same first tab a local workspace gets. Without it a freshly created
	// workspace is joined and blank -- no tabs across the top, and the window
	// showing the state it shows when nothing is joined -- which reads as the
	// create having failed.
	tab, err := w.firstTab(cmp.Or(created.Workspace.Name, name))
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
		Tabs:      []config.Tab{tab},
		ActiveTab: tab.ID,
		// Held, and only for workspaces made from here on. Flipping the
		// default for every existing one would mean an update landing and
		// nothing reaching anybody's colleagues for a week, with one word in a
		// badge as the only sign. A workspace somebody makes after this ships
		// starts the way they asked for; one they already had keeps what they
		// have been living with, and the setting is one switch away.
		HoldPush: true,
	}
	if err := w.adopt(ws, true); err != nil {
		return nil, err
	}
	w.writeFirstTab(tab.ID, tab.Name)
	w.publishWorkspace()
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
		// See CreateWorkspace: new to this machine, so it starts held.
		HoldPush: true,
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

	// Before the loop starts, so a restored workspace never gets one cycle of
	// pushing in before it learns it was told not to.
	s.setHold(ws.HoldPush)

	if ctx := w.syncCtx.Load(); ctx != nil {
		s.start(*ctx)
	}

	if save {
		if err := w.persist(); err != nil {
			return err
		}
	}
	w.publishWorkspace()

	// The journal has already been replayed by newSync, so the active tab's
	// plan is readable before a single byte has crossed the network. Adopting
	// it here is what makes opening Work on a plane show the tasks you left
	// rather than an empty board.
	w.adoptTasks()
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

// KnownWorkspace is a workspace this machine holds keys for, joined or not.
//
// The keys are on it, and that is the whole point: the server keeps hashes and
// cannot show a key again, so the copy on this machine is the only one there
// is. A panel that could not show it would be a panel that cannot answer the
// one question anybody opens it with.
type KnownWorkspace struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	ServerURL string `json:"serverUrl,omitempty"`
	WriteKey  string `json:"writeKey,omitempty"`
	ReadKey   string `json:"readKey,omitempty"`
	// Joined marks the one this machine is syncing with, so the panel can say
	// which of them is on screen behind it.
	Joined bool `json:"joined"`
}

// KnownWorkspaces lists every workspace whose keys this machine has kept.
//
// Joined first, then by name, because the joined one is the one somebody is
// most often looking for and a list that reorders itself as workspaces are
// joined and left is a list nobody can point at.
//
// The joined workspace is in here even when the config has not caught up --
// creating one writes it on the way out and there is a window before that --
// so the list is built from the config and then corrected from what is
// actually open.
func (w *Workbench) KnownWorkspaces() ([]KnownWorkspace, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("workbench: read config: %w", err)
	}

	w.wsMu.Lock()
	var joined *config.Workspace
	if w.ws != nil {
		clone := *w.ws
		joined = &clone
	}
	w.wsMu.Unlock()

	out := make([]KnownWorkspace, 0, len(cfg.KnownWorkspaces)+1)
	seen := false
	for _, k := range cfg.KnownWorkspaces {
		entry := KnownWorkspace{
			ID:        k.ID,
			Name:      k.Name,
			ServerURL: k.ServerURL,
			WriteKey:  k.WriteKey,
			ReadKey:   k.ReadKey,
		}
		if joined != nil && joined.ID == k.ID {
			entry.Joined = true
			seen = true
			// Whatever is open wins over what was written down: a read key
			// minted a moment ago is on the workspace before it is in the
			// config.
			if joined.ReadKey != "" {
				entry.ReadKey = joined.ReadKey
			}
			if joined.WriteKey != "" {
				entry.WriteKey = joined.WriteKey
			}
			if joined.Name != "" {
				entry.Name = joined.Name
			}
		}
		out = append(out, entry)
	}
	if !seen && joined != nil && joined.ServerURL != "" && joined.WriteKey != "" {
		out = append(out, KnownWorkspace{
			ID:        joined.ID,
			Name:      joined.Name,
			ServerURL: joined.ServerURL,
			WriteKey:  joined.WriteKey,
			ReadKey:   joined.ReadKey,
			Joined:    true,
		})
	}

	slices.SortStableFunc(out, func(a, b KnownWorkspace) int {
		if a.Joined != b.Joined {
			if a.Joined {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// ForgetWorkspace takes a workspace's keys off this machine.
//
// The one this machine is joined to is refused: forgetting its key while still
// syncing with it would leave a workspace open that nothing could ever rejoin,
// and the call that means "stop being in this workspace" is LeaveWorkspace.
func (w *Workbench) ForgetWorkspace(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("workbench: no workspace id")
	}

	w.wsMu.Lock()
	joined := w.ws != nil && w.ws.ID == id
	w.wsMu.Unlock()
	if joined {
		return errors.New("workbench: this is the workspace you are in — leave it first")
	}

	return config.Update(func(c *config.Config) { c.Forget(id) })
}

// MintKeyForWorkspace issues another key for any workspace whose write key
// this machine holds, joined or not.
//
// Minting needs the workspace's write key and its server, and both are in the
// remembered entry -- so "get me a read key to send somebody" works for a
// workspace this machine has left, which is exactly when the read key is the
// thing that was never written down. A read key is kept for the same reason
// MintKey keeps one: the server will not show it again.
func (w *Workbench) MintKeyForWorkspace(ctx context.Context, id, access string) (string, error) {
	id = strings.TrimSpace(id)
	access = strings.TrimSpace(access)
	if access != "read" && access != "write" {
		return "", fmt.Errorf("workbench: %q is not an access level", access)
	}
	if w.ops == nil {
		return "", ErrNoTransport
	}

	w.wsMu.Lock()
	joined := w.ws != nil && w.ws.ID == id
	w.wsMu.Unlock()
	if joined {
		// One path for the workspace that is open, so what it mints is
		// written to the workspace as well as to the remembered entry.
		return w.MintKey(ctx, access)
	}

	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("workbench: read config: %w", err)
	}
	known, ok := cfg.KnownWorkspace(id)
	if !ok {
		return "", errors.New("workbench: no keys for that workspace on this machine")
	}
	if known.ServerURL == "" || known.WriteKey == "" {
		return "", errors.New("workbench: that workspace has no server and no key to ask with")
	}

	key, err := w.ops.Mint(ctx, known.ServerURL, known.WriteKey, access)
	if err != nil {
		return "", fmt.Errorf("workbench: mint %s key: %w", access, err)
	}
	if key == "" {
		return "", errors.New("workbench: the server returned an empty key")
	}
	if access == "read" {
		known.ReadKey = key
		if err := config.Update(func(c *config.Config) { c.Remember(known) }); err != nil {
			// The key is in the caller's hands and the server will not show it
			// again, so this is worth less than what it would cost to fail.
			return key, nil
		}
	}
	return key, nil
}

// EnsureKeys returns the workspace's keys, minting the read key if this
// machine has not got one.
//
// What "show me the keys" should have meant all along. A machine that joined
// with a write key has no read key -- the server keeps only hashes, so there
// is no old one to show -- and the panel used to say so and offer a button.
// That reads as though looking at the keys is a thing that changes them, and
// the button said "make a fresh one" even when one already existed, which
// reads as though every look issues a new key.
//
// It does not. Minting takes nothing away: every key already in use keeps
// working, and a read key, once minted, is written to the config -- so this
// mints at most once per machine and answers with the same pair forever after.
//
// A failure to mint is not an error here. The write key is the one this machine
// syncs with and is worth showing on its own, and a panel that refuses to open
// because the network is down would be worse than one that opens with a field
// it cannot fill.
func (w *Workbench) EnsureKeys(ctx context.Context) (Keys, error) {
	keys, err := w.Keys()
	if err != nil {
		return Keys{}, err
	}
	if keys.ReadKey != "" || w.ops == nil {
		return keys, nil
	}

	w.wsMu.Lock()
	shared := w.ws != nil && w.ws.ServerURL != ""
	w.wsMu.Unlock()
	if !shared {
		// A workspace that lives only on this machine has no server to ask and
		// no keys to hand out. Not an error: it is most workspaces.
		return keys, nil
	}

	read, err := w.MintKey(ctx, "read")
	if err != nil {
		return keys, nil
	}
	keys.ReadKey = read
	return keys, nil
}

// MintKey asks the server for another key for the joined workspace.
//
// The way a key is ever got hold of again, and a mint rather than a read
// because there is nothing to read: the server stores hashes, so the plaintext
// handed out at creation exists only wherever it was written down. A machine
// that joined with a write key never had the read key at all, and this is how
// it gets one to send somebody.
//
// A minted read key is kept. It is the same thing the creating machine already
// has in its config, so keeping it means "show me the read key" answers
// instantly forever after rather than issuing a new one every time somebody
// opens the panel. A minted write key is not kept: this machine already has one
// that works, and replacing it would be swapping a credential that is in use
// for one that has never been tried.
func (w *Workbench) MintKey(ctx context.Context, access string) (string, error) {
	access = strings.TrimSpace(access)
	if access != "read" && access != "write" {
		return "", fmt.Errorf("workbench: %q is not an access level", access)
	}
	if w.ops == nil {
		return "", ErrNoTransport
	}

	w.wsMu.Lock()
	ws := w.ws
	w.wsMu.Unlock()
	if ws == nil {
		return "", errors.New("workbench: no workspace joined")
	}
	if ws.ServerURL == "" {
		return "", errors.New("workbench: this workspace is only on this machine, so it has no keys")
	}

	key, err := w.ops.Mint(ctx, ws.ServerURL, ws.WriteKey, access)
	if err != nil {
		return "", fmt.Errorf("workbench: mint %s key: %w", access, err)
	}
	if key == "" {
		return "", errors.New("workbench: the server returned an empty key")
	}

	if access == "read" {
		w.wsMu.Lock()
		if w.ws != nil {
			w.ws.ReadKey = key
		}
		w.wsMu.Unlock()
		if err := w.persist(); err != nil {
			// The key is good and is already in the caller's hands; failing
			// here would throw away something the server will not show again.
			// The cost of not writing it down is one more mint next time.
			return key, nil
		}
	}
	return key, nil
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

// SaveNow sends what this machine has been holding back.
//
// The other half of HoldPush. It is not a one-shot: the flag it sets clears
// when a push finds the outbox empty, so a Save pressed on a bad connection
// keeps trying instead of failing once and quietly going back to holding. See
// Sync.saveNow.
func (w *Workbench) SaveNow() (Status, error) {
	s := w.sync.Load()
	if s == nil {
		return Status{}, errors.New("workbench: no workspace joined")
	}
	if s.local() {
		return s.status(), errors.New("workbench: this workspace is only on this machine, so there is nothing to save")
	}
	s.saveNow()
	return s.status(), nil
}

// SetHoldPush decides whether this workspace pushes as it goes or waits to be
// told.
//
// Per workspace and written down, because it is a decision about one board and
// not a preference about the app: a workspace you share with four people and
// one you are thinking in alone want opposite answers, and joining a new one
// should start from the default rather than inherit either.
func (w *Workbench) SetHoldPush(hold bool) (Status, error) {
	s := w.sync.Load()
	if s == nil {
		return Status{}, errors.New("workbench: no workspace joined")
	}
	if s.local() {
		return s.status(), errors.New("workbench: this workspace is only on this machine, so there is nothing to hold back")
	}

	w.wsMu.Lock()
	if w.ws != nil {
		w.ws.HoldPush = hold
	}
	w.wsMu.Unlock()

	if err := w.persist(); err != nil {
		return s.status(), err
	}
	s.setHold(hold)
	if !hold {
		// Turning it off is somebody saying "send it": waiting up to three
		// seconds for the poll would make the switch look broken.
		s.nudge()
	}
	w.publishWorkspace()
	return s.status(), nil
}

// SyncNow asks for a push and a pull immediately rather than at the next
// poll. It is what the retry button on an offline indicator does, and it is
// also how a workspace left in SyncRejected is retried once the key is fixed.
func (w *Workbench) SyncNow() {
	if s := w.sync.Load(); s != nil {
		s.nudge()
	}
}

// ResetToServer discards everything this machine has not had accepted and
// reads the workspace back from the server.
//
// The way out of a machine whose copy has gone wrong: ops the server will
// never take, a document that disagrees with what everybody else is looking
// at, or simply an afternoon's work somebody would rather drop than merge.
// Nothing here is recoverable afterwards -- the unsent ops are deleted, not
// parked -- so the caller asks first.
//
// A workspace that lives only on this machine is refused rather than emptied.
// There is no other copy of it, so "take the server's" would mean "delete
// everything", and a button that reads one way and does the other is the
// worst kind of destructive.
func (w *Workbench) ResetToServer(ctx context.Context) error {
	s := w.sync.Load()
	if s == nil {
		return errors.New("workbench: no workspace joined")
	}
	if err := s.resetToServer(ctx); err != nil {
		return err
	}

	// The document is a different document now: tabs a colleague retired are
	// gone from it, and the plan behind the board is whatever the server's log
	// says. absorb is what the sync loop runs after somebody else's ops land,
	// and this is the same situation with a bigger page.
	w.absorb(s)
	w.publishWorkspace()
	return nil
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

	// A bound folder is the condition the board's source waits on, so the
	// plan arrives the moment the tab has somewhere to run.
	if active {
		w.adoptTasks()
	}
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

	// And then the new tab's own plan. Order matters: republishing first
	// means the cards that were already here keep their place, and the tasks
	// adopted below are added after them rather than interleaved.
	w.adoptTasks()
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

	// Unconditionally, and before the tab bookkeeping below returns early: a
	// page of ops that changed no tab at all routinely changes the plan, and
	// that is the board's source.
	w.adoptTasks()

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
	// The progress of any card adopted from a plan task, written back onto
	// the task itself. Appended to the same batch rather than applied
	// separately so a status change is still one fsync, whether or not the
	// card came from the plan.
	progress, err := taskOps(s, tab, w.taskLinks(), cards)
	if err != nil {
		s.setErr(err)
		s.publish()
		return
	}
	batch = append(batch, progress...)
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

	return config.Update(func(c *config.Config) {
		c.Workspace = snapshot
		// The keys, kept beyond the workspace they belong to.
		//
		// A key exists in exactly one place once the server has handed it out
		// -- the server keeps hashes and cannot show it again -- so a machine
		// that leaves a workspace and holds its key only in the entry it just
		// overwrote has lost it. They are not secret: they are handed out on
		// purpose, a few at a time, and this is the file that is already 0600
		// for the one that is in use.
		if snapshot != nil && snapshot.ServerURL != "" {
			c.Remember(config.Known{
				ID:        snapshot.ID,
				Name:      snapshot.Name,
				ServerURL: snapshot.ServerURL,
				WriteKey:  snapshot.WriteKey,
				ReadKey:   snapshot.ReadKey,
			})
		}
	})
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
