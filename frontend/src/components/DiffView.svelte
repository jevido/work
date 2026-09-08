<script lang="ts">
  import type { DiffHunkLine } from "../lib/diff/diff";

  let { lines, path = "" }: { lines: DiffHunkLine[]; path?: string } = $props();
</script>

<div class="diff">
  {#if path}<div class="path">{path}</div>{/if}
  <div class="lines">
    {#each lines as line, i (i)}
      {#if line.skipped}
        <div class="gap">⋯ {line.skipped} unchanged</div>
      {:else}
        <div class="line" data-kind={line.kind}>
          <span class="gutter">{line.oldLine ?? ""}</span>
          <span class="gutter">{line.newLine ?? ""}</span>
          <span class="sign">{line.kind === "add" ? "+" : line.kind === "remove" ? "−" : " "}</span
          ><span class="text">{line.text}</span>
        </div>
      {/if}
    {/each}
  </div>
</div>

<style>
  .diff {
    border: 1px solid var(--line);
    border-radius: 5px;
    overflow: hidden;
  }

  .path {
    padding: 4px 8px;
    border-bottom: 1px solid var(--line);
    background: rgba(255, 255, 255, 0.02);
    color: var(--muted);
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .lines {
    max-height: 320px;
    overflow: auto;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 11px;
    line-height: 1.5;
    user-select: text;
  }

  .line {
    display: flex;
    white-space: pre;
  }

  /* Colour carries the meaning, but the +/- sign carries it too, so the diff
     is still readable without relying on colour. */
  .line[data-kind="add"] {
    background: rgba(91, 200, 160, 0.1);
  }

  .line[data-kind="remove"] {
    background: rgba(239, 111, 108, 0.1);
  }

  .gutter {
    flex: none;
    width: 2.6em;
    padding-right: 6px;
    text-align: right;
    color: #4d5563;
    user-select: none;
  }

  .sign {
    flex: none;
    width: 1.2em;
    text-align: center;
    color: var(--muted);
  }

  .line[data-kind="add"] .sign {
    color: var(--ok);
  }

  .line[data-kind="remove"] .sign {
    color: var(--err);
  }

  .text {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }

  .gap {
    padding: 2px 8px;
    color: #4d5563;
    background: rgba(255, 255, 255, 0.015);
  }
</style>
