<script lang="ts">
  import type { AgentEntry, ClaudeSession, UserEntry } from "../lib/claude/session.svelte";
  import AgentTurn from "./AgentTurn.svelte";

  let {
    session,
    agentId,
    name,
    colour,
    onclose,
  }: {
    session: ClaudeSession;
    agentId: string;
    name: string;
    colour: string;
    onclose: () => void;
  } = $props();

  let prompt = $state("");
  let root: HTMLDivElement | undefined = $state();
  let promptEl: HTMLTextAreaElement | undefined = $state();
  let scroller: HTMLDivElement | undefined = $state();
  let pinned = $state(true);

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
    prompt = "";
    pinned = true;
    // Focus lands inside the panel either way, so Escape closes it from the
    // keyboard without having to click something first.
    (promptEl ?? root)?.focus();
  });

  // Follow the stream, but stop fighting the user once they scroll up.
  $effect(() => {
    // Touch what grows so this reruns as the turn extends.
    for (const entry of thread) {
      if (entry.kind !== "agent") continue;
      void entry.parts.length;
      for (const part of entry.parts) {
        if (part.kind === "text") void part.text.length;
        else void part.call.done;
      }
    }
    const el = scroller;
    if (!el || !pinned) return;
    el.scrollTop = el.scrollHeight;
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
    // Escape closes the panel and nothing else. The run keeps going -- putting
    // the window away is not a decision to throw the work out.
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onclose();
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

  $effect(() => {
    void prompt;
    fitComposer();
  });
</script>

<!-- Deliberately not a modal: the office behind it is the thing you are
     watching, and clicking another working desk should just swap this panel
     over rather than being swallowed by a backdrop. -->
<div
  class="dialog"
  bind:this={root}
  role="dialog"
  aria-label="{name}’s desk"
  tabindex="-1"
  onkeydown={onKeydown}
>
  <header>
    <span class="dot" style:background={colour}></span>
    <span class="name">{name}</span>
    {#if streaming}
      <span class="live" style:color={colour}>working</span>
    {/if}
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">
      ✕
    </button>
  </header>

  <div class="scroller" bind:this={scroller} onscroll={onScroll}>
    {#if thread.length === 0}
      <p class="hint">
        Nothing from {name} in this conversation yet. Ask him something below and
        it goes straight to his desk.
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
      oninput={fitComposer}
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

  .close {
    margin-left: auto;
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
