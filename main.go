// Work is an AI development workbench: you give a task to Anton, he assembles
// the right specialists, and you watch them work in a small visual office.
//
// The split of responsibilities is deliberate:
//
//   - Go owns real work: agent definitions, Claude processes, task lifecycle.
//   - Svelte owns application UI: the console, the layout, the controls.
//   - The Canvas renderer owns animation state, and only animation state.
//
// The backend emits semantic events (agent:assigned, agent:working,
// agent:finished, agent:error) and never positions. Movement never crosses the
// Wails bridge.
package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/config"
	"dev.jevido/work/internal/update"
	"dev.jevido/work/internal/workbench"
	"dev.jevido/work/services"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is the release this binary was built as. The release build stamps it
// in with -ldflags "-X main.version=..." from the version in build/config.yml,
// which is also the tag .github/workflows/release.yml publishes under -- so
// what the app reports and what it can download are the same number by
// construction rather than by anyone remembering to bump both.
//
// It stays "dev" for every other build, and internal/update treats that as
// "do not check, do not replace": a locally built binary is usually ahead of
// the newest release.
var version = update.DevVersion

func init() {
	// Registering the event payload types gives the frontend generated,
	// strongly typed listeners for each semantic event.
	application.RegisterEvent[workbench.AgentEvent](workbench.EventAgentAssigned)
	application.RegisterEvent[workbench.AgentEvent](workbench.EventAgentWorking)
	application.RegisterEvent[workbench.AgentEvent](workbench.EventAgentFinished)
	application.RegisterEvent[workbench.AgentEvent](workbench.EventAgentError)

	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeSession)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeText)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeThinking)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeTool)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeToolResult)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeResult)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeError)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeCancelled)

	application.RegisterEvent[workbench.RunEvent](workbench.EventRunStarted)
	application.RegisterEvent[workbench.RunEvent](workbench.EventRunPlan)
	application.RegisterEvent[workbench.RunEvent](workbench.EventRunFinished)

	application.RegisterEvent[workbench.BoardEvent](workbench.EventBoardUpdated)
	application.RegisterEvent[workbench.ChangesEvent](workbench.EventRunChanges)

	application.RegisterEvent[update.AvailableEvent](update.EventUpdateAvailable)
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	// The config is read first because it decides one of the arguments below:
	// which directory agents run in. A config that cannot be read is a
	// warning, not a fatal error -- the zero value describes a first run, and
	// Work is usable before it is configured.
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v", err)
	}

	// A desktop launcher starts Work in $HOME rather than in the project you
	// meant, so the directory it was last started from by hand stands in for
	// one. Remembering it at startup rather than at exit means it survives a
	// crash, and it costs a single small write on the launches that change it.
	workDir, remember := config.StartDir(cwd, cfg.WorkDir)
	if remember {
		if err := config.Update(func(c *config.Config) { c.WorkDir = workDir }); err != nil {
			log.Printf("config: %v", err)
		}
	}

	// The workbench needs to emit before the app exists, so the emitter reaches
	// for the app lazily. application.Get() is set by application.New below.
	emit := func(name string, data any) {
		if app := application.Get(); app != nil {
			app.Event.Emit(name, data)
		}
	}

	// CLAUDE_BIN lets you point Work at a specific local Claude installation.
	// Authentication stays entirely with that binary; Work never sees a key.
	wb := workbench.New(
		agents.Default(),
		claude.NewRunner(os.Getenv("CLAUDE_BIN")),
		emit,
		workDir,
	)

	// The update checker shares the workbench's lazy emitter for the same
	// reason: it is built here, but it has nothing to say until the app is
	// running. Its own goroutine is started by the service, whose startup
	// context ends it.
	updates := update.New(update.DefaultRepo, version, emit)

	// The saved config folder is applied before the window opens, so the office
	// draws the user's own team rather than the built-in one and then swapping.
	// A folder that has gone missing is a warning, not a fatal error: Work
	// falls back to the built-in team and the user can pick a folder again.
	//
	// A mode saved by a later version of Work, or edited by hand into
	// something the CLI does not accept, falls back to the default rather than
	// taking the window down with it.
	if mode, err := claude.ParsePermissionMode(cfg.PermissionMode); err != nil {
		log.Printf("permissions: %v", err)
	} else {
		wb.UsePermissionMode(mode)
	}
	if cfg.Root != "" {
		if _, err := wb.UseConfigRoot(cfg.Root); err != nil {
			log.Printf("agents: %v", err)
		}
	}

	app := application.New(application.Options{
		Name:        "Work",
		Description: "AI development workbench",
		Services: []application.Service{
			application.NewService(services.NewWorkbenchService(wb)),
			application.NewService(services.NewUpdateService(updates)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
			// Avatars are files in the user's own agent folders, so they
			// cannot be embedded like the rest of the frontend: the middleware
			// serves them off disk, from whichever folder is in use now. See
			// avatars.go.
			Middleware: avatarMiddleware(wb.ConfigRoot),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Work",
		Width:            1440,
		Height:           900,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(14, 16, 20),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
