<script lang="ts">
  import { untrack } from "svelte";
  import { avatarUrl } from "../lib/agents/avatar";
  import type { Roster } from "../lib/agents/roster.svelte";
  import { connectOffice, visualStateOf } from "../lib/bridge/office";
  import type { ClaudeSession, TextPart } from "../lib/claude/session.svelte";
  import type { Config } from "../lib/config/config.svelte";
  import { OfficeRenderer, type AgentSpec } from "../lib/office/renderer";
  import AgentDialog from "./AgentDialog.svelte";
  import PerfOverlay from "./PerfOverlay.svelte";

  let {
    roster,
    session,
    config,
    showPerf = false,
  }: {
    roster: Roster;
    session: ClaudeSession;
    /** Passed straight through to the desk panel's profile view. */
    config: Config;
    showPerf?: boolean;
  } = $props();

  let canvas: HTMLCanvasElement;
  let host: HTMLDivElement;
  let renderer: OfficeRenderer | null = $state(null);

  /** The desk whose panel is open, if any. */
  let openId = $state<string | null>(null);

  /** True while the pointer is over a desk. */
  let hot = $state(false);

  const openAgent = $derived(openId ? roster.find(openId) : null);

  /** The open desk's portrait, for the profile view inside its panel. */
  const openAvatar = $derived(openAgent ? avatarUrl(openAgent, roster.revision) : null);

  /**
   * Who is in the office, and where they sit.
   *
   * The renderer's cast is rebuilt from this and nothing else. Building it
   * allocates a new set of agents and puts everybody back at their opening
   * position, which is right when the team changes and wrong for a rename: it
   * would stop a walk mid-stride and blank a monitor still being typed on.
   * Names are pushed at the renderer on their own, below.
   *
   * Desks are in the key as well as ids, because reloading the config folder
   * can lay the room out again for the same people -- add one agent's folder
   * and the seating is re-dealt. An id list alone would leave everybody drawn
   * at the desk they used to have.
   */
  const castKey = $derived(
    roster.list
      .map((a) => `${a.id}:${a.desk.x},${a.desk.y},${a.desk.seatX},${a.desk.seatY}`)
      .join("\u0000"),
  );

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
    void castKey;
    const r = renderer;
    if (!r) return;
    r.setAgents(
      untrack(() =>
        roster.list.map(
          (a): AgentSpec => ({
            id: a.id,
            name: a.name,
            colour: a.colour,
            deskX: a.desk.x,
            deskY: a.desk.y,
            seatX: a.desk.seatX,
            seatY: a.desk.seatY,
            // Carried in the spec as well as pushed below, so a cast rebuilt
            // for a seating change does not open faceless and fill in after.
            avatar: avatarUrl(a, roster.revision) ?? undefined,
            // A run can outlive the page: dev reloads happen mid-stream, and
            // the office has to open showing the work already underway.
            state: visualStateOf(a.state),
          }),
        ),
      ),
    );
  });

  /**
   * The faces on the desks.
   *
   * Separate from the cast for the same reason a rename is: an avatar added to
   * a folder is not a reason to re-seat the room and stop everybody
   * mid-stride. It follows the roster's revision as well as its list, because
   * a picture can be replaced with the roster otherwise unchanged -- same
   * folder, same filename -- and that is exactly the case a reload has to
   * pick up. Each call is a no-op unless the URL actually changed.
   */
  $effect(() => {
    const r = renderer;
    if (!r) return;
    const revision = roster.revision;
    for (const a of roster.list) r.setAgentAvatar(a.id, avatarUrl(a, revision));
  });

  // A rename, which is the one part of an agent the office has to be able to
  // take while it is running. One call each, and each one is a no-op unless
  // that nameplate actually changed.
  $effect(() => {
    const r = renderer;
    if (!r) return;
    for (const a of roster.list) r.setAgentName(a.id, a.name);
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
    for (const agent of roster.list) {
      const live = liveTextFor(session, agent.id);
      r.setMonitorText(agent.id, live?.id ?? "", live?.text ?? "");
    }
  });

  // An agent who leaves the roster cannot keep a panel open.
  $effect(() => {
    if (openId && !roster.list.some((a) => a.id === openId)) openId = null;
  });

  /**
   * The agent whose desk is under a pointer event.
   *
   * Every desk answers, whatever its owner is up to. A desk panel is that
   * agent's whole side of the conversation and the profile that says who they
   * are, and neither of those waits for them to be busy -- an idle desk is
   * exactly where you go to read what somebody is for.
   */
  function deskAt(event: PointerEvent | MouseEvent): string | null {
    const r = renderer;
    if (!r) return null;
    const box = canvas.getBoundingClientRect();
    return r.hitTestMonitor(event.clientX - box.left, event.clientY - box.top);
  }

  function onPointerMove(event: PointerEvent) {
    // The cursor and the highlight are set from the same hit test as the
    // click, so what lights up is always what will open.
    const target = deskAt(event);
    renderer?.setHover(target);
    hot = target !== null;
  }

  function onPointerLeave() {
    renderer?.setHover(null);
    hot = false;
  }

  function onClick(event: MouseEvent) {
    const id = deskAt(event);
    if (!id) return;
    openId = id;
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

</script>

<div class="office" bind:this={host}>
  <canvas
    bind:this={canvas}
    class:hot
    onpointermove={onPointerMove}
    onpointerleave={onPointerLeave}
    onclick={onClick}
  ></canvas>

  {#if openAgent}
    <AgentDialog
      {session}
      {config}
      agent={openAgent}
      avatar={openAvatar}
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
</style>
