<script lang="ts">
  import { MindmapRenderer } from "@mindmap/renderer";
  import { mapRows } from "../lib/doc";
  import type { Viewer } from "../lib/viewer.svelte";

  let { viewer }: { viewer: Viewer } = $props();

  let canvas = $state<HTMLCanvasElement | null>(null);
  let renderer: MindmapRenderer | null = null;

  let scale = $state(1);

  const rows = $derived(mapRows(viewer.contents));

  /**
   * The same map the desktop app draws, from a document this page cannot
   * write to.
   *
   * Read-only is passed to the renderer rather than implied by not wiring a
   * drop handler, because those are different things: a drag with nowhere to
   * send its result still picks a box up, drags it across the screen and puts
   * it back, which looks exactly like an edit that did not save. Nothing here
   * lifts a box at all.
   */
  function scene() {
    return {
      rows,
      regionOf: (id: string) => viewer.regionEntryOf(id),
      tasksOf: (id: string) => viewer.tasksOf(id),
      // Nothing is focused: there is no caret on this page, and a box drawn as
      // selected would be claiming somebody had put it there.
      focused: () => null,
    };
  }

  $effect(() => {
    if (!canvas) return;
    const made = new MindmapRenderer(
      canvas,
      scene,
      () => {},
      (view) => (scale = view.scale),
      { readonly: true },
    );
    renderer = made;
    made.start();
    made.resize();

    const observer = new ResizeObserver(() => made.resize());
    observer.observe(canvas);

    return () => {
      observer.disconnect();
      made.stop();
      renderer = null;
    };
  });

  $effect(() => {
    void rows;
    renderer?.invalidate();
  });
</script>

<div class="map">
  <!--
    aria-hidden for the reason the desktop app's is: everything drawn here is
    in the outline below it, which is a real list of real text. A canvas in the
    accessibility tree would be the same document read out twice, once without
    any structure.
  -->
  <canvas bind:this={canvas} aria-hidden="true"></canvas>

  <div class="chips">
    <span class="chip">{rows.length} {rows.length === 1 ? "line" : "lines"}</span>
    <button class="chip zoom" onclick={() => renderer?.reset()} title="Back to 100%">
      {Math.round(scale * 100)}%
    </button>
  </div>

  {#if rows.length === 0}
    <p class="empty">Nothing to draw yet.</p>
  {/if}
</div>

<style>
  .map {
    position: relative;
    min-height: 320px;
    height: 100%;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel);
  }

  canvas {
    display: block;
    width: 100%;
    height: 100%;
    touch-action: none;
    cursor: grab;
  }

  canvas:active {
    cursor: grabbing;
  }

  .chips {
    position: absolute;
    right: 10px;
    bottom: 10px;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .chip {
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font: inherit;
    font-size: 10.5px;
  }

  .zoom {
    font-family: ui-monospace, monospace;
    cursor: pointer;
  }

  .empty {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    margin: 0;
    color: var(--muted);
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
