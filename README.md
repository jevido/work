# Work

An AI development workbench with a visual office.

You give a task to **Anton**. Anton decides how to approach it and, when it
helps, brings in specialists — **Jeff** for Go, infrastructure and performance,
**Chris** for UX, interaction and frontend. The agents are drawn as workers in a
small office: idle they wander, assigned they walk to their desk, working they
sit down and get on with it.

The office is not the product. It is a cheap, legible picture of what the AI
system is doing, so understanding a run does not mean reading raw terminal
output.

## Requirements

- Go 1.25+
- Node 22+ and npm
- [Wails v3](https://v3.wails.io) CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.18`
- The Claude Code CLI, already signed in: `claude`
- Linux: GTK4 and WebKitGTK 6.0 (`gtk4`, `webkitgtk-6.0`)

Work talks to your local `claude` binary, so it never handles credentials of its
own. Point it at a specific installation with `CLAUDE_BIN=/path/to/claude`.

## Running

```sh
wails3 dev      # hot-reloading development build
wails3 build    # production binary in ./bin/work
wails3 task check   # go vet + svelte-check
go test ./...
```

`Ctrl+P` toggles the performance overlay (FPS, frame time, agent count).

## Architecture

```
main.go                       Wails app: window, services, event registration
internal/agents/              Static agent definitions: role, desk, prompt, model
internal/claude/              Runs the local Claude CLI, parses its JSON stream
internal/workbench/           Runtime state, task lifecycle, semantic events
services/                     Thin adapters the frontend can call
frontend/src/lib/office/      Canvas 2D renderer, agent state machine, frame stats
frontend/src/lib/claude/      Console state, batched streaming
frontend/src/lib/bridge/      Event names, backend-to-renderer wiring
frontend/src/components/      Svelte UI
```

The division of labour is the important part:

- **Go owns real work.** Agents, Claude processes, task lifecycle.
- **Svelte owns application UI.** The console, layout, controls.
- **The Canvas renderer owns animation state**, and nothing else.

The backend emits semantic events only — `agent:assigned`, `agent:working`,
`agent:finished`, `agent:error` — and the frontend animates between them
locally. Positions never cross the Wails bridge, and agent movement costs zero
Svelte updates.

Performance is treated as a feature: one `requestAnimationFrame` loop, a
device-pixel-ratio aware canvas, delta-time animation, a frame cap, no
per-frame allocation, and drawing stops entirely while the window is hidden.

## Status

The first milestone: one agent (Anton) runs the task you type, and the office
reflects it. Jeff and Chris exist with their own prompts, desks and colours, but
Anton does not delegate to them yet. Their definitions already carry the fields
delegation will need — per-agent prompts, models and tool access.
