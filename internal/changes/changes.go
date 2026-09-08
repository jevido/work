// Package changes reports what a run did to the working tree, and undoes it.
//
// The Claude CLI has no hook that lets Work approve a write before it happens,
// so the gate sits after the fact instead: take a snapshot before a run, list
// what changed after it, show the diffs, and offer an exact revert.
//
// "Exact" is the important word. Reverting restores the file's content as it
// was when the run started, not its content at HEAD, so a file you had already
// edited before asking for help is restored to *your* version rather than
// thrown back to the last commit.
package changes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxStoredFile caps the size of a pre-run file kept in memory for reverting.
// Anything larger falls back to a git checkout, which is less exact but does
// not hold a large file for the length of a run.
const maxStoredFile = 4 << 20

// Change is one file a run touched.
type Change struct {
	// Path is relative to the working tree root.
	Path string `json:"path"`
	// Status is "modified", "added" or "deleted".
	Status string `json:"status"`
	// Patch is a unified diff, empty for a binary file.
	Patch string `json:"patch"`
	// Binary marks a file with no readable diff.
	Binary bool `json:"binary"`
	// Restorable is false when Work cannot put the file back exactly.
	Restorable bool `json:"restorable"`
}

// fileState is what a file looked like before the run.
type fileState struct {
	// existed is false for a path that was not on disk at all.
	existed bool
	// tracked is true when git knows the path, so a checkout can restore it.
	tracked bool
	// content is the pre-run bytes, kept only for a file that was already
	// dirty: a clean file can be restored from git instead.
	content []byte
	// stored is false when the file was too large to keep.
	stored bool
	hash   string
}

// Snapshot is the state of the working tree before a run.
type Snapshot struct {
	dir   string
	files map[string]fileState
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(ctx context.Context, dir string) bool {
	out, err := run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Take records the working tree so a later Diff can say what a run changed.
//
// Only files that are already modified or untracked are read: a clean file can
// be restored from git, so there is no reason to hold a copy of it.
func Take(ctx context.Context, dir string) (*Snapshot, error) {
	root, err := treeRoot(ctx, dir)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{dir: root, files: make(map[string]fileState)}
	dirty, err := dirtyPaths(ctx, root)
	if err != nil {
		return nil, err
	}
	for path, tracked := range dirty {
		snap.files[path] = readState(root, path, tracked)
	}
	return snap, nil
}

// Diff lists what changed since the snapshot was taken.
func (s *Snapshot) Diff(ctx context.Context) ([]Change, error) {
	if s == nil {
		return nil, nil
	}
	dirty, err := dirtyPaths(ctx, s.dir)
	if err != nil {
		return nil, err
	}

	var out []Change
	for path, tracked := range dirty {
		before, known := s.files[path]
		now := readState(s.dir, path, tracked)

		if known && before.hash == now.hash {
			// Dirty before the run and untouched by it: the user's own edit.
			continue
		}

		status := "modified"
		switch {
		case !now.existed:
			status = "deleted"
		case known && !before.existed, !known && !tracked:
			status = "added"
		}

		change := Change{
			Path:       path,
			Status:     status,
			Restorable: !known || before.stored || before.tracked,
		}
		change.Patch, change.Binary = patch(ctx, s.dir, path, tracked)
		out = append(out, change)
	}

	// Deleting a file that was dirty beforehand leaves no trace in git status,
	// so check the snapshot for anything that has since vanished.
	for path, before := range s.files {
		if _, stillDirty := dirty[path]; stillDirty {
			continue
		}
		if !before.existed {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.dir, path)); err == nil {
			continue
		}
		out = append(out, Change{
			Path:       path,
			Status:     "deleted",
			Restorable: before.stored,
		})
	}
	return out, nil
}

// Revert puts one file back the way it was when the snapshot was taken.
func (s *Snapshot) Revert(ctx context.Context, path string) error {
	if s == nil {
		return errors.New("changes: nothing was recorded for this run")
	}
	if err := safePath(path); err != nil {
		return err
	}
	full := filepath.Join(s.dir, path)

	before, known := s.files[path]
	switch {
	case known && before.existed && before.stored:
		// Exact restore of the content the run started with.
		return os.WriteFile(full, before.content, 0o644)

	case known && !before.existed:
		// The path was not there before the run.
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("changes: remove %s: %w", path, err)
		}
		return nil

	case !known:
		// Clean or absent at snapshot time. If git knows the file, its
		// committed state is exactly the pre-run state; otherwise the run
		// created it.
		if tracked(ctx, s.dir, path) {
			if _, err := run(ctx, s.dir, "checkout", "--", path); err != nil {
				return fmt.Errorf("changes: restore %s: %w", path, err)
			}
			return nil
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("changes: remove %s: %w", path, err)
		}
		return nil

	default:
		// Was dirty but too large to keep. A checkout is the best available,
		// and Diff has already flagged this as not exactly restorable.
		if before.tracked {
			if _, err := run(ctx, s.dir, "checkout", "--", path); err != nil {
				return fmt.Errorf("changes: restore %s: %w", path, err)
			}
			return nil
		}
		return fmt.Errorf("changes: %s was too large to record; revert it yourself", path)
	}
}

// dirtyPaths lists every modified or untracked file, mapped to whether git
// tracks it.
func dirtyPaths(ctx context.Context, root string) (map[string]bool, error) {
	out, err := run(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return nil, err
	}

	paths := make(map[string]bool)
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		path := entry[3:]
		// A rename reads "R  old -> new"; the -z form splits them into
		// separate entries, so take whatever this one names.
		if idx := strings.Index(path, "\x00"); idx >= 0 {
			path = path[:idx]
		}
		paths[path] = code != "??"
	}
	return paths, nil
}

func readState(root, path string, isTracked bool) fileState {
	state := fileState{tracked: isTracked}

	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return state
	}
	state.existed = true

	sum := sha256.Sum256(data)
	state.hash = hex.EncodeToString(sum[:])
	if len(data) <= maxStoredFile {
		state.content = data
		state.stored = true
	}
	return state
}

// patch produces a unified diff for one file. Reports whether the file is
// binary, in which case there is nothing to show.
func patch(ctx context.Context, root, path string, isTracked bool) (string, bool) {
	if isTracked {
		out, err := run(ctx, root, "diff", "--no-color", "-U3", "--", path)
		if err != nil {
			return "", false
		}
		return out, strings.Contains(out, "Binary files ")
	}

	// An untracked file has nothing to diff against, so compare it to nothing.
	// git exits non-zero when the files differ, which is the normal case here.
	out, _ := run(ctx, root, "diff", "--no-color", "-U3", "--no-index", "--", os.DevNull, path)
	return out, strings.Contains(out, "Binary files ")
}

func tracked(ctx context.Context, root, path string) bool {
	_, err := run(ctx, root, "ls-files", "--error-unmatch", "--", path)
	return err == nil
}

func treeRoot(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("changes: %s is not a git working tree: %w", dir, err)
	}
	return strings.TrimSpace(out), nil
}

// safePath refuses anything that could reach outside the working tree. The
// path arrives from the frontend, which is untrusted input.
func safePath(path string) error {
	if path == "" {
		return errors.New("changes: empty path")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("changes: %s must be relative to the repository", path)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("changes: %s escapes the repository", path)
	}
	return nil
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}
