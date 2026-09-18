package workbench

import (
	"testing"

	"dev.jevido/work/apps/studio/internal/agents"
	"dev.jevido/work/apps/studio/internal/claude"
	"dev.jevido/work/apps/studio/internal/config"
)

// Keys outlive the workspace they belong to. The server keeps hashes, so a key
// this machine drops is a key nobody can hand back.
func TestKeysAreRememberedBeyondTheJoinedWorkspace(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())
	w.UseOps(newFakeOps())

	shared := &config.Workspace{
		ID:        "ws_one",
		Name:      "Shared",
		ServerURL: "https://work.example",
		WriteKey:  "wk_one",
		ReadKey:   "rk_one",
		Actor:     "actor_one",
	}
	if err := w.adopt(shared, true); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// Somewhere else entirely, which is what leaving and making a local
	// workspace does.
	if _, err := w.CreateLocalWorkspace("On this machine"); err != nil {
		t.Fatalf("CreateLocalWorkspace: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	known, ok := cfg.KnownWorkspace("ws_one")
	if !ok {
		t.Fatal("the workspace this machine left took its keys with it")
	}
	if known.WriteKey != "wk_one" || known.ReadKey != "rk_one" {
		t.Errorf("remembered %+v, want both keys", known)
	}
}

// A later write that knows only half the pair must not erase the other half.
func TestRememberingKeepsAKeyItWasNotToldAbout(t *testing.T) {
	cfg := &config.Config{}
	cfg.Remember(config.Known{ID: "ws", ServerURL: "https://s", WriteKey: "wk", ReadKey: "rk"})
	cfg.Remember(config.Known{ID: "ws", ServerURL: "https://s", WriteKey: "wk"})

	known, ok := cfg.KnownWorkspace("ws")
	if !ok {
		t.Fatal("the entry went")
	}
	if known.ReadKey != "rk" {
		t.Errorf("read key = %q, want it kept", known.ReadKey)
	}
}
