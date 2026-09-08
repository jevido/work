<script lang="ts">
  import type { ChangeReview } from "../lib/changes/changes.svelte";
  import DiffView from "./DiffView.svelte";

  let { review }: { review: ChangeReview } = $props();

  let openPath = $state<string | null>(null);

  function toggle(path: string) {
    openPath = openPath === path ? null : path;
  }
</script>

{#if !review.isEmpty}
  <section class="review" aria-label="File changes">
    <header>
      <span class="title">Changes on disk</span>
      <span class="count">{review.files.length}</span>
    </header>

    {#each review.files as file (file.path)}
      <div class="file">
        <div class="row">
          <button class="open" onclick={() => toggle(file.path)} aria-expanded={openPath === file.path}>
            <span class="chevron" class:open={openPath === file.path}>▸</span>
            <span class="status" data-status={file.status}>{file.status}</span>
            <span class="path">{file.path}</span>
            {#if !file.binary}
              <span class="stat">
                <span class="added">+{file.added}</span><span class="removed">−{file.removed}</span>
              </span>
            {/if}
          </button>
          <button
            class="revert"
            onclick={() => review.revert(file.path)}
            disabled={file.reverting || !file.restorable}
            title={file.restorable
              ? "Restore this file to what it was before the run"
              : "Work did not record enough to restore this file exactly"}
          >
            {file.reverting ? "Reverting…" : "Revert"}
          </button>
        </div>

        {#if file.error}
          <div class="error" role="alert">{file.error}</div>
        {/if}

        {#if openPath === file.path}
          {#if file.binary}
            <p class="binary">Binary file — no diff to show.</p>
          {:else if file.diff.length === 0}
            <p class="binary">No textual diff available.</p>
          {:else}
            <div class="diff"><DiffView lines={file.diff} /></div>
          {/if}
        {/if}
      </div>
    {/each}
  </section>
{/if}

<style>
  .review {
    margin-bottom: 16px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--panel-2);
    overflow: hidden;
  }

  header {
    display: flex;
    align-items: baseline;
    gap: 7px;
    padding: 7px 9px;
    border-bottom: 1px solid var(--line);
  }

  .title {
    font-weight: 600;
    font-size: 12px;
  }

  .count {
    color: var(--muted);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }

  .file + .file {
    border-top: 1px solid var(--line);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding-right: 7px;
  }

  .open {
    display: flex;
    align-items: center;
    gap: 7px;
    flex: 1;
    min-width: 0;
    padding: 6px 9px;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    font-size: 11.5px;
  }

  .open:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  .chevron {
    flex: none;
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

  /* The word, not just a colour, so the state survives a screenshot and a
     colour-blind reader. */
  .status {
    flex: none;
    width: 4.6em;
    color: var(--muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .status[data-status="added"] {
    color: var(--ok);
  }

  .status[data-status="deleted"] {
    color: var(--err);
  }

  .path {
    flex: 1;
    min-width: 0;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    direction: rtl;
    text-align: left;
  }

  .stat {
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

  .revert {
    flex: none;
    padding: 3px 9px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel);
    cursor: pointer;
    font-size: 11px;
  }

  .revert:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .revert:hover:not(:disabled) {
    border-color: var(--err);
    color: var(--err);
  }

  .diff {
    padding: 0 9px 9px;
  }

  .binary {
    margin: 0;
    padding: 0 9px 9px;
    color: var(--muted);
    font-size: 11.5px;
  }

  .error {
    margin: 0 9px 8px;
    padding: 6px 8px;
    border: 1px solid #4a2b2b;
    border-radius: 5px;
    background: #241a1a;
    color: var(--err);
    font-size: 11.5px;
    user-select: text;
  }
</style>
