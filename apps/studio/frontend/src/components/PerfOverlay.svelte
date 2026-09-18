<script lang="ts">
  import type { FrameStats } from "../lib/office/perf";

  /**
   * Reads the renderer's stats on a slow timer. The stats object is mutated in
   * place by the render loop, so sampling it here keeps the overlay honest
   * without making the loop reactive.
   */
  let { stats }: { stats: () => FrameStats } = $props();

  let fps = $state(0);
  let frameMs = $state(0);
  let peakMs = $state(0);
  let agents = $state(0);
  let patches = $state(0);

  $effect(() => {
    const id = setInterval(() => {
      const s = stats();
      fps = s.fps;
      frameMs = s.frameMs;
      peakMs = s.peakMs;
      agents = s.agents;
      patches = s.patches;
    }, 250);
    return () => clearInterval(id);
  });
</script>

<div class="overlay">
  <span><b>{fps.toFixed(0)}</b> fps</span>
  <span><b>{frameMs.toFixed(2)}</b> ms</span>
  <span>peak <b>{peakMs.toFixed(2)}</b> ms</span>
  <span><b>{agents}</b> agents</span>
  <!-- The scene is drawn once per patch, so this is the multiplier on the
       millisecond figures beside it. "full" is one whole-canvas repaint. -->
  <span>{patches === 0 ? "full" : `${patches} patch`}</span>
</div>

<style>
  .overlay {
    position: absolute;
    top: 10px;
    left: 10px;
    display: flex;
    gap: 12px;
    padding: 5px 9px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: rgba(10, 12, 16, 0.78);
    color: var(--muted);
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 11px;
    line-height: 1.2;
    pointer-events: none;
  }

  b {
    color: var(--text);
    font-weight: 600;
  }
</style>
