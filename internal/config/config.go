// Package config stores the small amount of state Work keeps between runs.
//
// There is very little worth persisting -- where the user's agent folders
// live, what those agents are allowed to do, and which project they were last
// pointed at -- so this is a single JSON file rather than a settings system.
// Everything else about an agent is on disk under that root, which makes the
// config file cheap to lose: pick the folder again and the team comes back.
//
// The one thing here that is not cheap to lose is the workspace write key, so
// the file is written 0600. Everything else in it is a path or a preference.
// Anything that changes as often as a run does -- the unsynced op queue, the
// server cursor -- is deliberately not in here; see StateDir.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Config is the persisted file. New fields belong here rather than in a second
// file; a single small read at startup is the whole budget for this.
type Config struct {
	// Root is the folder the user picked. It contains agents/. Empty means the
	// user has not chosen one yet, which is the first-run state.
	Root string `json:"root,omitempty"`
	// PermissionMode is what agents are allowed to do, as one of the modes in
	// internal/claude. Empty means the default, which is what a config file
	// written before this field existed says.
	PermissionMode string `json:"permissionMode,omitempty"`
	// WorkDir is the last directory Work was deliberately started from -- the
	// project agents were last pointed at. It exists for launches that carry
	// no such directory of their own; see StartDir.
	WorkDir string `json:"workDir,omitempty"`
	// Workspace is the shared workbench this machine has joined, or nil when
	// it has joined none. Nil is the ordinary case and the one Work has always
	// had: no workspace means no server, no queue and no ops.
	Workspace *Workspace `json:"workspace,omitempty"`
}

// Workspace is a shared workbench: a server, an identity on it, and the local
// folders this machine has bound its tabs to.
//
// The split matters. ServerURL, ID, WriteKey and Actor describe the shared
// thing and are the same on every machine that joined it. Tabs are the exact
// opposite -- a tab's Dir is one person's checkout of one project, and it is
// never sent anywhere. The server knows tab IDs; it does not know where anyone
// keeps their code.
type Workspace struct {
	// ServerURL is the workspace server, e.g. https://work.jevido.app.
	ServerURL string `json:"serverUrl"`
	// ID identifies the workspace on that server.
	ID string `json:"id"`
	// Name is what the workspace was created as, for the UI to show.
	Name string `json:"name,omitempty"`
	// WriteKey authorises pushes and reads. This is the secret the 0600 in
	// Save exists for: anyone holding it can write to the workspace.
	WriteKey string `json:"writeKey,omitempty"`
	// ReadKey is the key that can see the workspace and not change it -- the
	// one to paste into the web viewer. The server hands both out once, when
	// the workspace is created, and cannot show either again; a machine that
	// joined with a write key it was given has no read key to keep.
	ReadKey string `json:"readKey,omitempty"`
	// Actor identifies this machine within the workspace. It is minted on
	// join, never reused, and it prefixes every op ID this machine produces --
	// which is what makes op IDs unique across peers without coordination.
	Actor string `json:"actor,omitempty"`
	// Tabs are the workspace's tabs and the local folder each is bound to on
	// this machine. A tab with an empty Dir is joined but not yet bound: it
	// shows up in the UI and asks for a folder before it can run anything.
	Tabs []Tab `json:"tabs,omitempty"`
	// ActiveTab is the tab whose folder agents currently run in.
	ActiveTab string `json:"activeTab,omitempty"`
}

// Tab is one tab of a workspace as this machine sees it.
//
// ID and Name are shared; Dir is not. Two people with the same tab open are
// looking at the same conversation and the same board while running agents in
// their own checkout, which is the whole point of binding the folder locally
// rather than syncing a path that would be wrong on every other machine.
type Tab struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Dir is this machine's project folder for the tab. Empty means unbound.
	Dir string `json:"dir,omitempty"`
}

// Tab returns the tab with the given ID.
func (w *Workspace) Tab(id string) (Tab, bool) {
	if w == nil {
		return Tab{}, false
	}
	for _, t := range w.Tabs {
		if t.ID == id {
			return t, true
		}
	}
	return Tab{}, false
}

// SetTab adds or replaces a tab, preserving order so the UI does not reshuffle
// on every edit.
func (w *Workspace) SetTab(t Tab) {
	for i := range w.Tabs {
		if w.Tabs[i].ID == t.ID {
			w.Tabs[i] = t
			return
		}
	}
	w.Tabs = append(w.Tabs, t)
}

// StartDir chooses the directory Work runs agents in for this launch, and
// reports whether that choice is worth writing back to the config.
//
// A terminal launch answers the question by itself: the directory you cd'd
// into before typing `work` is the project you meant. A desktop launcher does
// not -- it starts the process in $HOME, or in "/", which is not a project and
// not somewhere agents should be turned loose. So cwd is taken at face value
// when it looks like a deliberate choice, and the last directory that did
// stands in when it does not.
//
// saved is a fallback only, and only while it still exists: a project that has
// been moved or deleted since is a worse answer than the cwd we already have.
func StartDir(cwd, saved string) (dir string, remember bool) {
	cwd = filepath.Clean(cwd)
	if deliberate(cwd) {
		// Worth remembering only when it is news. The common case is starting
		// Work from the same project twice, and that should not write a file.
		return cwd, cwd != saved
	}
	if saved != "" && isDir(saved) {
		return saved, false
	}
	// Nothing better to offer than where we already are. This is the old
	// behaviour, and it is also the honest first run from a launcher: $HOME,
	// no git repository, no review -- but a window that opens.
	return cwd, false
}

// deliberate reports whether dir looks like somewhere a person chose to start
// Work, rather than somewhere a launcher dropped it.
func deliberate(dir string) bool {
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Nothing to compare against. Trusting dir is better than refusing to
		// run anywhere, and a machine with no home directory is not the case
		// this guard is for.
		return true
	}
	return dir != filepath.Clean(home)
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Path returns the config file location, honouring XDG_CONFIG_HOME so a user
// who has moved their config directory is not surprised by a stray ~/.config.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locate config dir: %w", err)
	}
	return filepath.Join(dir, "work", "config.json"), nil
}

// StateDir returns the directory Work keeps per-workspace runtime state in:
// the queue of ops that have not reached the server yet, and the cursor
// marking how far this machine has read the server's log.
//
// This is deliberately not the config file. Both of those change at the rate
// work happens rather than at the rate settings change, and Save rewrites the
// whole config -- so putting a queue in there would mean re-encoding every
// setting the user has on every op. It is also not the cache directory: a
// wiped cache would silently throw away work that has not been sent yet.
func StateDir(workspaceID string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locate config dir: %w", err)
	}
	if workspaceID == "" {
		return "", errors.New("config: empty workspace id")
	}
	// The ID is hashed rather than used directly. The server says its IDs are
	// opaque -- a prefix and nothing else promised -- so anything that
	// depended on one being a safe path element would be depending on
	// something it was told not to. A hash is a legal directory name for
	// every possible ID, including ones with a slash in them, and it needs no
	// validation that could turn a perfectly good workspace into an error.
	sum := sha256.Sum256([]byte(workspaceID))
	return filepath.Join(dir, "work", "workspaces", hex.EncodeToString(sum[:8])), nil
}

// Load reads the config. A missing file is not an error: it is the first run,
// and the zero Config describes it exactly. A corrupt file is an error, because
// silently overwriting a user's settings is worse than telling them.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return c, nil
}

// Update reads the config, applies mutate and writes the result back.
//
// This is how a single setting is changed: Save writes the whole file, so a
// caller that built a Config out of the one field it knows about would erase
// the others. Not atomic against a second process, which is not a case Work
// has -- one window, one config.
func Update(mutate func(*Config)) error {
	c, err := Load()
	if err != nil {
		return err
	}
	mutate(&c)
	return Save(c)
}

// Save writes the config, creating the directory if needed.
//
// The write goes to a temporary file first and is then renamed over the target,
// so a crash mid-write cannot leave a truncated file that Load would reject.
//
// The mode is set explicitly rather than left to whatever the file already
// had. os.CreateTemp happens to make 0600 files, but the config now holds a
// workspace write key, and "the secret is protected because of a default in
// another package" is not a property worth relying on -- especially over a
// config file that predates the key and may already exist as 0644.
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return fmt.Errorf("config: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // No-op once the rename below has succeeded.

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: protect %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	return nil
}
