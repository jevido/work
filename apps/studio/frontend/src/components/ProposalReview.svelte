<script lang="ts">
  /**
   * Claude's proposed restructuring, one row per operation, waiting.
   *
   * Beside the outline rather than over it. Every row names lines that are on
   * screen a few centimetres to the left, and a panel that covered them would
   * make "Move *Retry the first connect* under *Networking*" a sentence you
   * have to take on trust. Reviewing a restructuring is reading it against the
   * thing being restructured.
   *
   * Nothing in here applies until Apply is pressed. See Review, which says why
   * there is no setting that changes that.
   */
  import type { Review } from "../lib/workspace/review.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let {
    review,
    workspace,
  }: { review: Review; workspace: Workspace } = $props();

  const proposal = $derived(review.proposal);
  const rows = $derived(review.rows(workspace));
  const ready = $derived(rows.filter((r) => r.approved && r.blocked === null).length);
  const stale = $derived(rows.filter((r) => r.blocked !== null).length);

  /**
   * Whether the header's tick box is on, off, or neither.
   *
   * Only the rows that could actually go in are counted. A proposal whose
   * every applicable row is ticked reads as fully approved even when three
   * stale rows can never be -- which is true, and the alternative is a box
   * that can never be filled and a person hunting for the row they missed.
   */
  const live = $derived(rows.filter((r) => r.blocked === null));
  const allOn = $derived(live.length > 0 && live.every((r) => r.approved));
  const someOn = $derived(live.some((r) => r.approved) && !allOn);

  function apply() {
    review.apply(workspace);
  }
</script>

{#if proposal}
  <!-- A complementary landmark, not a dialog. It does not trap focus and does
       not disable the outline: editing a line while reading a proposal about
       it is a reasonable thing to do, and the rows update when you do. -->
  <section class="review" aria-labelledby="proposal-title">
    <header>
      <h2 id="proposal-title">Suggested changes</h2>
      <button class="discard" onclick={() => review.discard()}>Discard</button>
    </header>

    <p class="summary">{proposal.summary}</p>

    <!-- Said in the panel rather than assumed. The whole contract of this
         thing is that it does not touch the document until told to, and the
         person reading it has no way to know that unless it says so. -->
    <p class="posture">Nothing changes until you apply it.</p>

    <div class="all">
      <label>
        <!-- `indeterminate` is a DOM property with no attribute behind it, so
             it is set on the node rather than written in the template. The
             attachment re-runs when `someOn` changes, which is what keeps the
             dash in the box in step with the rows. -->
        <input
          type="checkbox"
          checked={allOn}
          disabled={live.length === 0}
          onchange={(e) => review.setAll(e.currentTarget.checked)}
          {@attach (node) => {
            (node as HTMLInputElement).indeterminate = someOn;
          }}
        />
        <span>Select all</span>
      </label>
      {#if stale > 0}
        <!-- Not an error and not hidden. The document moved while the proposal
             was being written, which is ordinary, and the rows it invalidated
             are still worth reading. -->
        <p class="stale">
          {stale}
          {stale === 1 ? "change no longer fits" : "changes no longer fit"} the outline.
        </p>
      {/if}
    </div>

    <ol>
      {#each rows as row (row.at)}
        <li class={{ blocked: row.blocked !== null }}>
          <label>
            <input
              type="checkbox"
              checked={row.approved}
              disabled={row.blocked !== null}
              aria-describedby={row.blocked !== null ? `why-${row.at}` : undefined}
              onchange={(e) => review.setApproved(row.at, e.currentTarget.checked)}
            />
            <span class="says">{row.says}</span>
          </label>
          {#if row.blocked !== null}
            <!-- The reason, in the row, in words. The strike-through is a
                 second cue for people who can see it and the only cue for
                 nobody: this line is what a screen reader reads out. -->
            <p class="why" id="why-{row.at}">Cannot apply — {row.blocked}.</p>
          {/if}
        </li>
      {/each}
    </ol>

    <footer>
      <button class="primary" onclick={apply} disabled={ready === 0}>
        {ready === 0 ? "Apply" : `Apply ${ready} ${ready === 1 ? "change" : "changes"}`}
      </button>
    </footer>

    <!-- What Apply did, in the panel. Not a live region of its own: Review
         announces through the one that lives in App.svelte, and two regions
         saying the same sentence is the sentence twice. -->
    <p class="outcome">
      {#if review.outcome}
        {@const o = review.outcome}
        Applied {o.applied} {o.applied === 1 ? "change" : "changes"}{o.skipped > 0
          ? `, left ${o.skipped} out`
          : ""}.
        {#if o.failed.length > 0}
          {o.failed.length}
          {o.failed.length === 1 ? "change" : "changes"} were refused by the document.
        {/if}
      {/if}
    </p>
  </section>
{/if}

<style>
  .review {
    display: grid;
    /* The list is the only row that grows; everything else keeps its height so
       a long proposal scrolls inside rather than pushing the buttons off. */
    grid-template-rows: auto auto auto auto minmax(0, 1fr) auto auto;
    gap: 8px;
    min-height: 0;
    padding: 10px;
    border-left: 1px solid var(--line);
    background: var(--panel);
    font-size: 12px;
  }

  header {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }

  h2 {
    margin: 0;
    margin-right: auto;
    font-size: 12px;
    font-weight: 600;
  }

  .summary {
    margin: 0;
    color: var(--text);
    line-height: 1.5;
  }

  .posture,
  .stale {
    margin: 0;
    color: var(--muted);
    font-size: 11px;
  }

  .all {
    display: grid;
    gap: 4px;
    padding-bottom: 6px;
    border-bottom: 1px solid var(--line);
  }

  ol {
    display: grid;
    align-content: start;
    gap: 6px;
    margin: 0;
    padding: 0;
    overflow-y: auto;
    list-style: none;
  }

  label {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: 7px;
    align-items: start;
    cursor: pointer;
  }

  input[type="checkbox"] {
    /* Nudged onto the first line of text rather than centred on a row that may
       wrap to three. */
    margin: 2px 0 0;
  }

  input[type="checkbox"]:disabled + .says {
    text-decoration: line-through;
    color: var(--muted);
  }

  input[type="checkbox"]:disabled {
    cursor: default;
  }

  li.blocked label {
    cursor: default;
  }

  .says {
    line-height: 1.45;
    overflow-wrap: anywhere;
  }

  .why {
    margin: 2px 0 0 24px;
    color: var(--muted);
    font-size: 11px;
  }

  footer {
    display: flex;
    gap: 8px;
    padding-top: 6px;
    border-top: 1px solid var(--line);
  }

  .primary {
    padding: 5px 12px;
    border: 1px solid var(--accent);
    border-radius: 5px;
    background: var(--accent);
    color: #1a1408;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }

  .primary:disabled {
    border-color: var(--line);
    background: var(--panel-2);
    color: var(--muted);
    cursor: default;
  }

  .discard {
    padding: 3px 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--text);
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  .discard:hover {
    border-color: var(--accent);
  }

  .outcome {
    margin: 0;
    min-height: 1em;
    color: var(--muted);
    font-size: 11px;
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
