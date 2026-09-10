// Package config stores the small amount of state Work keeps between runs.
//
// There is very little worth persisting -- where the user's agent folders
// live, what those agents are allowed to do, and which project they were last
// pointed at -- so this is a single JSON file rather than a settings system.
// Everything else about an agent is on disk under that root, which makes the
// config file cheap to lose: pick the folder again and the team comes back.
package config

import (
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
