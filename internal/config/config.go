// Package config stores the small amount of state Work keeps between runs.
//
// There is exactly one thing worth persisting so far -- where the user's agent
// folders live -- so this is a single JSON file rather than a settings system.
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
