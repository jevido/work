<script lang="ts">
  import { untrack } from "svelte";
  import type { AgentStatus } from "../../bindings/dev.jevido/work/internal/workbench/models.js";
  import type { AgentEntry, ClaudeSession, UserEntry } from "../lib/claude/session.svelte";
  import type { Config } from "../lib/config/config.svelte";
  import { PanelDrag, dragHandle } from "../lib/ui/drag.svelte";
  import { followTail } from "../lib/ui/follow";
  import AgentProfile from "./AgentProfile.svelte";
  import AgentTurn from "./AgentTurn.svelte";

  let {
    session,
    config,
    agent,
    avatar,
    onclose,
  }: {
    session: ClaudeSession;
    /** Only so the profile view can say which folder to edit this agent in. */
    config: Config;
    agent: AgentStatus;
    /**
     * Where to fetch this agent's picture, or null if their folder holds none.
     * Passed in rather than derived here: the office owns how a folder on disk
     * becomes a URL, and this panel only ever shows one.
     */
    avatar: string | null;
    onclose: () => void;
  } = $props();

  const agentId = $derived(agent.id);
  const name = $derived(agent.name);
  const colour = $derived(agent.colour);

  /** True while the profile has replaced the thread. */
  let showProfile = $state(false);

  let prompt = $state("");
  let root: HTMLDivElement | undefined = $state();
  let promptEl: HTMLTextAreaElement | undefined = $state();
  let scroller: HTMLDivElement | undefined = $state();
  let pinned = $state(true);

  /**
   * Where this panel has been dragged to.
   *
   * Kept across desk switches on purpose: it is the same window in the same
   * place, showing somebody else. Moving it out of the way of a desk you want
   * to watch should not have to be done twice.
   */
  const drag = new PanelDrag();

  /**
   * This agent's side of the conversation, read straight off the live entries
   * rather than copied: closing the panel keeps nothing, and reopening it -- or
   * switching to another desk -- shows where things actually stand now.
   *
   * What you said to this agent directly belongs here too. Work you gave the
   * team does not: Anton routed that, and it reads as a different exchange.
   */
  const thread = $derived(
    session.entries.filter(
      (e): e is AgentEntry | UserEntry =>
        (e.kind === "agent" || e.kind === "user") && e.agentId === agentId,
    ),
  );

  /** Whether this agent is mid-turn, which is not the same as the run's status. */
  const streaming = $derived(
    thread.some((e) => e.kind === "agent" && e.status === "streaming"),
  );

  // Switching desks is a new conversation to read: start at the bottom of it,
  // and hand the caret over so you can reply without clicking first.
  $effect(() => {
    void agentId;
    // Everything below is untracked on purpose, and the panel does not work
    // without it. `promptEl` is bound to the composer, which only exists while
    // the thread is showing. Reading it here made this effect depend on it, so
    // opening the profile -- which unmounts the composer and sets promptEl to
    // undefined -- re-ran this effect and set `showProfile` straight back to
    // false. The button looked dead.
    //
    // Whether it bounced came down to effect flush order, so it reproduced in
    // WebKitGTK, which is the engine Wails uses on Linux, and not in Chromium.
    // This is a "the desk changed" effect: only the desk belongs above.
    untrack(() => {
      prompt = "";
      pinned = true;
      // Left open, it would now be showing somebody else's profile under this
      // agent's name.
      showProfile = false;
      // Focus lands inside the panel either way, so Escape closes it from the
      // keyboard without having to click something first.
      (promptEl ?? root)?.focus();
    });
  });

  // Follow the stream, but stop fighting the user once they scroll up.
  // Shared with the console; see followTail.
  $effect(() => {
    const el = scroller;
    if (!el) return;
    return followTail(el, () => pinned);
  });

  function onScroll() {
    const el = scroller;
    if (!el) return;
    pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 32;
  }

  function send() {
    const text = prompt;
    if (!text.trim() || session.busy) return;
    prompt = "";
    pinned = true;
    void session.submit(text, agentId);
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      // From the profile, Escape goes back to the thread rather than closing
      // the window: it is a second view of the same desk, and nothing in it
      // can be lost by leaving. From the thread it closes the panel and
      // nothing else -- the run keeps going, since putting the window away is
      // not a decision to throw the work out.
      if (showProfile) showProfile = false;
      else onclose();
      return;
    }
    // While a run is in progress there is nothing to send, so Enter goes back
    // to being a newline rather than a key that does nothing.
    if (
      event.key === "Enter" &&
      !session.busy &&
      !event.shiftKey &&
      !event.ctrlKey &&
      !event.altKey &&
      !event.metaKey &&
      !event.isComposing
    ) {
      event.preventDefault();
      send();
    }
  }

  /** Grows the composer with its content, like the main console's. */
  function fitComposer() {
    const el = promptEl;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
  }

  // The only caller: `oninput` did this too, which measured and resized the
  // box twice per keystroke, each one a forced layout. See the console's.
  $effect(() => {
    void prompt;
    fitComposer();
  });

  // Coming back from the profile, the caret goes where it was: in the
  // composer, ready to say something to the agent whose profile you were just
  // reading.
  $effect(() => {
    if (showProfile) return;
    (promptEl ?? root)?.focus();
  });
</script>

<!-- Deliberately not a modal: the office behind it is the thing you are
     watching, and clicking another desk should just swap this panel over
     rather than being swallowed by a backdrop. -->
<div
  class="dialog"
  bind:this={root}
  role="dialog"
  aria-label="{name}’s desk"
  tabindex="-1"
  style:transform={drag.transform}
  onkeydown={onKeydown}
>
  <!-- Drag it by the header: the office underneath is the thing being watched,
       and a panel parked over the desk you care about is worse than no panel. -->
  <header {@attach dragHandle(drag)}>
    <span class="dot" style:background={colour}></span>
    <span class="name">{name}</span>
    {#if streaming}
      <span class="live" style:color={colour}>working</span>
    {/if}
    <!-- Next to Close because it is the same kind of thing: what this window
         is showing, rather than anything the agent is doing. A person rather
         than a pencil, because nothing in there can be typed into any more --
         the folder on disk is where an agent is edited. -->
    <button
      class="edit"
      class:on={showProfile}
      onclick={() => (showProfile = !showProfile)}
      aria-pressed={showProfile}
      aria-label="Profile"
      title={showProfile ? "Back to the conversation" : `${name}’s profile`}
    >
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path
          d="M8 8.4a3.1 3.1 0 1 0 0-6.2 3.1 3.1 0 0 0 0 6.2Zm0 1.3c-3 0-5.4 1.6-5.4 3.5 0 .4.3.7.7.7h9.4c.4 0 .7-.3.7-.7 0-1.9-2.4-3.5-5.4-3.5Z"
          fill="currentColor"
        />
      </svg>
    </button>
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">
      ✕
    </button>
  </header>

  {#if showProfile}
    <AgentProfile {agent} {config} {avatar} />
  {:else}
    <div class="scroller" bind:this={scroller} onscroll={onScroll}>
      {#if thread.length === 0}
        <p class="hint">
          Nothing from {name} in this conversation yet. Ask them something below
          and it goes straight to their desk.
        </p>
      {/if}

      {#each thread as entry (entry.id)}
        {#if entry.kind === "user"}
          <div class="said">{entry.text}</div>
        {:else}
          <!-- The name is in the header; repeating it on every turn is noise in a
               panel that only ever shows one person. -->
          <AgentTurn {entry} showName={false} />
        {/if}
      {/each}
    </div>

    <div class="composer">
      <textarea
        bind:this={promptEl}
        bind:value={prompt}
        onkeydown={onKeydown}
        placeholder="Ask {name} directly…"
        rows="1"
        spellcheck="false"
      ></textarea>
      <div class="actions">
        {#if session.busy}
          <!-- One run at a time is the workbench's design. Say why Send is dark
               rather than leaving a dead button to be poked at -- and leave the
               box itself live, so a follow-up can be written while you watch.
               It sends the moment the run ends. -->
          <span class="note">Sends when this run ends</span>
        {/if}
        <button class="primary" onclick={send} disabled={session.busy || !prompt.trim()}>
          Send
        </button>
      </div>
    </div>
  {/if}
</div>

<style>
  .dialog {
    position: absolute;
    outline: none;
    z-index: 2;
    left: 12px;
    bottom: 12px;
    display: flex;
    flex-direction: column;
    width: min(420px, calc(100% - 24px));
    max-height: min(72%, 520px);
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    box-shadow: 0 12px 32px rgb(0 0 0 / 0.45);
    overflow: hidden;
  }

  header {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 9px 10px 9px 12px;
    border-bottom: 1px solid var(--line);
    background: var(--panel-2);
  }

  .dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex: none;
  }

  .name {
    font-weight: 600;
  }

  .live {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  /* Sits where the close button used to start, so Close stays the last thing
     in the header and does not move as the toggle appears. */
  .edit {
    margin-left: auto;
    display: flex;
    padding: 3px 6px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }

  .edit svg {
    width: 13px;
    height: 13px;
  }

  .edit:hover {
    border-color: var(--line);
    color: var(--text);
  }

  /* Held down while the profile is up: the header says which of the two things
     this panel can show you is on screen. */
  .edit.on {
    border-color: var(--line);
    background: var(--panel);
    color: var(--accent);
  }

  .close {
    padding: 2px 7px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }

  .close:hover {
    border-color: var(--line);
    color: var(--text);
  }

  .scroller {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
    padding: 12px;
  }

  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }

  .said {
    margin-bottom: 12px;
    padding: 7px 9px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--panel-2);
    font-size: 12px;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .composer {
    border-top: 1px solid var(--line);
    padding: 9px 10px 10px;
  }

  textarea {
    width: 100%;
    resize: none;
    overflow-y: auto;
    min-height: 2.4em;
    max-height: 140px;
    padding: 7px 8px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel-2);
    color: var(--text);
    font: inherit;
    user-select: text;
  }

  textarea:focus {
    outline: none;
    border-color: #3a4250;
  }

  .actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 7px;
  }

  .note {
    color: var(--muted);
    font-size: 11px;
  }

  button.primary {
    padding: 5px 12px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
    cursor: pointer;
  }

  button.primary:disabled {
    opacity: 0.45;
    cursor: default;
  }

  button.primary:hover:not(:disabled) {
    filter: brightness(1.12);
  }
</style>
