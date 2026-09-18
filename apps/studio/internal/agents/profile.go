package agents

// The folder is the whole definition of an agent, but most of what is in it is
// too big to carry in the roster: a PERSONALITY.md may run to
// MaxPersonalityBytes and skills/ may hold a dozen entries, while the roster is
// re-read and re-sent to the frontend on every reload and every state change.
// So the profile view asks for one agent's folder at the moment it opens one,
// and this is what it reads.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxSkills bounds what a skills/ folder may contribute. A folder that filled
// up by accident is cut short rather than sent whole to a panel that can only
// show a few rows of chips.
const maxSkills = 64

// Profile is what an agent's folder currently says about them.
//
// Everything here is read at call time rather than taken from the last scan:
// the panel that shows it is opened by someone who may have just edited the
// file, and stale text in the one view whose whole job is to show the file
// would read as a bug rather than as a cache.
type Profile struct {
	ID string `json:"id"`
	// Dir is the folder this was read from, empty for a built-in agent that
	// has no folder behind it.
	Dir string `json:"dir,omitempty"`
	// Skills are the names in skills/, one per skill folder or file.
	Skills []string `json:"skills"`
	// Skillset is the built-in skillset, if this agent has one. It is Work's
	// rather than the folder's, which is why it is separate from Skills.
	Skillset []string `json:"skillset"`
	// Blurb is the one line the coordinator routes this agent on, whichever of
	// the two it comes from, so nothing has to work that rule out again.
	Blurb string `json:"blurb,omitempty"`
	// Personality is PERSONALITY.md without its heading.
	Personality string `json:"personality,omitempty"`
	// Avatar is the picture in the folder, if it holds one.
	Avatar string `json:"avatar,omitempty"`
}

// ReadProfile reads an agent's folder for display.
//
// A missing folder, a missing personality file and an empty skills/ are all
// ordinary: the result simply says so, and only a folder that exists but
// cannot be read is an error.
func ReadProfile(a Agent) (Profile, error) {
	p := Profile{
		ID:       a.ID,
		Dir:      a.Dir,
		Skills:   ReadSkills(a.Dir),
		Skillset: a.Skillset,
		Blurb:    a.Blurb(),
		Avatar:   a.Avatar,
	}

	text, err := ReadPersonality(a.Dir)
	if err != nil {
		return p, err
	}
	p.Personality = personalityBody(text)
	// The scan's summary is as old as the last reload, and this is the one
	// place that has just read the file itself. Recomputing keeps the line the
	// panel calls "what Anton routes on" true of the file on screen under it.
	if len(a.Skillset) == 0 {
		p.Blurb = summarise(text)
	}
	return p, nil
}

// ReadSkills lists the skills in an agent's skills/ folder.
//
// A skill is either a folder -- the layout Claude Code uses, a directory with
// a SKILL.md in it, which is also what a symlink into a shared skills folder
// lands on -- or a single markdown file. Either way the name is what the entry
// is called, minus the extension, because that is the name the skill is
// referred to by everywhere else.
//
// The same leading dot and underscore rule as agent folders applies, for the
// same reasons: editor and version-control directories are not skills, and a
// rename is how a skill is parked without moving it.
func ReadSkills(dir string) []string {
	if dir == "" {
		return nil
	}
	skillsDir := filepath.Join(dir, SkillsDirName)
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		// No skills/ is the ordinary case for a folder somebody made by hand,
		// and Ensure creates it on the next reload anyway.
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		// Stat rather than trust the entry's own type: a skill is very often a
		// symlink into a shared skills folder, and a symlink's type says
		// nothing about whether it lands on a file, a directory or nothing.
		info, err := os.Stat(filepath.Join(skillsDir, name))
		if err != nil {
			continue
		}
		if !info.IsDir() {
			ext := filepath.Ext(name)
			if !strings.EqualFold(ext, ".md") {
				continue
			}
			name = strings.TrimSuffix(name, ext)
		}
		names = append(names, name)
		if len(names) == maxSkills {
			break
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

// personalityBody is PERSONALITY.md without its heading.
//
// The heading is the agent's name, and every view that shows this text already
// names them above it, so keeping it would print the name twice. A file that is
// only a heading has no body, which is different from having no file.
func personalityBody(text string) string {
	body := strings.TrimSpace(text)
	if !strings.HasPrefix(body, "#") {
		return body
	}
	_, after, ok := strings.Cut(body, "\n")
	if !ok {
		return ""
	}
	return strings.TrimSpace(after)
}

// MaxSkillBytes bounds one skill, and MaxSkillsBytes the lot of them.
//
// Both, because they fail differently. One runaway file is a mistake in one
// skill; twenty reasonable ones is a folder that grew a skill at a time until
// every task an agent runs carries a small book. The caps are of the same order
// as MaxPersonalityBytes and for the same reason: this text is paid for on
// every single dispatch.
const (
	MaxSkillBytes  = 16 << 10
	MaxSkillsBytes = 48 << 10
)

// SkillFileName is the document inside a skill folder, following the layout
// Claude Code uses -- which is also what a symlink into a shared skills folder
// lands on.
const SkillFileName = "SKILL.md"

// Skill is one thing an agent knows how to do, as the text of it.
type Skill struct {
	// Name is the folder or file it came from, minus any extension.
	Name string
	// Body is the document, trimmed and truncated to MaxSkillBytes.
	Body string
}

// ReadSkillTexts reads the skills in an agent's folder, in the order ReadSkills
// lists them.
//
// This is the half that was missing. skills/ has been read for the profile
// panel since it existed, and store.go said so out loud: "nothing puts them in
// front of Claude yet". So a skill was a thing you could write, see listed, and
// watch have no effect whatsoever -- which is worse than not having the folder,
// because it looks like it works.
//
// Read on every dispatch, like the personality file, so editing a skill changes
// the next task rather than the next restart. A skill with an empty body is
// dropped: a folder somebody made and has not written yet should cost nothing.
func ReadSkillTexts(dir string) []Skill {
	names := ReadSkills(dir)
	if len(names) == 0 {
		return nil
	}

	skillsDir := filepath.Join(dir, SkillsDirName)
	out := make([]Skill, 0, len(names))
	total := 0
	for _, name := range names {
		body, ok := readSkill(skillsDir, name)
		if !ok || body == "" {
			continue
		}
		// The budget is checked before appending rather than after, so the cut
		// falls between skills instead of in the middle of one. Half a skill is
		// instructions that stop mid-sentence, which is worse than one skill
		// fewer.
		if total+len(body) > MaxSkillsBytes {
			break
		}
		total += len(body)
		out = append(out, Skill{Name: name, Body: body})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// readSkill reads one entry, whichever of the two shapes it is.
func readSkill(skillsDir, name string) (string, bool) {
	// The folder form first, because ReadSkills strips the extension off the
	// file form -- so "the-board" may be either the folder or the-board.md, and
	// only one of the two stats will succeed.
	for _, candidate := range []string{
		filepath.Join(skillsDir, name, SkillFileName),
		filepath.Join(skillsDir, name+".md"),
	} {
		f, err := os.Open(candidate)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxSkillBytes))
		f.Close()
		if err != nil {
			continue
		}
		return strings.TrimSpace(string(data)), true
	}
	return "", false
}
