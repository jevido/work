<script lang="ts">
  import * as Workbench from "../bindings/dev.jevido/work/services/workbenchservice.js";
  import ClaudeConsole from "./components/ClaudeConsole.svelte";
  import KanbanBoard from "./components/KanbanBoard.svelte";
  import OfficeCanvas from "./components/OfficeCanvas.svelte";
  import { TaskBoard } from "./lib/board/board.svelte";
  import { ClaudeSession } from "./lib/claude/session.svelte";
  import type { AgentIdentity } from "./lib/claude/session.svelte";
  import type { AgentSpec } from "./lib/office/renderer";

  const session = new ClaudeSession();
  const board = new TaskBoard();

  let agents = $state<AgentSpec[]>([]);
  let identities = $state<AgentIdentity[]>([]);
  let loadError = $state<string | null>(null);
  let showPerf = $state(false);

  // Backend events are wired once for the lifetime of the app.
  $effect(() => session.listen());
  $effect(() => board.listen());

  $effect(() => {
    let cancelled = false;
    Workbench.Agents()
      .then((list) => {
        if (cancelled) return;
        // A Go nil slice arrives as null, so an empty team is not an error.
        const roster = list ?? [];
        // The console and the board label things by agent, so they need the
        // roster too.
        identities = roster.map((a) => ({ id: a.id, name: a.name, colour: a.colour }));
        session.setAgents(identities);
        agents = roster.map((a) => ({
          id: a.id,
          name: a.name,
          colour: a.colour,
          deskX: a.desk.x,
          deskY: a.desk.y,
          seatX: a.desk.seatX,
          seatY: a.desk.seatY,
        }));
      })
      .catch((err: unknown) => {
        if (!cancelled) loadError = err instanceof Error ? err.message : String(err);
      });
    return () => {
      cancelled = true;
    };
  });

  function onKeydown(event: KeyboardEvent) {
    // Ctrl/Cmd+P toggles the performance overlay. Deliberately cheap and
    // deliberately optional.
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "p") {
      event.preventDefault();
      showPerf = !showPerf;
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<main>
  <div class="work">
    <KanbanBoard {board} agents={identities} />
    <div class="office">
      {#if loadError}
        <div class="load-error">{loadError}</div>
      {/if}
      <OfficeCanvas {agents} {showPerf} />
    </div>
  </div>
  <aside>
    <ClaudeConsole {session} />
  </aside>
</main>

<style>
  main {
    display: grid;
    /* Office takes the room; the console keeps a usable, fixed-ish width. */
    grid-template-columns: minmax(0, 1fr) clamp(340px, 27%, 620px);
    /* An auto-sized row grows to its tallest child, which lets a long
       transcript push the whole window -- canvas included -- off screen.
       Pinning the row to the viewport makes the console scroll instead. */
    grid-template-rows: minmax(0, 100%);
    height: 100%;
    overflow: hidden;
  }

  .work {
    display: grid;
    /* The board takes a fixed slice off the top; the office gets the rest. */
    grid-template-rows: clamp(150px, 24%, 240px) minmax(0, 1fr);
    min-width: 0;
    min-height: 0;
  }

  .office {
    position: relative;
    min-width: 0;
    min-height: 0;
  }

  aside {
    min-width: 0;
    min-height: 0;
  }

  .load-error {
    position: absolute;
    z-index: 1;
    top: 10px;
    right: 10px;
    padding: 6px 10px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
  }
</style>
