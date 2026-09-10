<script lang="ts">
  import type { ToolCall } from "../lib/diff/tools";
  import DiffView from "./DiffView.svelte";

  let { call }: { call: ToolCall } = $props();

  // Collapsed by default: a turn can make a dozen calls, and the point of the
  // row is that the interesting ones are recognisable without opening any.
  let open = $state(false);
</script>

<div class="tool" class:failed={call.failed}>
  <button class="head" onclick={() => (open = !open)} aria-expanded={open}>
    <span class="chevron" class:open>▸</span>
    <span class="name">{call.name}</span>
    {#if call.summary}<span class="summary">{call.summary}</span>{/if}
    {#if call.diff}
      <span class="stat">
        <span class="added">+{call.added}</span><span class="removed">−{call.removed}</span>
      </span>
    {/if}
    {#if !call.done}
      <span class="pending">running</span>
    {:else if call.failed}
      <span class="badge">refused</span>
    {/if}
  </button>

  {#if open}
    <!-- `data-expand` marks this as something the reader opened rather than
         something the run produced. The transcript follows a stream to the
         bottom as it arrives; it must not do that when a row is expanded, or
         opening a long result scrolls the top of it -- the part you opened it
         for -- straight back out of view. See lib/ui/follow.ts, which is the
         only thing that reads this. -->
    <div class="body" data-expand>
      {#if call.diff}
        <DiffView lines={call.diff} path={call.path} />
      {:else}
        <pre class="args">{call.input}</pre>
      {/if}

      {#if call.done && call.result}
        <div class="label">Result</div>
        <pre class="result">{call.result}</pre>
      {/if}
    </div>
  {/if}
</div>

<style>
  .tool {
    margin: 4px 0;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel-2);
    overflow: hidden;
  }

  .tool.failed {
    border-color: #4a2b2b;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 7px;
    width: 100%;
    padding: 5px 8px;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    font-size: 11.5px;
  }

  .head:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  .chevron {
    color: var(--muted);
    font-size: 9px;
    transition: transform 120ms ease;
  }

  .chevron.open {
    transform: rotate(90deg);
  }

  @media (prefers-reduced-motion: reduce) {
    .chevron {
      transition: none;
    }
  }

  .name {
    font-weight: 600;
    flex: none;
  }

  /* The path or command, which is what makes a row recognisable at a glance. */
  .summary {
    color: var(--muted);
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .stat {
    margin-left: auto;
    flex: none;
    display: flex;
    gap: 5px;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10.5px;
  }

  .added {
    color: var(--ok);
  }

  .removed {
    color: var(--err);
  }

  .pending,
  .badge {
    margin-left: auto;
    flex: none;
    color: var(--muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .badge {
    color: var(--err);
  }

  .body {
    border-top: 1px solid var(--line);
    padding: 7px 8px;
  }

  .label {
    margin: 7px 0 3px;
    color: var(--muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  pre {
    margin: 0;
    max-height: 260px;
    overflow: auto;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 11px;
    line-height: 1.45;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .args,
  .result {
    color: var(--muted);
  }
</style>
