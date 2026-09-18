<script lang="ts">
  import { Events } from "@wailsio/runtime";

  import * as Workbench from "../../bindings/dev.jevido/work/apps/studio/internal/bridge/workbenchservice.js";
  import type { PlanTask } from "../../bindings/dev.jevido/work/apps/studio/internal/workbench/models.js";
  import { BOARD_UPDATED, RUN_FINISHED, WORKSPACE_SYNC } from "../lib/bridge/events";

  let { running }: { running: boolean } = $props();

  let next = $state<PlanTask | null>(null);
  let refused = $state<string | null>(null);
  let starting = $state(false);

  /*
    What "next" is comes from the backend rather than from the board on screen.
    The board is this machine's runs; the plan is the workspace's order, and the
    two are not the same list -- deciding here which card looks next would be a
    second definition of next, disagreeing with the first the moment anybody
    reorders anything.
  */
  async function refresh() {
    try {
      const [task, ok] = await Workbench.NextTask();
      next = ok ? task : null;
    } catch {
      // No workspace, or a backend without this call. Nothing to offer is not
      // an error and has nothing to say.
      next = null;
    }
  }

  $effect(() => {
    void refresh();
    // The plan moves when anybody edits it, the board moves when a run does,
    // and a finished run is the moment somebody is most likely to want the next
    // one. All three change the answer.
    const offs = [BOARD_UPDATED, WORKSPACE_SYNC, RUN_FINISHED].map((name) =>
      Events.On(name, () => void refresh()),
    );
    return () => offs.forEach((off) => off());
  });

  async function start() {
    if (starting || running) return;
    starting = true;
    refused = null;
    try {
      await Workbench.StartNextTask();
      await refresh();
    } catch (err) {
      refused = err instanceof Error ? err.message : String(err);
    } finally {
      starting = false;
    }
  }
</script>

{#if next}
  <section class="next" aria-label="Next task">
    <p class="what">
      <span class="label">Next</span>
      <span class="title">{next.text}</span>
      {#if next.fromText}
        <!-- The reason it is on the plan. A task without it is a line somebody
             has to go and look up, and the link back is the spine of all this. -->
        <span class="from">from “{next.fromText}”</span>
      {/if}
    </p>

    <!--
      The button says what it will do. "Start next task" on its own is a button
      nobody presses twice, because after the first time they cannot tell what
      it is about to run.
    -->
    <button onclick={start} disabled={running || starting}>
      {#if running}
        A run is in flight
      {:else if starting}
        Starting…
      {:else}
        Start “{next.text}”
      {/if}
    </button>

    {#if refused}
      <p class="refused" role="alert">{refused}</p>
    {/if}
  </section>
{/if}

<style>
  .next {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    padding: 6px 8px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel);
    margin-bottom: 8px;
  }

  .what {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin: 0;
    min-width: 0;
    flex: 1;
    font-size: 12px;
  }

  .label {
    flex-shrink: 0;
    font-size: 10px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .title {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .from {
    flex-shrink: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--muted);
    font-size: 11px;
  }

  button {
    flex-shrink: 0;
    padding: 3px 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .refused {
    width: 100%;
    margin: 0;
    font-size: 11px;
    color: var(--warn, #c66);
  }
</style>
