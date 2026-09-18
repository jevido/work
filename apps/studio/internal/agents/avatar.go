package agents

// Avatars are the one part of an agent's folder that has to leave the disk as
// bytes rather than as text: a webview cannot open a filesystem path, so
// something has to hand it the picture. Scan reports where the file is; this
// is how a caller finds that file again from an agent's id, safely.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// maxAgentIDLen bounds an id before it is ever joined onto a path. Folder
// names come off disk and ids come off a URL; neither is worth a syscall at
// this length.
const maxAgentIDLen = 128

// AvatarPath returns the file holding one agent's avatar under a config root,
// or "" when that agent has none -- which is the ordinary case, not an error.
//
// The returned path is resolved and known to be inside the root's agents
// folder. Both inputs are treated as untrusted: the id may have arrived from a
// URL, and the folder it names arrived from disk and may be a symlink pointing
// somewhere else entirely. An agent folder that leads out of the config folder
// is refused rather than followed, so this can never become a way to read the
// rest of the filesystem.
//
// Only the two names in avatarNames are ever returned, so the rest of an
// agent's folder -- their PERSONALITY.md, their skills -- is not reachable
// through here either.
func AvatarPath(root, id string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("agents: no config root")
	}
	if !validAgentID(id) {
		return "", fmt.Errorf("agents: %q does not name an agent", id)
	}

	dir := AgentsDir(root)
	path := avatarIn(filepath.Join(dir, id))
	if path == "" {
		return "", nil
	}

	// Containment is checked on the resolved paths, not on the ones that were
	// joined: agents/<id> being a symlink is exactly the case a string prefix
	// check would wave through.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("agents: resolve %s: %w", dir, err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("agents: resolve %s: %w", path, err)
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("agents: %s is outside %s", realPath, realDir)
	}
	return realPath, nil
}

// validAgentID reports whether id could name a folder directly under agents/.
//
// One path element, nothing hidden: the same rule isAgentFolder applies when
// scanning, so an id this rejects could not have been an agent anyway. It also
// happens to rule out "..", "." and anything with a separator in it.
func validAgentID(id string) bool {
	if id == "" || len(id) > maxAgentIDLen {
		return false
	}
	if strings.HasPrefix(id, ".") {
		return false
	}
	if strings.ContainsAny(id, `/\`+"\x00") {
		return false
	}
	return id == filepath.Base(id)
}
