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

	"dev.jevido/work/apps/studio/templates"
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
	// CoordinatorFolder is the agent Work cannot assemble a team without:
	// with no coordinator there is nobody to hand a task to, and Scan says so
	// rather than returning a roster that cannot work.
	CoordinatorFolder = "anton"
	// ThinkerFolder leads the orientation and isolation conversations. Guaranteed for
	// a softer reason than the coordinator -- deleting it falls back to him
	// rather than failing -- but guaranteed, because those two modes are
	// otherwise run by the agent whose whole prompt is about routing work.
	// See Registry.For.
	ThinkerFolder = "jared"
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
	// Whether this is the first time anybody has had an agents folder here.
	// It decides one thing and one thing only: whether the starter colleagues
	// are written. They are a starting point, not part of the product, so a
	// user who deleted Chris must not find him back tomorrow.
	fresh := false
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		fresh = true
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agents: create %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}

	shipped, err := fs.ReadDir(templates.Agents(), ".")
	if err != nil {
		return fmt.Errorf("agents: read the shipped agents: %w", err)
	}

	// The built-in team is guaranteed, fresh root or not. That is two agents
	// rather than one, and for two different reasons: Scan cannot build a team
	// without a coordinator, and the orientation and isolation conversations
	// would otherwise fall back to him -- see Default. Everyone else is the
	// user's to keep or delete.
	//
	// Idempotent, because copyShipped never touches a file that already
	// exists. A root somebody has been editing for months comes through this
	// untouched.
	for _, a := range Default().All() {
		agentDir := filepath.Join(dir, a.ID)
		if err := copyShipped(a.ID, agentDir); err != nil {
			return err
		}
		if err := writeAgentFolder(agentDir, a.Name); err != nil {
			return err
		}
	}

	if fresh {
		for _, e := range shipped {
			id := e.Name()
			if !e.IsDir() || id == TemplateFolderName {
				continue
			}
			if _, built := Default().Get(id); built {
				continue
			}
			if err := copyShipped(id, filepath.Join(dir, id)); err != nil {
				return err
			}
		}
	}

	// The template is Work's own documentation rather than anybody's agent, so
	// it is rewritten every time: a stale copy of it describes a folder layout
	// that may no longer be the one Scan reads. Anyone who wanted to keep an
	// edited version has already copied it, which is the whole point of it.
	if err := copyShippedOver(TemplateFolderName, filepath.Join(dir, TemplateFolderName)); err != nil {
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
		if err := writeAgentFolder(filepath.Join(dir, e.Name()), displayName(e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyShipped writes one shipped agent folder to disk, skipping every file that
// is already there. An id nothing is shipped for is not an error: a built-in
// with no folder in templates/ simply gets the generic starter below.
func copyShipped(id, dir string) error { return copyTree(id, dir, false) }

// copyShippedOver is the same, for the one folder Work owns rather than the
// user: existing files are replaced.
func copyShippedOver(id, dir string) error { return copyTree(id, dir, true) }

func copyTree(id, dir string, overwrite bool) error {
	source := templates.Agents()
	if _, err := fs.Stat(source, id); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("agents: read the shipped %s: %w", id, err)
	}

	// Guaranteed even when the shipped folder has no skills in it: git and
	// go:embed both drop an empty directory, and an agent folder without one is
	// a folder somebody has to create before they can add a skill.
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		return fmt.Errorf("agents: create %s: %w", dir, err)
	}

	return fs.WalkDir(source, id, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, id)))
		if e.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("agents: create %s: %w", target, err)
			}
			return nil
		}
		if !overwrite {
			if _, err := os.Stat(target); err == nil {
				return nil
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("agents: read %s: %w", target, err)
			}
		}
		body, err := fs.ReadFile(source, path)
		if err != nil {
			return fmt.Errorf("agents: read the shipped %s: %w", path, err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return fmt.Errorf("agents: write %s: %w", target, err)
		}
		return nil
	})
}

// writeAgentFolder makes sure one folder is a complete agent: it has a skills/
// directory, and it has a PERSONALITY.md. An existing personality file is never
// touched -- it is the user's document.
//
// This is the fallback, not the usual path. An agent Work ships gets its file
// from templates/agents; this is what a folder somebody made by hand in a file
// manager gets, so that a bare directory with a name in it is a reasonable way
// to ask for an agent.
//
// What it writes is deliberately not a system prompt. The personality file is
// read back and appended to the prompt on every single turn, so seeding it from
// the prompt would hand Claude the same instructions twice for the life of the
// folder. A document written for the person who opens the file is a different
// document from one written for the model.
func writeAgentFolder(dir, name string) error {
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		return fmt.Errorf("agents: create %s: %w", dir, err)
	}

	path := filepath.Join(dir, PersonalityFileName)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("agents: read %s: %w", path, err)
	}

	body := fmt.Sprintf(
		"# %s\n\nDescribe %s here: what they own, how they work, what they push "+
			"back on. This file is read fresh every time they are given a task.\n",
		name, name)
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
