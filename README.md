# Work

An AI development workbench with a visual office.

You give a task to **Anton**. Anton decides how to approach it and, when it
helps, splits it between your specialists — whoever those are. Anton is the
only agent Work ships; the rest of the team is yours to write, one folder
each, and he is handed the roster as it stands on every request rather than
being told in advance who exists. The agents are drawn as workers in a small
office: idle they wander, assigned they walk to their desk, working they sit
down and get on with it.

The office is not the product. It is a cheap, legible picture of what the AI
system is doing, so understanding a run does not mean reading raw terminal
output.

## Requirements

- Go 1.25+
- Node 22+ and npm
- [Wails v3](https://v3.wails.io) CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.18`
- The Claude Code CLI, already signed in: `claude`

Work talks to your local `claude` binary, so it never handles credentials of its
own. Point it at a specific installation with `CLAUDE_BIN=/path/to/claude`.

Agents run in Work's working directory and inherit your local Claude
installation's own permission configuration — Work passes no permission flags of
its own. Whatever your `claude` setup already allows without asking, an agent can
do here, and there is no approval prompt in the UI yet. Start Work from a
directory you are happy for it to work in.

## Building

The webview binding is CGO, so each OS needs its own native toolchain and
webview runtime before anything will link.

**Linux** — GTK4 and WebKitGTK 6.0, plus a C compiler:

```sh
# Arch
sudo pacman -S gtk4 webkitgtk-6.0 base-devel
# Debian/Ubuntu
sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev build-essential
```

**macOS** — Xcode Command Line Tools only (WebKit is part of the OS):

```sh
xcode-select --install
```

**Windows** — a C compiler (MSVC via [Visual Studio Build
Tools](https://visualstudio.microsoft.com/downloads/#build-tools-for-visual-studio-2022),
or `mingw-w64`/TDM-GCC) and the WebView2 runtime, which ships with Windows
11 and current Windows 10 out of the box.

### On Linux

```sh
wails3 dev      # hot-reloading development build
wails3 build    # production binary in ./bin/work
wails3 task check   # go vet + svelte-check
go test ./...
```

`Ctrl+P` toggles the performance overlay (FPS, frame time, agent count).

### On macOS and Windows

The `wails3` wrappers do not work here yet. `wails3 build`, `wails3 dev` and
`wails3 task run` all dispatch through the Taskfile as `{{OS}}:build`, and only
the `linux` target is wired up (`build/linux/`), so on either platform they stop
at:

```
task: Task "darwin:build" does not exist
```

`wails3 update build-assets` fills in `build/darwin/Info.plist` and
`build/windows/` (manifest, NSIS, `info.json`), but it does not write the
per-platform Taskfiles — those come from the `wails3 init` template and were
never carried in this repository.

Building by hand works on any OS with the toolchain above:

```sh
npm --prefix frontend install
npm --prefix frontend run build       # go:embed needs frontend/dist to exist
go build -tags production -o bin/work # bin/work.exe on Windows
```

`frontend/bindings/` is committed, so a fresh clone does not need to generate
it; run `wails3 generate bindings -ts -i` after changing a Go service signature.

What you get from that is a bare executable. There is no packaging for mac or
Windows — the `task package` pipeline (AppImage, deb, rpm, AUR) is Linux-only,
and `common:generate:icons` is deliberately stubbed to a no-op because no
`.icns` or `.ico` is carried here, so the app ships with no icon on those
platforms.

## Architecture

```
main.go                       Wails app: window, services, event registration
internal/agents/              The agents folder: scanning, avatars, desk layout
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
— T1, T2 — because that is how you talk to him about it: "close T3", "put Ada
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

## Your team

Agents are folders. Pick a config root the first time Work starts and it
creates this:

```
<root>/agents/
  anton/            The coordinator. The one agent Work insists on.
    PERSONALITY.md
    skills/
  _template/        Copy this to add someone.
    PERSONALITY.md
    skills/
```

Adding a colleague is `cp -r _template ada`. The folder name is the agent's
id and Work capitalises it for the name on the desk, so `ada` becomes Ada.
Folders beginning with `_` or `.` are skipped, which is how `_template` sits
among the agents without being one — and how you park a half-written agent
without moving it out.

`PERSONALITY.md` is the agent. It is read fresh on every dispatch, so an edit
lands on the next task rather than the next restart, and **its first line of
prose is the blurb Anton routes on** — the only thing he knows about that
agent when he decides who gets the work. Spend it on the specialty.

Optionally drop an `avatar.webp` or `avatar.png` beside it for the office to
draw, and skills in `skills/` — one folder or `.md` file per skill, and a
symlink into a shared skills folder counts, which is the cheap way to give the
same skill to two agents. Their names show up in the agent's profile; nothing
puts them in front of Claude yet.

Click the person icon in an agent's window for that profile: what Anton routes
them on, the skills in their `skills/`, their personality file and the path to
edit it. All of it is read off disk when you open it, so a file you just saved
is what you see.

Anton is not told in advance who works here. Every routing turn is handed the
roster as it actually is, and the schema that turn must satisfy enumerates the
agent ids that currently exist, so he can neither invent a colleague nor keep
naming one you deleted. Nothing about the team is compiled in except Anton
himself, who is guaranteed because a team with no coordinator has nobody to
hand a task to.

The wrench menu re-scans without a restart. `_template` is Work's file rather
than yours: it is rewritten on every scan, because it documents the layout and
a stale copy of it is worse than none. Your agents' personality files are never
touched.

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
