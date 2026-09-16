<script lang="ts">
  import type { Review, UndoOutcome } from "../lib/workspace/review.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let {
    review,
    workspace,
  }: { review: Review; workspace: Workspace | null } = $props();

  const record = $derived(review.applied);

  /** How many of them Undo will refuse. Said before it is pressed, not after. */
  const stuck = $derived(record?.rows.filter((r) => r.inverse.kind === "none").length ?? 0);

  /** What Undo managed, once it has run. Replaces the button. */
  let outcome = $state<UndoOutcome | null>(null);

  /** Whether the list of what went in is open. */
  let showing = $state(false);

  function undo() {
    if (!workspace) return;
    outcome = review.undo(workspace);
  }

  // A new set of changes is a new bar, so whatever the last Undo said about
  // the last one stops being on screen with it.
  $effect(() => {
    void record;
    outcome = null;
    showing = false;
  });
</script>

{#if outcome}
  <!--
    What Undo managed. It stays until the next proposal arrives rather than
    fading: the rows it could not put back are named here and nowhere else,
    and a message about work that could not be recovered should not disappear
    while somebody is reading it.
  -->
  <div class="bar" role="status">
    <p class="line">
      Put back {outcome.reversed}
      {outcome.reversed === 1 ? "change" : "changes"}{#if outcome.deleted > 0}.
        <strong>{outcome.deleted} deleted {outcome.deleted === 1 ? "line is" : "lines are"} gone
        for good.</strong>{/if}
    </p>
    {#if outcome.kept.length > 0}
      <ul class="kept">
        {#each outcome.kept as row, at (at)}
          <li><span class="says">{row.says}</span> — {row.why}</li>
        {/each}
      </ul>
    {/if}
  </div>
{:else if record}
  <div class="bar" role="status">
    <p class="line">
      <span class="n">{record.rows.length}</span>
      {record.rows.length === 1 ? "operation" : "operations"} applied{#if stuck > 0},
        {stuck} of them {stuck === 1 ? "a deletion" : "deletions"}{/if}{#if record.skipped > 0}
        · {record.skipped} skipped{/if}
      <!-- A disclosure rather than the list. The bar is a fact about what just
           happened and belongs on one line; what exactly happened is worth
           having and is not worth a paragraph in front of the composer. -->
      <button class="link" aria-expanded={showing} onclick={() => (showing = !showing)}>
        {showing ? "Hide" : "Details"}
      </button>
      <button class="undo" onclick={undo} disabled={!workspace}>Undo</button>
    </p>

    {#if stuck > 0}
      <!-- Said before Undo is pressed, not after. A button that promises to
           put everything back and then apologises is worse than one that was
           honest about its limits while there was still a choice. -->
      <p class="warn">
        Undo cannot bring back a deleted line. The merge treats a deletion as final,
        whichever order it arrives in.
      </p>
    {/if}

    {#if showing}
      {#if record.summary}
        <p class="summary">{record.summary}</p>
      {/if}
      <ul class="rows">
        {#each record.rows as row, at (at)}
          <li class:stuck={row.inverse.kind === "none"}>{row.says}</li>
        {/each}
      </ul>
    {/if}
  </div>
{/if}

<style>
  /* Between the transcript and the composer.

     Not inside the scroller, where it would scroll away at exactly the moment
     somebody is reading the turn that caused it, and not in the header row,
     which is reserved for controls that have nothing to do with what was
     said. */
  .bar {
    flex: none;
    padding: 8px 12px;
    border-top: 1px solid var(--line);
    background: var(--panel-2);
    font-size: 12px;
  }

  .line {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    color: var(--muted);
    flex-wrap: wrap;
  }

  .n {
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }

  .undo {
    margin-left: auto;
    padding: 3px 12px;
    border: 1px solid var(--accent);
    border-radius: 5px;
    background: none;
    color: var(--accent);
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  .undo:hover:not(:disabled) {
    background: var(--accent);
    color: #1a1408;
  }

  .undo:disabled {
    opacity: 0.45;
    cursor: default;
  }

  .link {
    padding: 0;
    border: none;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 11px;
    text-decoration: underline;
    text-underline-offset: 2px;
    cursor: pointer;
  }

  .link:hover {
    color: var(--text);
  }

  .warn,
  .summary {
    margin: 6px 0 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .rows,
  .kept {
    margin: 6px 0 0;
    padding: 0 0 0 16px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.6;
  }

  /* A row Undo will not touch, marked in the list as well as counted in the
     line above: the count says how many and this says which. */
  .rows .stuck {
    color: var(--text);
  }

  .kept .says {
    color: var(--text);
  }

  strong {
    color: var(--text);
    font-weight: 500;
  }
</style>
