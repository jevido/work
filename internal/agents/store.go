package agents

// Agents live on disk, one folder each, under a root the user picks. The
// folder is the definition: its name is the agent, PERSONALITY.md is who they
// are, skills/ is what they know how to do. Nothing here depends on the UI, so
// an agent added in an editor is a first-class agent.

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
	// SkillsDirName holds an agent's skills, one folder or markdown file each.
	// Work guarantees it exists and reads the names out of it for the profile
	// view; nothing puts them in front of Claude yet.
	SkillsDirName = "skills"
	// CoordinatorFolder is the one agent Work insists on: without a
	// coordinator there is nobody to hand a task to.
	CoordinatorFolder = "anton"
	// TemplateFolderName is a ready-made agent folder to copy. It lives among
	// the agents because that is where a copy of it belongs, and it is skipped
	// by the scan on the underscore rule below.
	TemplateFolderName = "_template"
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

	if err := writeTemplate(filepath.Join(dir, TemplateFolderName)); err != nil {
		return err
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

// templatePersonality is the document copied into every new agent. It is
// written to be read in an editor by someone who has never seen Work's layout,
// so it says what the folder name does, what the first prose line is used for,
// and what else may sit beside it.
const templatePersonality = `# Template

Unedited template -- replace this line with what this agent owns.

Copy this folder to make an agent:

    cp -r _template ada

The folder name is the agent's id, and Work capitalises it for the name on the
desk -- ` + "`ada`" + ` becomes Ada. Folders starting with ` + "`_`" + ` or ` + "`.`" + ` are skipped, which
is why this one is not on the team. Change the heading above to the new name.

Then replace the line under it with the agent itself: what they own, how they
work, what they push back on.

**The first line of prose is the blurb Anton routes on.** It is the only thing
he knows about this agent when he decides who gets a task, so spend it on the
specialty rather than on a greeting -- and edit it, or the placeholder above is
what he reads.

This file is read fresh every time the agent is given work, so an edit lands on
the next task rather than the next restart. Keep it short: every task pays for
it in tokens.

Optional, beside this file:

- ` + "`avatar.webp`" + ` or ` + "`avatar.png`" + `, drawn at the desk in the office.
- ` + "`skills/`" + `, one folder or ` + "`.md`" + ` file per skill. A symlink into a shared
  skills folder counts, which is the cheap way to share one between agents.
  Their names show up in the agent's profile; nothing puts them in front of
  Claude yet.
`

// writeTemplate keeps agents/_template present and current.
//
// Unlike an agent's personality this file is Work's, not the user's, so it is
// rewritten rather than preserved: it is documentation of the current layout,
// and a stale copy of it is worse than none. Anyone wanting to keep an edited
// version has already copied the folder, which is the whole point of it.
func writeTemplate(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		return fmt.Errorf("agents: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, PersonalityFileName)
	if err := os.WriteFile(path, []byte(templatePersonality), 0o644); err != nil {
		return fmt.Errorf("agents: write %s: %w", path, err)
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

// isAgentFolder decides whether a directory under agents/ describes an agent.
//
// A leading dot or underscore means it does not. Dot keeps version-control and
// editor directories out; underscore is the escape hatch that lets _template
// sit among the agents without becoming one, and lets a user park a
// half-written agent by renaming rather than moving it.
func isAgentFolder(e fs.DirEntry) bool {
	name := e.Name()
	return e.IsDir() && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")
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
