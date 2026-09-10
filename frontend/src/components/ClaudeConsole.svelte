<script lang="ts">
  import type { Snippet } from "svelte";
  import type { ClaudeSession, RunStatus } from "../lib/claude/session.svelte";
  import type { ChangeReview as ChangeReviewState } from "../lib/changes/changes.svelte";
  import { followTail } from "../lib/ui/follow";
  import AgentTurn from "./AgentTurn.svelte";
  import ChangeReview from "./ChangeReview.svelte";

  let {
    session,
    review,
    controls,
  }: {
    session: ClaudeSession;
    review: ChangeReviewState;
    /**
     * The app's own controls, rendered at the left of the header row.
     *
     * Passed in rather than built here: what they change -- the permission
     * mode, the config folder -- is nothing to do with the conversation. This
     * component owns the row they sit in and nothing about what is in them.
     */
    controls?: Snippet;
  } = $props();

  let prompt = $state("");
  let scroller: HTMLDivElement | undefined = $state();
  let promptEl: HTMLTextAreaElement | undefined = $state();
  let pinned = true;

  /** True while the change review is on screen. */
  let reviewing = $state(false);
  /** The button that opened it, so closing hands focus back to it. */
  let reviewOpener: HTMLElement | null = null;

  /**
   * What the header button says, and what a screen reader is told.
   *
   * The count is the whole label: "Changes" alone does not say whether there
   * are any, and this button is the only trace the review leaves once the diff
   * stops living in the transcript.
   */
  const changed = $derived(review.files.length);

  function openReview(from: EventTarget | null) {
    reviewOpener = from instanceof HTMLElement ? from : null;
    // Opening it is reading the news, whatever is done next.
    review.markSeen();
    reviewing = true;
  }

  function closeReview() {
    reviewing = false;
    // Reverting the last file empties the list, which unmounts the button that
    // opened this. Focus goes to the composer rather than nowhere.
    const back = reviewOpener?.isConnected ? reviewOpener : promptEl;
    reviewOpener = null;
    back?.focus();
  }

  const statusLabel: Record<RunStatus, string> = {
    idle: "Ready",
    planning: "Routing",
    working: "Working",
    done: "Done",
    cancelled: "Stopped",
    error: "Error",
  };

  // The console is the only text input in the window, so it takes focus on
  // launch. Typing should never require a click first.
  $effect(() => {
    promptEl?.focus();
  });

  function submit() {
    const text = prompt;
    // Only the side channel blocks the composer now: while a run is going,
    // submit puts the question to Anton instead of starting a second run.
    if (!text.trim() || session.chatBusy) return;
    prompt = "";
    pinned = true;
    void session.submit(text);
  }

  function onKeydown(event: KeyboardEvent) {
    // Enter sends; Shift+Enter, Ctrl+Enter and Alt+Enter all make a new line.
    // isComposing guards input methods, where Enter commits a candidate and
    // must never be read as "send".
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.ctrlKey &&
      !event.altKey &&
      !event.metaKey &&
      !event.isComposing
    ) {
      event.preventDefault();
      submit();
      return;
    }
    // Escape stops the run without reaching for the mouse.
    if (event.key === "Escape" && session.busy) {
      event.preventDefault();
      void session.cancel();
    }
  }

  /**
   * Grows the composer with its content, up to a third of the panel. A fixed
   * three-line box makes a long prompt feel like typing through a letterbox.
   */
  function fitComposer() {
    const el = promptEl;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 260)}px`;
  }

  // Re-fit whenever the text changes, including when submit() empties it.
  $effect(() => {
    void prompt;
    fitComposer();
  });

  function onScroll() {
    const el = scroller;
    if (!el) return;
    pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 32;
  }

  // Follow the stream, but stop fighting the user once they scroll up.
  // See followTail: this used to be an effect that read the whole transcript
  // on every frame of a stream to notice that one part of it had grown.
  $effect(() => {
    const el = scroller;
    if (!el) return;
    return followTail(el, () => pinned);
  });
</script>

<section class="console">
  <header>
    <!-- The app's controls, then what the run is doing, at opposite ends of the
         one strip in this column that is not the conversation itself.

         "Claude" used to head this row. It named the panel to somebody who had
         already worked out that the right-hand side is where Claude answers,
         and the space is better spent on the two things that change. -->
    {#if controls}
      <div class="controls">{@render controls()}</div>
    {:else}
      <!-- Keeps the status against the right edge when nothing was handed in,
           which is every use of this component except the app's own. -->
      <div class="controls"></div>
    {/if}

    <!-- The review's only trace in the console. It sits with the run status
         because that is what it is: something the run did, alongside how the
         run is going. Absent when nothing has changed -- an affordance for a
         review of nothing is worse than none. -->
    {#if changed > 0}
      <button
        class="changes"
        class:unseen={review.unseen}
        onclick={(event) => openReview(event.currentTarget)}
        aria-haspopup="dialog"
        aria-expanded={reviewing}
        title="Review what the run changed on disk"
      >
        {#if review.unseen}<span class="pip" aria-hidden="true"></span>{/if}
        <span class="n">{changed}</span>
        changed {changed === 1 ? "file" : "files"}
      </button>
    {:else if !review.tracked}
      <!-- Outside a git repository there is nothing to diff against and revert
           cannot put a file back, so the count would sit at zero for the wrong
           reason. Said once, quietly: a run still works, it just cannot be
           reviewed afterwards. -->
      <span class="untracked" title="Work reviews changes with git, and this folder is not a repository">
        no change review — not a git repository
      </span>
    {/if}

    <div class="status" data-state={session.status}>{statusLabel[session.status]}</div>
  </header>

  <!-- Lives outside the {#if} above so it exists before it has anything to
       say: a region announced into being is a region nobody hears. -->
  <p class="announce" role="status">
    {#if review.unseen}
      {changed} {changed === 1 ? "file" : "files"} changed on disk — review pending.
    {/if}
  </p>

  <div class="scroller" bind:this={scroller} onscroll={onScroll}>
    {#if session.isEmpty}
      <p class="hint">Give Anton work. He decides who on the team should take it.</p>
    {/if}

    {#each session.entries as entry (entry.id)}
      {#if entry.kind === "user"}
        <article class="turn you">
          <div class="speaker">
            <span class="name">You</span>
            {#if entry.agentId}
              <span class="aside">→ {session.nameOf(entry.agentId)}</span>
            {/if}
          </div>
          <div class="said">{entry.text}</div>
        </article>
      {:else if entry.kind === "plan"}
        <article class="turn">
          <div class="speaker">
            <span class="dot" style:background={entry.colour}></span>
            <span class="name">{entry.agentName}</span>
            <span class="aside">
              {entry.mode === "team" ? "split the work" : "kept this one"}
            </span>
          </div>
          <div class="plan">
            {#if entry.reason}<p class="reason">{entry.reason}</p>{/if}
            {#each entry.steps as step (step.agentId)}
              <div class="step">
                <span class="dot" style:background={step.colour}></span>
                <span class="who">{step.agentName}</span>
                <span class="what">
                  {step.task}
                  {#if step.taskId}<span class="ref">{step.taskId}</span>{/if}
                </span>
              </div>
            {/each}
            {#each entry.updates as update (update.taskId)}
              <div class="update">
                <span class="ref">{update.taskId}</span>
                {#if update.status}<span class="badge">{update.status}</span>{/if}
                {#if update.agentName}<span>→ {update.agentName}</span>{/if}
                {#if update.title}<span class="what">“{update.title}”</span>{/if}
              </div>
            {/each}
          </div>
        </article>
      {:else if entry.kind === "agent"}
        <AgentTurn {entry} />
      {:else}
        <div class="notice" class:bad={entry.tone === "error"} role={entry.tone === "error" ? "alert" : undefined}>
          {entry.text}
        </div>
      {/if}
    {/each}
  </div>

  <div class="composer">
    <textarea
      bind:this={promptEl}
      bind:value={prompt}
      onkeydown={onKeydown}
      oninput={fitComposer}
      placeholder={session.busy
        ? "Ask Anton about the work in progress…   (Shift+Enter for a new line)"
        : "Give Anton work…   (Shift+Enter for a new line)"}
      rows="2"
      spellcheck="false"
    ></textarea>
    <div class="actions">
      {#if session.chatBusy}
        <button class="ghost" onclick={() => session.cancelChat()}>Stop answer</button>
      {/if}
      {#if session.busy}
        <button class="ghost" onclick={() => session.cancel()}>Cancel</button>
      {:else if !session.chatBusy}
        <button
          class="ghost"
          onclick={() => session.clear()}
          disabled={session.isEmpty}
          title="Clears the conversation and the agents' memory of it"
        >
          New chat
        </button>
      {/if}
      <button class="primary" onclick={submit} disabled={session.chatBusy || !prompt.trim()}>
        {session.busy ? "Ask" : "Send"}
      </button>
    </div>
  </div>
</section>

<!-- Fixed and centred on the window, so it is not held to the width of the
     column it opens from. -->
{#if reviewing}
  <ChangeReview {review} onclose={closeReview} />
{/if}

<style>
  .console {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
    /* Lets .scroller shrink below its content instead of stretching the page. */
    min-height: 0;
    background: var(--panel);
    border-left: 1px solid var(--line);
  }

  header {
    /* The menus in .controls hang out of this row and over the transcript, so
       it has to be a positioned ancestor that stacks above what it covers. */
    position: relative;
    z-index: 2;
    display: flex;
    align-items: center;
    gap: 8px;
    /* Padding trimmed from 10px to keep the row at the same 40px it was: the
       controls are taller than the line of text that used to be here, and a
       header that changed height would push the whole transcript down. */
    padding: 7px 12px;
    border-bottom: 1px solid var(--line);
    /* The review button is the only thing in here that comes and goes, and a
       header that grows when it appears pushes the whole transcript down to
       announce itself -- which is the interruption moving the review out of the
       transcript was meant to end. */
    min-height: 40px;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 8px;
    /* Pushes the review button and the status to the right edge together, so
       the status does not move as the button comes and goes. */
    margin-right: auto;
  }

  /* Quiet by default: changes that have been looked at are a fact about the
     run, not something being asked of you. */
  .changes {
    display: flex;
    align-items: center;
    gap: 5px;
    flex: none;
    /* Sized to sit inside the header's line box rather than to stretch it: at
       the inherited line-height this pill is 23px tall against a 19.5px line,
       and the 3px it gained was the header's to lose. */
    padding: 3px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    line-height: 1;
    cursor: pointer;
  }

  .changes .n {
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }

  .changes:hover {
    border-color: #3a4250;
    color: var(--text);
  }

  /* Until it has been opened once, it is news. Same accent the office uses for
     work in progress, plus a dot -- the state survives a screenshot and a
     reader who cannot see the colour. */
  .changes.unseen {
    border-color: var(--accent);
    color: var(--text);
  }

  .changes.unseen .pip {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
  }

  /* Not a button and not news: nothing here can be opened, so it must not look
     like the pill it stands in for. */
  .untracked {
    flex: none;
    color: var(--muted);
    font-size: 11px;
  }

  /* Announced, never drawn: the button carries this on screen. */
  .announce {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  .status {
    font-size: 11px;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .status[data-state="planning"],
  .status[data-state="working"] {
    color: var(--accent);
  }

  .status[data-state="done"] {
    color: var(--ok);
  }

  .status[data-state="error"] {
    color: var(--err);
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
  }

  .turn {
    margin-bottom: 16px;
  }

  .speaker {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 5px;
  }

  .name {
    font-weight: 600;
    font-size: 12px;
  }

  .aside {
    color: var(--muted);
    font-size: 11px;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex: none;
  }

  /* Your own words sit in a block of their own, so the conversation reads as
     an exchange rather than a log of replies. */
  .you .name {
    color: var(--muted);
  }

  .said {
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--panel-2);
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .plan {
    padding-left: 14px;
    border-left: 1px solid var(--line);
  }

  .reason {
    margin: 0 0 8px;
    color: var(--muted);
    font-size: 12px;
    user-select: text;
  }

  .step {
    display: grid;
    grid-template-columns: auto auto 1fr;
    align-items: start;
    gap: 6px;
    padding: 3px 0;
    font-size: 12px;
    user-select: text;
  }

  .step .dot {
    margin-top: 5px;
  }

  .step .who {
    justify-self: start;
    font-weight: 600;
  }

  .step .what {
    color: var(--muted);
  }

  /* Board bookkeeping Anton did on the way past: closing a task you named,
     renaming one, handing one over. */
  .update {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    padding: 3px 0;
    color: var(--muted);
    font-size: 11.5px;
    user-select: text;
  }

  .ref {
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10.5px;
    color: var(--text);
  }

  .badge {
    padding: 1px 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .notice {
    margin-bottom: 16px;
    color: var(--muted);
    font-size: 11px;
    user-select: text;
  }

  .notice.bad {
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
  }

  .composer {
    border-top: 1px solid var(--line);
    padding: 10px 12px 12px;
  }

  textarea {
    width: 100%;
    /* Height is driven by content in fitComposer, so the handle would lie. */
    resize: none;
    overflow-y: auto;
    min-height: 2.6em;
    max-height: 260px;
    padding: 8px 9px;
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
    justify-content: flex-end;
    gap: 8px;
    margin-top: 8px;
  }

  button {
    padding: 6px 12px;
    border-radius: 6px;
    border: 1px solid var(--line);
    background: var(--panel-2);
    cursor: pointer;
  }

  button:disabled {
    opacity: 0.45;
    cursor: default;
  }

  button.primary {
    border-color: transparent;
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
  }

  button.ghost:hover:not(:disabled),
  button.primary:hover:not(:disabled) {
    filter: brightness(1.12);
  }
</style>
