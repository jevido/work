<script lang="ts">
  import { connectOffice } from "../lib/bridge/office";
  import { OfficeRenderer, type AgentSpec } from "../lib/office/renderer";
  import PerfOverlay from "./PerfOverlay.svelte";

  let {
    agents,
    showPerf = false,
  }: { agents: AgentSpec[]; showPerf?: boolean } = $props();

  let canvas: HTMLCanvasElement;
  let host: HTMLDivElement;
  let renderer: OfficeRenderer | null = $state(null);

  $effect(() => {
    const r = new OfficeRenderer(canvas);
    renderer = r;

    const observer = new ResizeObserver((entries) => {
      const box = entries[0]?.contentRect;
      if (box) r.resize(box.width, box.height);
    });
    observer.observe(host);
    r.resize(host.clientWidth, host.clientHeight);

    const disconnect = connectOffice(r);
    r.start();

    return () => {
      disconnect();
      r.stop();
      observer.disconnect();
      renderer = null;
    };
  });

  // The cast can change (agents loaded, or reconfigured later) without
  // rebuilding the renderer.
  $effect(() => {
    renderer?.setAgents(agents);
  });
</script>

<div class="office" bind:this={host}>
  <canvas bind:this={canvas}></canvas>
  {#if showPerf && renderer}
    <PerfOverlay stats={() => renderer!.stats} />
  {/if}
</div>

<style>
  .office {
    position: relative;
    height: 100%;
    min-width: 0;
    overflow: hidden;
    background: #0b0d11;
  }

  canvas {
    display: block;
  }
</style>
