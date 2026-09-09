package agents

// The folder is the whole definition of an agent, but most of what is in it is
// too big to carry in the roster: a PERSONALITY.md may run to
// MaxPersonalityBytes and skills/ may hold a dozen entries, while the roster is
// re-read and re-sent to the frontend on every reload and every state change.
// So the profile view asks for one agent's folder at the moment it opens one,
// and this is what it reads.

import (
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
