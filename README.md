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

Agents run in Work's working directory and inherit your local Claude
installation's own permission configuration — Work passes no permission flags of
its own. Whatever your `claude` setup already allows without asking, an agent can
do here, and there is no approval prompt in the UI yet. Start Work from a
directory you are happy for it to work in.

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

## How a run works

You type a task. Anton takes a short, schema-constrained routing turn on a
cheaper model (`PlanModel`) and answers one question: keep it, or split it. The
schema enumerates the real specialist IDs, so he cannot invent a colleague, and
the plan is normalised before it runs — unknown agents, repeats, blank tasks and
anything over the step cap are dropped.

If he keeps it, he answers it himself. If he splits it, the specialists run at
the same time, each with their own prompt, model and tool access, and each
streaming into its own block in the console. A specialist failing does not fail
the run: its error goes to synthesis with everyone else's answers, so a partial
result still reaches you. Anton then takes a final turn and gives you one
answer.

One run is active at a time. Cancelling the run stops every agent inside it.

## Status

Delegation works end to end. Still missing: thinking output is streamed but not
displayed, tool calls show as name chips without arguments or results, there is
no task history or session resume, and no approval step before an agent acts.
