package agents

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"dev.jevido/work/apps/studio/templates"
)

// Which agent leads a conversation is a different question from which agent
// receives a task, and For is the one that answers the first.
func TestForReturnsTheModeOwner(t *testing.T) {
	reg := Default()

	cases := map[string]string{
		"orientation":    "jared",
		"isolation":      "jared",
		"implementation": "anton",
		// A mode nobody claims is not an error. It is a build talking to a
		// roster that predates it, and falling back to the coordinator is what
		// this app did before modes had owners at all.
		"":         "anton",
		"whatever": "anton",
	}
	for mode, want := range cases {
		lead, ok := reg.For(mode)
		if !ok {
			t.Errorf("For(%q) found nobody", mode)
			continue
		}
		if lead.ID != want {
			t.Errorf("For(%q) = %s, want %s", mode, lead.ID, want)
		}
	}
}

// Leading a conversation and being delegable are separate properties, and
// Jared has both: Anton may hand him a step like any other specialist.
func TestTheModeOwnerIsStillASpecialist(t *testing.T) {
	jared, ok := Default().Get("jared")
	if !ok {
		t.Fatal("no jared in the built-in team")
	}
	if jared.Role != RoleSpecialist {
		t.Errorf("role = %q, want %q", jared.Role, RoleSpecialist)
	}
	if len(jared.Skillset) == 0 {
		t.Error("no skillset, so the routing prompt has nothing to match on")
	}
	if jared.Blurb() == "" {
		t.Error("no blurb, so the roster cannot describe him")
	}
}

// Every guaranteed agent has a personality file shipped for it, and that file
// is written for the person who opens it rather than for Claude. Seeding it
// from the system prompt sent Claude its own instructions twice on every turn,
// because the file is read back and appended to the prompt.
func TestEveryBuiltInShipsAPersonality(t *testing.T) {
	for _, a := range Default().All() {
		body, err := fs.ReadFile(templates.Agents(), path.Join(a.ID, PersonalityFileName))
		if err != nil {
			t.Errorf("%s ships no %s: %v", a.ID, PersonalityFileName, err)
			continue
		}
		if strings.Contains(string(body), a.SystemPrompt) {
			t.Errorf("%s's shipped personality is a copy of its system prompt", a.ID)
		}
		if summarise(string(body)) == "" {
			t.Errorf("%s's shipped personality has no first line for Anton to route on", a.ID)
		}
	}
}

// The starter colleagues are shipped, complete and routable -- an agent folder
// whose personality file says nothing is an agent Anton cannot place.
func TestStarterColleaguesAreComplete(t *testing.T) {
	entries, err := fs.ReadDir(templates.Agents(), ".")
	if err != nil {
		t.Fatalf("reading the shipped agents: %v", err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == TemplateFolderName {
			continue
		}
		if _, built := Default().Get(e.Name()); built {
			continue
		}
		found++
		body, err := fs.ReadFile(templates.Agents(), path.Join(e.Name(), PersonalityFileName))
		if err != nil {
			t.Errorf("%s ships no %s: %v", e.Name(), PersonalityFileName, err)
			continue
		}
		if blurb := summarise(string(body)); blurb == "" || strings.HasPrefix(blurb, "#") {
			t.Errorf("%s's blurb is %q, which is not a description of the agent", e.Name(), blurb)
		}
	}
	if found == 0 {
		t.Error("no starter colleagues ship at all, so a fresh root has nobody to delegate to")
	}
}
