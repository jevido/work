package workbench

// File ownership.
//
// Specialists in one plan run at the same time, in one working tree, so two of
// them editing the same file is the one collision Work can see coming. Anton's
// plan names the files each step owns; this is where that claim is enforced.
//
// Two rules keep the office out of deadlock. Taking files is all-or-nothing at
// the start of a step, so nobody ever holds one file while waiting for another
// and a cycle cannot form. And a step that has given its files back to wait for
// somebody else stops counting as an owner, so the colleague it is waiting on
// is never left waiting for it in turn.

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// claims is who owns which files for the duration of one delegated plan. File
// ownership is a property of a single fan-out, not of the whole session.
type claims struct {
	mu sync.Mutex
	// held is what each agent has taken and is editing now.
	held map[string][]string
	// declared is what the plan says each agent will edit, whether they have
	// taken it yet or not. A step that reports itself blocked needs this: the
	// colleague holding it up may not have reached those files yet.
	declared map[string][]string
	// waiting marks an agent that has given its files back and is queueing
	// behind somebody else. It owns nothing while it waits, which is what
	// stops two blocked steps from waiting for each other forever.
	waiting map[string]bool
	// finished marks a step that will never take files again.
	finished map[string]bool
	// released is closed and replaced whenever ownership changes, which is how
	// a waiting step learns to try again. A broadcast rather than a channel
	// per waiter: changes are rare and waiters are few.
	released chan struct{}
}

// newClaims records what a plan intends to own before any of it runs.
func newClaims(steps []PlanStep) *claims {
	c := &claims{
		held:     make(map[string][]string),
		declared: make(map[string][]string, len(steps)),
		waiting:  make(map[string]bool),
		finished: make(map[string]bool),
		released: make(chan struct{}),
	}
	for _, step := range steps {
		if len(step.Files) > 0 {
			c.declared[step.AgentID] = step.Files
		}
	}
	return c
}

// take gives every path to agentID at once, or gives none of them and names
// the agent standing in the way.
//
// A step with no files takes nothing and is never blocked: an agent who is
// only reading, or only answering, does not need to own anything.
func (c *claims) take(agentID string, paths []string) (blockedBy string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.waiting, agentID)
	if len(paths) == 0 {
		return "", true
	}
	if holder := c.holderLocked(agentID, paths); holder != "" {
		c.waiting[agentID] = true
		return holder, false
	}
	// Re-taking is how a step resumes after being blocked, so add rather than
	// assume the agent is holding nothing.
	c.held[agentID] = append(c.held[agentID], paths...)
	return "", true
}

// finish releases an agent's files and records that it is not coming back for
// them, so anybody queueing on its declared paths can stop waiting.
func (c *claims) finish(agentID string) {
	c.mu.Lock()
	delete(c.held, agentID)
	delete(c.waiting, agentID)
	c.finished[agentID] = true
	c.wakeLocked()
	c.mu.Unlock()
}

// wait marks an agent as queueing rather than working. It owns nothing until
// it takes its files again.
func (c *claims) wait(agentID string) {
	c.mu.Lock()
	delete(c.held, agentID)
	c.waiting[agentID] = true
	c.wakeLocked()
	c.mu.Unlock()
}

func (c *claims) wakeLocked() {
	close(c.released)
	c.released = make(chan struct{})
}

// waiter returns a channel closed on the next change of ownership. Take it
// before checking whether you are blocked, or you can miss the release that
// would have let you through and then wait for one that never comes.
func (c *claims) waiter() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.released
}

// holder names the agent editing any of these paths right now, ignoring the
// asker's own claims. Empty means nobody has them.
func (c *claims) holder(asking string, paths []string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.holderLocked(asking, paths)
}

// pending names the agent a step should wait for: whoever holds these paths
// now, or whoever the plan says still intends to. An agent that is itself
// queueing does not count, which is what keeps two blocked steps from waiting
// for each other. Empty means there is nothing left to wait for.
func (c *claims) pending(asking string, paths []string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if holder := c.holderLocked(asking, paths); holder != "" {
		return holder
	}
	for agentID, owned := range c.declared {
		if agentID == asking || c.finished[agentID] || c.waiting[agentID] {
			continue
		}
		if anyConflict(paths, owned) {
			return agentID
		}
	}
	return ""
}

func (c *claims) holderLocked(asking string, paths []string) string {
	for agentID, owned := range c.held {
		if agentID == asking {
			continue
		}
		if anyConflict(paths, owned) {
			return agentID
		}
	}
	return ""
}

// heldByOthers lists what everybody except the asker is editing right now, so
// a step can be told what is off limits before it starts looking.
func (c *claims) heldByOthers(asking string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []string
	for agentID, owned := range c.held {
		if agentID == asking {
			continue
		}
		out = append(out, owned...)
	}
	sort.Strings(out)
	return out
}

func anyConflict(want, have []string) bool {
	for _, a := range want {
		for _, b := range have {
			if conflict(a, b) {
				return true
			}
		}
	}
	return false
}

// cleanPaths normalises what a model wrote into something comparable: repo
// -relative, forward slashes, no "./", no trailing slash, no duplicates.
// Anything empty or reaching outside the tree is dropped rather than guessed
// at.
func cleanPaths(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		p := cleanPath(raw)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func cleanPath(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.Trim(p, "`\"'")
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ""
	}
	// Clean would resolve a glob's meaning away in a path like a/*/../b, so
	// only plain paths are cleaned; a pattern is taken as written.
	if !strings.ContainsAny(p, "*?[") {
		p = path.Clean(p)
	}
	p = strings.TrimSuffix(p, "/")
	if p == "" || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// conflict reports whether two claimed paths could name the same file.
//
// It is deliberately generous: a directory conflicts with everything under it,
// and a glob conflicts with anything it matches. Two globs only conflict when
// one matches the other literally, which is the honest limit here -- deciding
// whether two arbitrary patterns can match the same name is not worth solving
// for a claim written by hand. Erring towards conflict costs a wait; erring
// the other way costs a lost edit.
func conflict(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if under(a, b) || under(b, a) {
		return true
	}
	return matches(a, b) || matches(b, a)
}

// under reports whether child sits inside parent.
func under(parent, child string) bool {
	return strings.HasPrefix(child, parent+"/")
}

// matches reports whether pattern covers name, treating a recursive pattern's
// root as covering the whole subtree so that src/** and src/a/b.ts conflict.
func matches(pattern, name string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	if ok, err := path.Match(pattern, name); err == nil && ok {
		return true
	}
	// path.Match's * does not cross a slash, so a recursive pattern needs the
	// prefix comparison: everything before the first wildcard is a directory
	// the pattern is rooted in.
	if i := strings.IndexAny(pattern, "*?["); i > 0 {
		root := strings.TrimSuffix(pattern[:i], "/")
		if root != "" && (root == name || under(root, name)) {
			return strings.Contains(pattern, "**")
		}
	}
	return false
}
