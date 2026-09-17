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
	// CoordinatorFolder is the agent Work cannot assemble a team without:
	// with no coordinator there is nobody to hand a task to, and Scan says so
	// rather than returning a roster that cannot work.
	CoordinatorFolder = "anton"
	// ThinkerFolder leads the idea and planning conversations. Guaranteed for
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
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agents: create %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}

	// The built-in team is guaranteed, fresh root or not. That is two agents
	// now rather than one, and for two different reasons: Scan cannot build a
	// team without a coordinator, and the idea and planning conversations
	// would otherwise fall back to him -- see Default. Everyone else is the
	// user's to add.
	//
	// Idempotent, because writeAgentFolder never touches a PERSONALITY.md that
	// already exists. A root somebody has been editing for months comes through
	// this untouched.
	for _, a := range Default().All() {
		agentDir := filepath.Join(dir, a.ID)
		if err := writeAgentFolder(agentDir, a.Name, a.Starter); err != nil {
			return err
		}
		if err := seedSkills(agentDir, a.ID); err != nil {
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
  Each one is read and handed to the agent along with this file, every time
  they are given work -- so a skill is instructions that take effect, and
  every task pays for it in tokens the same way this file does.
`

// starterSkills are the skills Work writes into a built-in agent's folder the
// first time it makes one, by agent id and then by file name.
//
// Written once and never again, exactly like PERSONALITY.md and for the same
// reason: the moment it is on disk it is the user's document, and an agent
// whose instructions are silently rewritten under them is an agent nobody can
// tune. That is the opposite of _template, which is Work's own documentation
// and is kept current.
//
// What is in here is judgement -- when a card is worth making, what a good
// title is, when to tag one. What is deliberately *not* in here is the shape of
// the tool call: that lives in internal/propose, where it cannot drift out of
// step with the schema it describes. A skill that restated the JSON would be a
// second copy of it, going stale in a file nobody thinks to update.
var starterSkills = map[string]map[string]string{
	ThinkerFolder: {"the-board.md": boardSkill},
}

// seedSkills writes an agent's starter skills, skipping any that exist.
func seedSkills(dir, id string) error {
	files := starterSkills[id]
	if len(files) == 0 {
		return nil
	}
	skillsDir := filepath.Join(dir, SkillsDirName)
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("agents: create %s: %w", skillsDir, err)
	}
	for name, body := range files {
		path := filepath.Join(skillsDir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("agents: read %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return fmt.Errorf("agents: write %s: %w", path, err)
		}
	}
	return nil
}

// boardSkill is how to work a board of cards well, as opposed to how to call
// the tool that changes one.
const boardSkill = `# The board

Idea mode is a board: one subject in the middle, a handful of clusters around
it, and cards hanging off each of them, several levels deep. You put things on
it. A conversation that ends in agreement and an empty board has produced
nothing.

## What a board is for

Mapping something so it can be seen all at once. Usually that thing is a
product or a system: what it is made of, and what each part is made of, down to
the level where somebody could build it or disagree with it.

It is not a list of talking points, and it is not a set of questions. "Why
rebuild it at all", "where the real difficulty lies" and "who is this for" are
things to *say* -- put them in your reply. They are not cards. A card is part
of the thing; a question is part of the conversation about it.

## What a card is

A **title** and a **detail**. The title is drawn on the board and read at a
glance; the detail is what somebody sees when they open it.

The title names a part, in the words the domain already uses for it:
"Memberships", "Direct debit runs", "Match sheets". Six or seven words at the
outside, usually one or two. Not a sentence, not a question, not an opinion.

The detail is where the sentence goes: what this part covers, what it has to
handle, what is unusual about it, what you assumed. Write one whenever you know
something the title cannot hold. An empty detail is fine; a detail restating
the title is noise.

## Shape

- Four to seven clusters at the top. Each one is a capability of the thing, not
  a theme about it.
- **Go deep.** Three or four levels is normal for a real system, and the levels
  are what carry the meaning: a cluster with three children and nothing under
  them says the domain has three parts and stops, which is almost never true.
- Keep going until a leaf is concrete: a screen, a rule, a document, a job that
  runs, a thing a member does. "Handled somehow" is not a leaf.
- A cluster head takes a glyph so it can be found from across the board.
- Link cards that relate across clusters, and caption the link with why.
  "Matches" and "Memberships" meet at eligibility; say so on the line.

### What that looks like

A club-management product, in outline. This is the shape, not the content --
the content is whatever the thing in front of you is actually made of.

    Memberships
      Types and tiers
        Junior, senior, social, life
        Household and family bundles
        Concessions and hardship rates
      Joining and leaving
        Application and approval
        Waiting lists
        Resignation, and the notice period
      Dues and collection
        Billing runs and pro rata
        Direct debit, and what happens when one fails
        Arrears, reminders, suspension

    Matches
      Fixtures
        The season calendar and how it is drawn up
        Home and away, and who supplies the pitch
        Postponement and rearrangement
      Teams and selection
        Squads, and who is eligible to play
        Availability, and who chases it
        Team sheets
      Tournaments
        Formats: knockout, round robin, ladder
        Seeding and draws
        Scheduling across courts and days
      Results
        Entry, and who is allowed to enter one
        Tables and standings
        Disputes

Notice what the leaves are: things somebody has to build and somebody has to
do. "Direct debit, and what happens when one fails" is a card. "Payments are
important" is not.

## When to ask, and when to map

If somebody names a thing and asks for its ideas, features or shape: **map it.**
Build it out of what that kind of system is made of, mark what you guessed, and
say in your reply which parts you were least sure about. Do not open with
questions. A board they can correct is worth more than three questions they
have to answer before seeing anything.

Ask first only when you genuinely cannot start: you do not know what the thing
*is*, and no reasonable guess is available.

Assumptions go in the detail of the card they affect, not in a cluster of their
own.

## Tagging

The workspace sets up two vocabularies, and you can only use the words that are
there.

- **Guidelines** are what this workspace is trying to be. Put a card under the
  ones it genuinely serves. Five guidelines on one card means either the card
  is vague or somebody is tagging out of enthusiasm, and saying so is more use
  than tagging it.
- **Interested** is who is waiting on a card. Only when you have been told, or
  it is on the board already. Never infer that Sales cares about something
  because it sounds commercial -- an invented name is worse than a blank, and
  the point of the list is that somebody can sort by it.

If a card obviously needs a word the workspace has not set up, say so in your
reply. You cannot add one, and that is deliberate.

## Rebuilding

When somebody dumps a pile of half-formed ideas, or names a product and asks
for a board of it, start over: everything already there is set aside under one
collapsed line, not deleted, so they can take it back. Do not start over to
tidy a branch or to add to a board -- that is the ordinary operations.

## What you are not

You do not build the thing. You have no tool that edits a file or runs a
command, and the coordinator cannot hand you a step -- so "I could put that
together for you" is an offer you cannot keep, and somebody who accepts it has
been misled about what this conversation is.

When the thinking is done and the thing wants building, say so plainly and say
it is Anton's. Then stop. Sketching the implementation in the chat window to be
useful is the same mistake in a smaller font: it is work nobody can run, review
or take back, and it is not on the board either.

The best thing you can leave behind is a board somebody can hand to Anton
without explaining it.

## What not to do

- Do not put a question on the board. Ask it in your reply.
- Do not write a card you could not defend as a part of the thing.
- Do not stop at one level because the top level looked tidy.
- Do not split one part across four cards to make the board look busy.
- Do not put the same thing under two clusters. If it belongs to both, it is a
  card in one and a link to the other.
- Do not fill in a detail with a paraphrase of the title to make the card look
  finished.
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
//
// `starter` is Agent.Starter, and used to be Agent.SystemPrompt. That was a
// quiet bug worth naming here as well as there: the personality file is read
// back and appended to the system prompt on every single turn, so seeding it
// from the prompt handed Claude the same instructions twice for the life of
// the folder. A document written for the person who opens the file is a
// different document from one written for the model.
func writeAgentFolder(dir, name, starter string) error {
	if err := os.MkdirAll(filepath.Join(dir, SkillsDirName), 0o755); err != nil {
		return fmt.Errorf("agents: create %s: %w", dir, err)
	}

	path := filepath.Join(dir, PersonalityFileName)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("agents: read %s: %w", path, err)
	}

	if starter == "" {
		starter = fmt.Sprintf(
			"Describe %s here: what they own, how they work, what they push back on. "+
				"This file is read fresh every time they are given a task.\n", name)
	}
	body := fmt.Sprintf("# %s\n\n%s", name, starter)
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
