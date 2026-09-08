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
internal/board/               The task board: cards, columns, assignment
internal/changes/             What a run did to the working tree, and undoing it
internal/claude/              Runs the local Claude CLI, parses its JSON stream
internal/workbench/           Runtime state, task lifecycle, semantic events
services/                     Thin adapters the frontend can call
frontend/src/lib/office/      Canvas 2D renderer, agent state machine, frame stats
frontend/src/lib/claude/      Conversation state, batched streaming
frontend/src/lib/board/       Task board state
frontend/src/lib/changes/     File-change review state
frontend/src/lib/diff/        Line differ, unified-diff parser, tool summaries
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

## The window

The left side is the work: a task board across the top, the office below it. The
right side is the conversation.

The board is not a second planning system. Anton already creates and assigns
tasks when he routes a request, so the board shows those assignments and tracks
them as they run — Assigned, In progress, Done, Blocked. Nothing on it costs an
extra Claude call.

The board is Anton's to manage and yours to read. Every task carries a short ID
— T1, T2 — because that is how you talk to him about it: "close T3", "put Chris
on T4". He is shown the board on every request and can create tasks, close
them, rename them or hand them to someone else, and any edit he makes appears in
the conversation next to his reasoning. There is no drag-and-drop; the board
reflects decisions rather than accepting them.

The conversation keeps everything: what you asked, Anton's routing decision and
his reason for it, each agent's reply labelled with who said it, every tool call
with its arguments and result, and what the run cost.

Tool calls are collapsed to one line each — the tool and the file or command,
which is enough to recognise the interesting ones — and open to show the full
arguments and output. An edit opens as a diff rather than as two walls of text. Agents remember it too — each one continues its own Claude session
across turns, so a follow-up can lean on what was already said. "New chat"
clears the screen, the board and the agents' memory in one gesture, because
those three drifting apart would be worse than any of them being stale.

## Reviewing what an agent did

Work never grants an agent more than your own Claude installation already
allows: no permission flag is passed unless an agent's `PermissionMode` sets
one. Setting it to `acceptEdits` lets that agent write files.

The Claude CLI has no hook that lets Work approve a write before it happens, so
the gate sits after the fact. Work snapshots the working tree when a run starts
and lists every file the run touched when it ends, with diffs and a per-file
revert.

Revert restores the content the file had when the run started — not the content
at HEAD. A file you had already edited before asking for help goes back to *your*
version, not the last commit. Where Work cannot promise that, the revert button
says so instead of guessing.

Changes you made yourself before the run are not attributed to it, and stopping
a run still shows what it managed to change first.

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

One run is active at a time. Cancelling the run stops every agent inside it and
puts their unfinished cards back in Assigned.

Follow-up turns resume each agent's session. If resuming fails before producing
anything — a session the CLI no longer has — the turn is retried once from
scratch, so a stale session degrades into a fresh answer rather than a failed
run. The routing turn is deliberately excluded: it is schema-constrained and
stateless, and is simply told when it is looking at a follow-up.

## Measured cost

Taken on a 20-core laptop, 2560x1600 at 240Hz, GTK4 + WebKitGTK 6.0. Memory is
reported as PSS (each shared page divided between the processes mapping it),
which is the honest figure for a multi-process webview; RSS double-counts.

| | |
|---|---|
| Binary, everything embedded | 9.5 MB |
| Disk at rest, nothing else needed | 9.5 MB |
| Memory, idle | ~340 MB PSS (~940 MB RSS across 8 processes) |
| Startup, exec to window on screen | 216-220 ms |
| CPU, window not presented | ~0% |
| CPU, no animation loop | ~1% of one core |
| CPU, visible and animating | ~6% of one core |
| One Claude turn | 365 MB peak RSS, ~26% of one core while running |

Two things worth knowing about those numbers.

The idle memory is the webview, not Work: it is the same with the animation
loop switched off, and the Go side is a rounding error next to it. A single
Claude turn costs more RAM than the entire application, and a delegated run
holds two or more of them at once, so agent count is the memory story.

The visible CPU figure was 27% before the office cached its static layer and
started repainting only the rectangles that change. Frame time is now 0.32 ms
with a 1.08 ms peak, and a calm office runs at 15fps instead of 30. Most of what
remains is WebKit compositing the canvas surface rather than anything this code
does, so the next real gain would come from compositing fewer frames, not from
drawing them faster.

Measuring this on a tiling compositor needs care: when the window stops being
presented, `requestAnimationFrame` stops firing and the cost falls to nothing,
so a sample taken while the window is covered reads far lower than the truth.
Compare only samples taken at the same window size with the window on top.

## Status

Delegation, the conversation, the board, tool visibility and change review all
work end to end. Still missing: nothing is persisted across restarts, thinking
output is streamed but not displayed, there is no pre-write approval (the CLI
exposes no hook for one), only one run can be active at a time, and the frontend
has no test runner — the differ and the diff parser were verified by hand
against real `git diff` output rather than by a suite.
