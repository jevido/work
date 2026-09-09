package agents

// Agents live on disk, one folder each, under a root the user picks. The
// folder is the definition: its name is the agent, PERSONALITY.md is who they
// are, skills/ is what they will eventually know how to do. Nothing here
// depends on the UI, so an agent added in an editor is a first-class agent.

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// The on-disk layout. These names are part of the user's interface: they are
// what someone sees in a file manager, so they are spelled for a human.
const (
	// AgentsDirName is the folder under the config root that holds agents.
	AgentsDirName = "agents"
	// PersonalityFileName is read and prepended to an agent's system prompt
	// every time they are given work.
	PersonalityFileName = "PERSONALITY.md"
	// SkillsDirName is reserved for per-agent skill files. Work only
	// guarantees it exists; nothing reads it yet.
	SkillsDirName = "skills"
	// CoordinatorFolder is the one agent Work insists on: without a
	// coordinator there is nobody to hand a task to.
	CoordinatorFolder = "anton"
)

// avatarNames are the avatar filenames looked for in an agent's folder, in
// preference order. webp first because it is the smaller of the two for the
// same picture, and the office draws these at desk size.
var avatarNames = []string{"avatar.webp", "avatar.png"}

// MaxPersonalityBytes bounds what a PERSONALITY.md may contribute to a system
// prompt. Every task an agent runs pays for this file in tokens, so a file
// that grew by accident is truncated rather than billed for.
const MaxPersonalityBytes = 32 << 10

// maxSummaryBytes bounds the roster line derived from a personality file. The
// coordinator's routing prompt lists every agent, so this is per-agent cost on
// every single run.
const maxSummaryBytes = 200

// fallbackColours dress agents that are not part of the built-in team. Picked
// by hash of the agent ID, so an agent keeps its colour across restarts
// without anything being written down.
var fallbackColours = []string{
	"#e08b6f", "#b98cd8", "#6fc4d8", "#d8c26f", "#8fd67a",
	"#d87a9e", "#7f9ad8", "#c9a06f",
}

// AgentsDir is the folder holding agent folders under a config root.
func AgentsDir(root string) string { return filepath.Join(root, AgentsDirName) }

// Ensure prepares a config root so that Scan will find a usable team.
//
// It is idempotent and safe to call on every startup and every reload, which
// is the point: it is also what repairs a folder somebody half-created by
// hand. Seeding stops at the coordinator: a new root gets agents/anton and
// nothing else, because the team is the user's to author. Recreating a
// built-in is a mkdir -- Scan restores its structure from the folder name.
func Ensure(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("agents: empty config root")
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("agents: config root %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("agents: config root %s is not a directory", root)
	}

	dir := AgentsDir(root)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agents: create %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}

	// Only the coordinator is guaranteed, fresh root or not: Scan cannot
	// build a team without one, and everyone else is the user's to add.
	if a, ok := Default().Get(CoordinatorFolder); ok {
		if err := writeAgentFolder(filepath.Join(dir, a.ID), a.Name, a.SystemPrompt); err != nil {
			return err
		}
	}

	// Anything the user added by hand is completed rather than rejected: a
	// bare folder with a name in it is a reasonable way to ask for an agent.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if !isAgentFolder(e) {
			continue
		}
		if err := writeAgentFolder(filepath.Join(dir, e.Name()), displayName(e.Name()), ""); err != nil {
			return err
		}
	}
	return nil
}

// writeAgentFolder creates one agent folder, its skills/ directory and, if it
// has none, a starter PERSONALITY.md. An existing personality file is never
// touched: it is the user's document.
func writeAgentFolder(dir, name, prompt string) error {
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		return fmt.Errorf("agents: create %s: %w", dir, err)
	}

	path := filepath.Join(dir, PersonalityFileName)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("agents: read %s: %w", path, err)
	}

	if prompt == "" {
		prompt = fmt.Sprintf(
			"Describe %s here: what they own, how they work, what they push back on. "+
				"This file is read fresh every time they are given a task.", name)
	}
	body := fmt.Sprintf("# %s\n\n%s\n", name, prompt)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("agents: write %s: %w", path, err)
	}
	return nil
}

// Scan reads the agents folder and returns the team it describes.
//
// It is read-only; Ensure is what repairs a folder. Desks are assigned from
// the resulting roster, so the office layout is a function of who exists rather
// than of anything stored on disk.
//
// An agent whose folder name matches a built-in keeps that agent's structure --
// its colour, model, role and skillset -- because those are Work's, not the
// user's. Everything else is a new specialist.
func Scan(root string) ([]Agent, error) {
	dir := AgentsDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("agents: read %s: %w", dir, err)
	}

	builtin := Default()
	seen := make(map[string]bool, len(entries))
	var coordinator []Agent
	var specialists []Agent

	for _, e := range entries {
		if !isAgentFolder(e) {
			continue
		}
		// Folder names are matched case-insensitively so "Anton" is Anton, and
		// the loser of a case collision is skipped rather than overwriting the
		// winner with a half-built duplicate.
		id := strings.ToLower(e.Name())
		if seen[id] {
			continue
		}
		seen[id] = true

		agentDir := filepath.Join(dir, e.Name())
		a, ok := builtin.Get(id)
		if !ok {
			a = Agent{
				ID:     id,
				Name:   displayName(e.Name()),
				Role:   RoleSpecialist,
				Title:  "Specialist",
				Colour: colourFor(id),
			}
		}
		a.Dir = agentDir
		a.Avatar = avatarIn(agentDir)
		if text, err := ReadPersonality(agentDir); err == nil {
			a.Summary = summarise(text)
		}

		if a.Role == RoleCoordinator {
			coordinator = append(coordinator, a)
			continue
		}
		specialists = append(specialists, a)
	}

	if len(coordinator) == 0 {
		return nil, fmt.Errorf(
			"agents: no coordinator in %s: expected a %s folder", dir, CoordinatorFolder)
	}

	// The coordinator leads the list because the office draws it in order and
	// the roster in the routing prompt reads better with him first.
	out := append(coordinator, specialists...)
	AssignDesks(out)
	return out, nil
}

// ReadPersonality returns an agent's PERSONALITY.md, truncated to
// MaxPersonalityBytes. A missing file is not an error: an agent with no
// personality file simply contributes nothing to the prompt.
//
// This is read on every dispatch rather than cached, so editing the file in an
// editor changes the next task. It is one small sequential read against a task
// that is about to start a whole Claude process, so the cost does not register.
func ReadPersonality(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	f, err := os.Open(filepath.Join(dir, PersonalityFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("agents: read personality: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, MaxPersonalityBytes))
	if err != nil {
		return "", fmt.Errorf("agents: read personality: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// isAgentFolder reports whether a directory entry describes an agent. Hidden
// folders are skipped so a .git or .DS_Store cannot become a colleague.
func isAgentFolder(e fs.DirEntry) bool {
	return e.IsDir() && !strings.HasPrefix(e.Name(), ".")
}

// avatarIn returns the agent's avatar path, or empty if the folder has none.
func avatarIn(dir string) string {
	for _, name := range avatarNames {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// summarise pulls a one-line description out of a personality file: the first
// line of prose, falling back to the heading when the file is only a heading.
func summarise(text string) string {
	var heading string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if heading == "" {
				heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
			}
			continue
		}
		return truncate(line, maxSummaryBytes)
	}
	return truncate(heading, maxSummaryBytes)
}

// truncate cuts to at most n bytes without splitting a rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// utf8Start reports whether b begins a rune rather than continuing one.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// displayName turns a folder name into something to put on a desk:
// "data-wrangler" reads as "Data wrangler".
func displayName(folder string) string {
	name := strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(folder))
	if name == "" {
		return folder
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// colourFor picks a stable colour for an agent Work does not ship. Hashing the
// ID means the colour survives restarts and folder reordering without being
// stored anywhere.
func colourFor(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return fallbackColours[int(h.Sum32())%len(fallbackColours)]
}
