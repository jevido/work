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
	"dev.jevido/work/internal/workbench"
	"dev.jevido/work/services"
)

//go:embed all:frontend/dist
var assets embed.FS

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
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeResult)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeError)
	application.RegisterEvent[workbench.ClaudeEvent](workbench.EventClaudeCancelled)

	application.RegisterEvent[workbench.RunEvent](workbench.EventRunStarted)
	application.RegisterEvent[workbench.RunEvent](workbench.EventRunPlan)
	application.RegisterEvent[workbench.RunEvent](workbench.EventRunFinished)
}

func main() {
	workDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
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

	app := application.New(application.Options{
		Name:        "Work",
		Description: "AI development workbench",
		Services: []application.Service{
			application.NewService(services.NewWorkbenchService(wb)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
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
