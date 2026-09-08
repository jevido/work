<script lang="ts">
  import type { ClaudeSession, Phase, RunStatus } from "../lib/claude/session.svelte";

  let { session }: { session: ClaudeSession } = $props();

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

  // Only the synthesis turn is worth labelling: a plain work block is obvious
  // from the agent's name, and planning has no block at all.
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
    // Enter sends, Shift+Enter makes a new line. This is a console, not a
    // document editor.
    if (event.key === "Enter" && !event.shiftKey) {
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

  function onScroll() {
    const el = scroller;
    if (!el) return;
    pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 32;
  }

  // Follow the stream, but stop fighting the user once they scroll up.
  $effect(() => {
    // Touch what grows so this reruns as output arrives.
    void session.blocks.map((b) => b.text.length).join();
    void session.plan;
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
    {#if session.isEmpty && !session.busy}
      <p class="hint">Give Anton work. He decides whether to bring in Jeff or Chris.</p>
    {/if}

    {#if session.status === "planning" && !session.plan}
      <div class="routing">Anton is deciding who should take this…</div>
    {/if}

    {#if session.plan}
      <div class="plan">
        <div class="plan-head">
          {session.plan.mode === "team" ? "Anton split the work" : "Anton kept this one"}
        </div>
        {#if session.plan.reason}
          <p class="plan-reason">{session.plan.reason}</p>
        {/if}
        {#each session.plan.steps as step (step.agentId)}
          <div class="plan-step">
            <span class="dot" style:background={step.colour}></span>
            <span class="who">{step.agentName}</span>
            <span class="what">{step.task}</span>
          </div>
        {/each}
      </div>
    {/if}

    {#each session.blocks as block (block.taskId)}
      <article class="block" data-status={block.status}>
        <div class="block-head">
          <span class="dot" style:background={block.colour}></span>
          <span class="who">{block.agentName}</span>
          {#if phaseLabel[block.phase]}
            <span class="phase">{phaseLabel[block.phase]}</span>
          {/if}
          {#if block.status === "streaming"}
            <span class="pulse" style:background={block.colour}></span>
          {/if}
          {#each block.tools as tool, i (`${tool}-${i}`)}
            <span class="chip">{tool}</span>
          {/each}
        </div>

        {#if block.text}
          <pre>{block.text}</pre>
        {/if}

        {#if block.error}
          <div class="error" role="alert">{block.error}</div>
        {/if}

        {#if block.status !== "streaming" && block.durationMs > 0}
          <div class="footnote">
            {(block.durationMs / 1000).toFixed(1)}s
            {#if block.costUsd > 0}· ${block.costUsd.toFixed(4)}{/if}
          </div>
        {/if}
      </article>
    {/each}

    {#if session.error}
      <div class="error run-error" role="alert">{session.error}</div>
    {/if}

    {#if session.status === "done" && session.totalCostUsd > 0}
      <div class="total">run total ${session.totalCostUsd.toFixed(4)}</div>
    {/if}
  </div>

  <div class="composer">
    <textarea
      bind:this={promptEl}
      bind:value={prompt}
      onkeydown={onKeydown}
      placeholder="Give Anton work…"
      rows="3"
      spellcheck="false"
    ></textarea>
    <div class="actions">
      {#if session.busy}
        <button class="ghost" onclick={() => session.cancel()}>Cancel</button>
      {:else}
        <button class="ghost" onclick={() => session.reset()} disabled={session.isEmpty}>
          Clear
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

  .hint,
  .routing {
    margin: 0;
    color: var(--muted);
  }

  .routing {
    padding: 2px 0 10px;
  }

  .plan {
    margin-bottom: 14px;
    padding: 10px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--panel-2);
  }

  .plan-head {
    font-weight: 600;
    font-size: 12px;
  }

  .plan-reason {
    margin: 5px 0 8px;
    color: var(--muted);
    font-size: 12px;
    user-select: text;
  }

  .plan-step {
    display: grid;
    grid-template-columns: auto auto 1fr;
    align-items: baseline;
    gap: 6px;
    padding: 3px 0;
    font-size: 12px;
    user-select: text;
  }

  .plan-step .who,
  .plan-step .dot {
    /* Align to the step's first line, not the middle of a tall task. */
    justify-self: start;
    align-self: start;
  }

  .plan-step .dot {
    margin-top: 5px;
  }

  .plan-step .what {
    color: var(--muted);
  }

  .block {
    margin-bottom: 16px;
  }

  .block-head {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 5px;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    /* Baseline-aligned inside the plan grid, centred in the flex header. */
    align-self: center;
  }

  .who {
    font-weight: 600;
    font-size: 12px;
  }

  .phase {
    color: var(--muted);
    font-size: 11px;
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

  .chip {
    padding: 1px 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 10.5px;
    white-space: nowrap;
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

  .run-error {
    margin-left: 0;
  }

  .footnote,
  .total {
    padding-top: 5px;
    padding-left: 14px;
    color: var(--muted);
    font-size: 11px;
  }

  .total {
    padding-left: 0;
  }

  .composer {
    border-top: 1px solid var(--line);
    padding: 10px 12px 12px;
  }

  textarea {
    width: 100%;
    resize: none;
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
