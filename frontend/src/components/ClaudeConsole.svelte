<script lang="ts">
  import type { ClaudeSession, Phase, RunStatus } from "../lib/claude/session.svelte";
  import type { ChangeReview as ChangeReviewState } from "../lib/changes/changes.svelte";
  import ChangeReview from "./ChangeReview.svelte";
  import ToolRow from "./ToolRow.svelte";

  let {
    session,
    review,
  }: { session: ClaudeSession; review: ChangeReviewState } = $props();

  let prompt = $state("");
  let scroller: HTMLDivElement | undefined = $state();
  let promptEl: HTMLTextAreaElement | undefined = $state();
  let pinned = true;

  const statusLabel: Record<RunStatus, string> = {
    idle: "Ready",
    planning: "Routing",
    working: "Working",
    done: "Done",
    cancelled: "Stopped",
    error: "Error",
  };

  // Only the synthesis turn is worth labelling: a plain work turn is obvious
  // from the agent's name, and routing has an entry of its own.
  const phaseLabel: Record<Phase, string> = {
    plan: "",
    work: "",
    synthesis: "bringing it together",
  };

  // The console is the only text input in the window, so it takes focus on
  // launch. Typing should never require a click first.
  $effect(() => {
    promptEl?.focus();
  });

  function submit() {
    const text = prompt;
    if (!text.trim() || session.busy) return;
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
  $effect(() => {
    // Touch what grows so this reruns as the conversation extends.
    void session.entries.length;
    for (const entry of session.entries) {
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
</script>

<section class="console">
  <header>
    <div class="title">Claude</div>
    <div class="status" data-state={session.status}>{statusLabel[session.status]}</div>
  </header>

  <div class="scroller" bind:this={scroller} onscroll={onScroll}>
    {#if session.isEmpty}
      <p class="hint">Give Anton work. He decides whether to bring in Jeff or Chris.</p>
    {/if}

    {#each session.entries as entry (entry.id)}
      {#if entry.kind === "user"}
        <article class="turn you">
          <div class="speaker"><span class="name">You</span></div>
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
        <article class="turn" data-status={entry.status}>
          <div class="speaker">
            <span class="dot" style:background={entry.colour}></span>
            <span class="name">{entry.agentName}</span>
            {#if phaseLabel[entry.phase]}
              <span class="aside">{phaseLabel[entry.phase]}</span>
            {/if}
            {#if entry.status === "streaming"}
              <span class="pulse" style:background={entry.colour}></span>
            {/if}
          </div>

          {#each entry.parts as part (part.id)}
            {#if part.kind === "text"}
              {#if part.text.trim()}<pre>{part.text}</pre>{/if}
            {:else}
              <div class="tools"><ToolRow call={part.call} /></div>
            {/if}
          {/each}

          {#if entry.error}
            <div class="error" role="alert">{entry.error}</div>
          {/if}

          {#if entry.status !== "streaming" && entry.durationMs > 0}
            <div class="footnote">
              {(entry.durationMs / 1000).toFixed(1)}s
              {#if entry.costUsd > 0}· ${entry.costUsd.toFixed(4)}{/if}
            </div>
          {/if}
        </article>
      {:else}
        <div class="notice" class:bad={entry.tone === "error"} role={entry.tone === "error" ? "alert" : undefined}>
          {entry.text}
        </div>
      {/if}
    {/each}

    <ChangeReview {review} />
  </div>

  <div class="composer">
    <textarea
      bind:this={promptEl}
      bind:value={prompt}
      onkeydown={onKeydown}
      oninput={fitComposer}
      placeholder="Give Anton work…   (Shift+Enter for a new line)"
      rows="2"
      spellcheck="false"
    ></textarea>
    <div class="actions">
      {#if session.busy}
        <button class="ghost" onclick={() => session.cancel()}>Cancel</button>
      {:else}
        <button
          class="ghost"
          onclick={() => session.clear()}
          disabled={session.isEmpty}
          title="Clears the conversation and the agents' memory of it"
        >
          New chat
        </button>
      {/if}
      <button class="primary" onclick={submit} disabled={session.busy || !prompt.trim()}>
        Send
      </button>
    </div>
  </div>
</section>

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
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 10px 12px;
    border-bottom: 1px solid var(--line);
  }

  .title {
    font-weight: 600;
    letter-spacing: 0.01em;
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

  .pulse {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    animation: blink 1s steps(2, end) infinite;
  }

  @keyframes blink {
    50% {
      opacity: 0.15;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .pulse {
      animation: none;
      opacity: 0.6;
    }
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

  /* Tool rows sit in the same left-hand channel as the prose, so a turn reads
     as one column of activity. */
  .tools {
    padding-left: 14px;
    border-left: 1px solid var(--line);
  }

  pre {
    margin: 0;
    padding-left: 14px;
    border-left: 1px solid var(--line);
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .error {
    margin: 6px 0 0 14px;
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
    user-select: text;
  }

  .footnote {
    padding: 5px 0 0 14px;
    color: var(--muted);
    font-size: 11px;
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
