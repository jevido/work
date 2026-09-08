<script lang="ts">
  import * as Workbench from "../bindings/dev.jevido/work/services/workbenchservice.js";
  import ClaudeConsole from "./components/ClaudeConsole.svelte";
  import OfficeCanvas from "./components/OfficeCanvas.svelte";
  import { ClaudeSession } from "./lib/claude/session.svelte";
  import type { AgentSpec } from "./lib/office/renderer";

  const session = new ClaudeSession();

  let agents = $state<AgentSpec[]>([]);
  let loadError = $state<string | null>(null);
  let showPerf = $state(false);

  // Backend events are wired once for the lifetime of the app.
  $effect(() => session.listen());

  $effect(() => {
    let cancelled = false;
    Workbench.Agents()
      .then((list) => {
        if (cancelled) return;
        // A Go nil slice arrives as null, so an empty team is not an error.
        agents = (list ?? []).map((a) => ({
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
  <div class="office">
    {#if loadError}
      <div class="load-error">{loadError}</div>
    {/if}
    <OfficeCanvas {agents} {showPerf} />
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
    height: 100%;
  }

  .office {
    position: relative;
    min-width: 0;
  }

  aside {
    min-width: 0;
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
