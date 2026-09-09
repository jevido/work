<script lang="ts">
  import { untrack } from "svelte";
  import { avatarUrl } from "../lib/agents/avatar";
  import type { Roster } from "../lib/agents/roster.svelte";
  import { connectOffice, visualStateOf } from "../lib/bridge/office";
  import type { ClaudeSession, TextPart } from "../lib/claude/session.svelte";
  import type { Config } from "../lib/config/config.svelte";
  import { OfficeRenderer, type AgentSpec, type DeskTarget } from "../lib/office/renderer";
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

  /**
   * Where each desk is on screen, and the button sitting on it.
   *
   * A canvas cannot be focused, labelled or read out, so the desks are real
   * buttons positioned over the picture of them. They are the whole pointer
   * and keyboard story for the office: hit-testing in world coordinates was
   * doing the same job for the mouse alone, and having two of them would have
   * meant two things to keep in step.
   *
   * What an agent is doing is deliberately not in the button's name. It would
   * be read out on every focus and go stale between events, and the panel the
   * button opens -- and the console beside it -- say it properly.
   */
  let targets = $state<DeskTarget[]>([]);
  /**
   * The desk buttons themselves, so closing a panel can put the focus back on
   * the desk it opened from. Reactive because `bind:this` writes into it: a
   * plain object drew Svelte's binding_property_non_reactive warning, and the
   * read in close() could not be relied on.
   */
  let deskEls = $state<Record<string, HTMLButtonElement | undefined>>({});

  /**
   * Recomputed whenever the layout could have moved: a resize, or a new cast.
   *
   * The renderer is passed in rather than read from state. Reading it here
   * made this a tracked read of `renderer` inside the very effect that writes
   * it, so setting up the renderer invalidated its own effect and rebuilt the
   * renderer forever -- Svelte's effect_update_depth_exceeded, on load.
   */
  function measureDesks(r: OfficeRenderer | null) {
    targets = r?.deskTargets() ?? [];
  }

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
      // The world is scaled to fit, so every desk is somewhere else now.
      measureDesks(r);
    });
    observer.observe(host);
    r.resize(host.clientWidth, host.clientHeight);

    const disconnect = connectOffice(r);
    r.start();
    measureDesks(r);

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
    measureDesks(r);
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
   * Opens a desk, remembering nothing else: a desk panel is that agent's whole
   * side of the conversation and the profile that says who they are, and
   * neither of those waits for them to be busy -- an idle desk is exactly
   * where you go to read what somebody is for.
   */
  function open(id: string) {
    openId = id;
  }

  /**
   * Closes the panel and puts the focus back on the desk it came from.
   *
   * Without this the caret lands back at the top of the document, and getting
   * to the next desk means tabbing through the whole workbench again.
   */
  function close() {
    const id = openId;
    openId = null;
    if (id) deskEls[id]?.focus();
  }

  /**
   * Lights a desk up. Driven from the buttons, so the highlight follows the
   * keyboard as well as the pointer -- the same cue either way.
   */
  function highlight(id: string | null) {
    renderer?.setHover(id);
  }

  /** What to call a desk. Falls back to the id if the roster has moved on. */
  function nameOf(agentId: string): string {
    return roster.find(agentId)?.name ?? agentId;
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

<!-- Labelled as a group so the desks inside it are announced as somewhere,
     rather than as a run of buttons after the console. -->
<div class="office" bind:this={host} role="group" aria-label="Office floor">
  <!-- Hidden from assistive technology on purpose. It is a picture of state
       that is available as text elsewhere: who is on the team, who is working
       and what they are writing are all in the console and in the desk panels.
       Wandering, lunch and ping pong are not state at all. -->
  <canvas bind:this={canvas} aria-hidden="true"></canvas>

  {#each targets as target (target.id)}
    <button
      class="desk"
      bind:this={deskEls[target.id]}
      style:left="{target.left}px"
      style:top="{target.top}px"
      style:width="{target.width}px"
      style:height="{target.height}px"
      aria-label="{nameOf(target.id)}’s desk"
      aria-haspopup="dialog"
      onclick={() => open(target.id)}
      onpointerenter={() => highlight(target.id)}
      onpointerleave={() => highlight(null)}
      onfocus={() => highlight(target.id)}
      onblur={() => highlight(null)}
    ></button>
  {/each}

  {#if openAgent}
    <AgentDialog
      {session}
      {config}
      agent={openAgent}
      avatar={openAvatar}
      onclose={close}
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

  /*
   * A desk, as far as the pointer and the keyboard are concerned: an invisible
   * button over the monitor the canvas has drawn. No background of its own --
   * the highlight is drawn on the canvas, by the renderer, so a hover and a
   * focus look the same as they always did.
   */
  .desk {
    position: absolute;
    /* A monitor is small, and in a narrow panel the whole world scales down
       with it. The floor keeps the target clickable when the art gets tiny,
       at the cost of reaching a little past the monitor it sits on. */
    min-width: 24px;
    min-height: 24px;
    padding: 0;
    border: none;
    border-radius: 5px;
    background: none;
    cursor: pointer;
  }

  /*
   * Except for focus, which the canvas cannot draw convincingly enough to be
   * the only cue. Two pixels, offset, in the accent colour: the same ring the
   * rest of the workbench uses.
   */
  .desk:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
