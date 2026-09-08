<script lang="ts">
  import type { ClaudeSession } from "../lib/claude/session.svelte";

  let { session }: { session: ClaudeSession } = $props();

  let prompt = $state("");
  let transcriptEl: HTMLPreElement | undefined = $state();
  let promptEl: HTMLTextAreaElement | undefined = $state();
  let pinned = true;

  // The console is the only text input in the window, so it takes focus on
  // launch. Typing should never require a click first.
  $effect(() => {
    promptEl?.focus();
  });

  const statusLabel: Record<string, string> = {
    idle: "Ready",
    starting: "Starting",
    streaming: "Working",
    done: "Done",
    cancelled: "Stopped",
    error: "Error",
  };

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
    const el = transcriptEl;
    if (!el) return;
    pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  }

  // Follow the stream, but stop fighting the user once they scroll up.
  $effect(() => {
    const text = session.transcript;
    const el = transcriptEl;
    if (!el || !pinned) return;
    void text;
    el.scrollTop = el.scrollHeight;
  });
</script>

<section class="console">
  <header>
    <div class="title">Claude</div>
    <div class="status" data-state={session.status}>
      {statusLabel[session.status] ?? session.status}
    </div>
  </header>

  {#if session.meta.model || session.tools.length}
    <div class="meta">
      {#if session.meta.model}<span class="chip">{session.meta.model}</span>{/if}
      {#each session.tools as tool (tool)}
        <span class="chip tool">{tool}</span>
      {/each}
    </div>
  {/if}

  <pre
    class="transcript"
    bind:this={transcriptEl}
    onscroll={onScroll}
    class:empty={!session.transcript && !session.error}>{session.transcript ||
      (session.error ? "" : "Give Anton a task.")}</pre>

  {#if session.error}
    <div class="error" role="alert">{session.error}</div>
  {/if}

  {#if session.status === "done" && session.meta.durationMs > 0}
    <div class="footnote">
      {(session.meta.durationMs / 1000).toFixed(1)}s
      {#if session.meta.costUsd > 0}· ${session.meta.costUsd.toFixed(4)}{/if}
    </div>
  {/if}

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
        <button class="ghost" onclick={() => session.reset()} disabled={!session.transcript}>
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

  .status[data-state="streaming"],
  .status[data-state="starting"] {
    color: var(--accent);
  }

  .status[data-state="done"] {
    color: var(--ok);
  }

  .status[data-state="cancelled"] {
    color: var(--muted);
  }

  .status[data-state="error"] {
    color: var(--err);
  }

  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    padding: 8px 12px 0;
  }

  .chip {
    padding: 2px 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 10.5px;
    white-space: nowrap;
  }

  .chip.tool {
    color: var(--text);
    background: var(--panel-2);
  }

  .transcript {
    flex: 1;
    margin: 0;
    padding: 12px;
    overflow-y: auto;
    overflow-x: hidden;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .transcript.empty {
    color: var(--muted);
  }

  .error {
    margin: 0 12px 10px;
    padding: 8px 10px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
    user-select: text;
  }

  .footnote {
    padding: 0 12px 10px;
    color: var(--muted);
    font-size: 11px;
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
