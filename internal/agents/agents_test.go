package agents

import "testing"

// Which agent leads a conversation is a different question from which agent
// receives a task, and For is the one that answers the first.
func TestForReturnsTheModeOwner(t *testing.T) {
	reg := Default()

	cases := map[string]string{
		"idea":     "jared",
		"planning": "jared",
		"work":     "anton",
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

// A built-in's starter personality is written for the person who opens the
// file. Seeding it from the system prompt sent Claude its own instructions
// twice on every turn, because the file is read back and appended to it.
func TestStartersAreNotTheSystemPrompt(t *testing.T) {
	for _, a := range Default().All() {
		if a.Starter == "" {
			t.Errorf("%s has no starter personality", a.ID)
			continue
		}
		if a.Starter == a.SystemPrompt {
			t.Errorf("%s's starter is its system prompt", a.ID)
		}
	}
}
