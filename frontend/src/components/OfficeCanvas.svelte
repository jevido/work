<script lang="ts">
  import { connectOffice } from "../lib/bridge/office";
  import type { ClaudeSession, TextPart } from "../lib/claude/session.svelte";
  import { OfficeRenderer, type AgentSpec } from "../lib/office/renderer";
  import AgentDialog from "./AgentDialog.svelte";
  import PerfOverlay from "./PerfOverlay.svelte";

  let {
    agents,
    session,
    showPerf = false,
  }: { agents: AgentSpec[]; session: ClaudeSession; showPerf?: boolean } = $props();

  let canvas: HTMLCanvasElement;
  let host: HTMLDivElement;
  let renderer: OfficeRenderer | null = $state(null);

  /** The desk whose panel is open, if any. */
  let openId = $state<string | null>(null);
  /** A one-off line explaining a click that did nothing. */
  let cue = $state<string | null>(null);
  let cueHandle = 0;

  /** True while the pointer is over a monitor you can actually open. */
  let hot = $state(false);

  const openAgent = $derived(agents.find((a) => a.id === openId) ?? null);

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

  /**
   * The live readout on the desks.
   *
   * The renderer is not reactive, so this is the single place the console's
   * stream crosses into it. Text accumulates per turn and flushes once per
   * animation frame, so this runs at most once a frame while somebody is
   * typing and not at all otherwise -- and each agent gets one call, with the
   * id of the run of prose so the renderer can tell a continuation from a new
   * run. Wrapping, scrolling and whether any of it is worth redrawing are the
   * renderer's business, not this component's.
   */
  $effect(() => {
    const r = renderer;
    if (!r) return;
    for (const spec of agents) {
      const live = liveTextFor(session, spec.id);
      r.setMonitorText(spec.id, live?.id ?? "", live?.text ?? "");
    }
  });

  // An agent who leaves the roster cannot keep a panel open.
  $effect(() => {
    if (openId && !agents.some((a) => a.id === openId)) openId = null;
  });

  $effect(() => () => clearTimeout(cueHandle));

  /**
   * The agent under a pointer event, and whether their desk is open to
   * visitors. Only a working agent has anything to show, and that judgement is
   * the frontend's: it is the same animation state the monitor is lit from.
   */
  function monitorAt(event: PointerEvent | MouseEvent): { id: string; working: boolean } | null {
    const r = renderer;
    if (!r) return null;
    const box = canvas.getBoundingClientRect();
    const id = r.hitTestMonitor(event.clientX - box.left, event.clientY - box.top);
    if (!id) return null;
    return { id, working: r.stateOf(id) === "working" };
  }

  function onPointerMove(event: PointerEvent) {
    const hit = monitorAt(event);
    // Only a working monitor lights up, so the cursor and the glow always agree
    // with each other and with what a click will do.
    const target = hit?.working ? hit.id : null;
    renderer?.setHover(target);
    hot = target !== null;
  }

  function onPointerLeave() {
    renderer?.setHover(null);
    hot = false;
  }

  function onClick(event: MouseEvent) {
    const hit = monitorAt(event);
    if (!hit) return;
    if (hit.working) {
      showCue(null);
      openId = hit.id;
      return;
    }
    // Clicking a dark monitor is a fair thing to try. Silence would read as a
    // broken control, so say why nothing opened.
    const name = agents.find((a) => a.id === hit.id)?.name ?? "This agent";
    showCue(`${name} isn’t working on anything right now.`);
  }

  /**
   * The run of prose an agent is writing at this moment, or null when they are
   * not mid-turn.
   *
   * Scanned from the end and stopped at the request that started the run: a
   * streaming turn is always among the last few entries, so the cost does not
   * grow with the length of the conversation.
   */
  function liveTextFor(s: ClaudeSession, agentId: string): TextPart | null {
    const entries = s.entries;
    for (let i = entries.length - 1; i >= 0; i--) {
      const entry = entries[i];
      if (entry.kind === "user") break;
      if (entry.kind !== "agent") continue;
      if (entry.agentId !== agentId || entry.status !== "streaming") continue;
      // The last run of prose, tool calls after it included: while a tool is
      // running there is nothing new to say, and the last thing they said
      // should stay on the glass rather than blink out and come back.
      for (let j = entry.parts.length - 1; j >= 0; j--) {
        const part = entry.parts[j];
        if (part.kind === "text") return part;
      }
      return null;
    }
    return null;
  }

  function showCue(text: string | null) {
    clearTimeout(cueHandle);
    cue = text;
    if (text) cueHandle = setTimeout(() => (cue = null), 2600);
  }
</script>

<div class="office" bind:this={host}>
  <canvas
    bind:this={canvas}
    class:hot
    onpointermove={onPointerMove}
    onpointerleave={onPointerLeave}
    onclick={onClick}
  ></canvas>

  {#if cue}
    <div class="cue" role="status">{cue}</div>
  {/if}

  {#if openAgent}
    <AgentDialog
      {session}
      agentId={openAgent.id}
      name={openAgent.name}
      colour={openAgent.colour}
      onclose={() => (openId = null)}
    />
  {/if}

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

  canvas.hot {
    cursor: pointer;
  }

  .cue {
    position: absolute;
    z-index: 3;
    left: 50%;
    /* Above the panel and clear of the load-error in the corner, so a cue
       never lands on top of what it is explaining. */
    top: 12px;
    transform: translateX(-50%);
    padding: 6px 11px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel);
    color: var(--muted);
    font-size: 12px;
    white-space: nowrap;
    pointer-events: none;
  }
</style>
