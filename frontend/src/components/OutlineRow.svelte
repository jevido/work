<script lang="ts">
  import { isCollapsed, labelOf, type Row } from "../lib/workspace/model";
  import { getOutline } from "../lib/workspace/outline.svelte";
  import Self from "./OutlineRow.svelte";

  let { rows, at = 0, depth = 0 }: { rows: Row[]; at?: number; depth?: number } = $props();

  const outline = getOutline();
  const ws = outline.workspace;

  /**
   * The run of rows at this depth, and where each one's children start.
   *
   * The flat row list is walked rather than the tree, because it is the list
   * the keyboard moves through -- and rendering from the tree while navigating
   * a flat list is two orderings to keep in step. A folded line contributes no
   * rows, so it simply has no children here.
   */
  /**
   * What the fold control is called.
   *
   * Built here rather than interpolated across four lines of the template,
   * where the indentation ends up inside the string -- screen readers
   * collapse the whitespace, but a label that reads back with a line break in
   * it in the accessibility inspector is a label nobody trusts.
   */
  function foldLabel(row: Row, folded: boolean): string {
    const n = row.descendants;
    return `${folded ? "Unfold" : "Fold"} ${labelOf(row.node)}, ${n} ${n === 1 ? "line" : "lines"}`;
  }

  const level = $derived.by(() => {
    const out: { row: Row; from: number; to: number }[] = [];
    let i = at;
    while (i < rows.length && rows[i].depth >= depth) {
      if (rows[i].depth > depth) break;
      const row = rows[i];
      const from = i + 1;
      let to = from;
      while (to < rows.length && rows[to].depth > depth) to++;
      out.push({ row, from, to });
      i = to;
    }
    return out;
  });
</script>

<!--
  A real nested list, so a screen reader says "level 3, 4 items" without being
  told. That announcement is the whole reason this is not a flat list of rows
  with a padding-left: depth is the only structure an outline has, and an
  aria-level bolted onto a flat list says the number without saying it changed.
-->
<ul class:nested={depth > 0}>
  {#each level as { row, from, to } (row.node.id)}
    {@const id = row.node.id}
    {@const folded = isCollapsed(row.node)}
    {@const task = ws.taskFor(id)}
    <li>
      <div class="row" class:active={outline.isFocused(id)}>
        {#if row.node.children.length > 0}
          <button
            class="fold"
            aria-expanded={!folded}
            aria-label={foldLabel(row, folded)}
            onclick={() => outline.toggle(row)}
          >
            <span class="chevron" class:folded aria-hidden="true"></span>
          </button>
        {:else}
          <!-- Hidden from assistive technology: it is the absence of a fold
               control, which is already conveyed by there being no control. -->
          <span class="bullet" aria-hidden="true"></span>
        {/if}

        <!--
          A plain single-line input. Enter, Tab and the arrows are taken over
          below, and everything else -- selection, the caret, an input method,
          the platform's own text shortcuts -- is the browser's, which is a
          better text editor than this file is going to be.
        -->
        <input
          type="text"
          value={ws.text(id)}
          aria-label="Outline line"
          spellcheck="false"
          autocomplete="off"
          placeholder={row.depth === 0 && rows.length === 1 ? "What are you thinking about?" : ""}
          oninput={(event) => ws.setText(id, event.currentTarget.value)}
          onkeydown={(event) => outline.keydown(event, row)}
          onfocus={() => outline.focused(id)}
          onblur={() => outline.isFocused(id) && outline.focused(null)}
          {@attach (el) => outline.register(id, el)}
        />

        {#if folded && row.descendants > 0}
          <!-- What folding hid. A chevron alone makes a branch of forty lines
               look like a line with nothing under it. -->
          <button class="hidden-count" onclick={() => outline.toggle(row)}>
            {row.descendants}
          </button>
        {/if}

        <!--
          The row's own actions. Always in the DOM and only visible on hover or
          focus-within: a button that appears on hover alone is a button a
          keyboard cannot find, and one that is display:none until then is a
          button a screen reader is told does not exist.
        -->
        <div class="actions">
          {#if task}
            <span class="on-plan" title="This line is on the plan">on the plan</span>
          {:else}
            <button
              class="ghost"
              onclick={() => outline.promote(row)}
              title="Add this line to the plan (Ctrl+Enter)"
            >
              To plan
            </button>
          {/if}
          <button
            class="ghost danger"
            onclick={() => outline.remove(row)}
            title="Delete this line and everything under it"
          >
            Remove
          </button>
        </div>
      </div>

      {#if to > from}
        <Self {rows} at={from} depth={depth + 1} />
      {/if}
    </li>
  {/each}
</ul>

<style>
  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /*
   * Every level steps in by one indent, and the guide line makes the step
   * readable at a glance rather than countable.
   *
   * Keyed off the component's own depth rather than written as `ul ul`,
   * because each level is a separate instance of this component and so a
   * separate style scope -- a descendant selector here would match nothing.
   */
  ul.nested {
    margin-left: 9px;
    padding-left: 10px;
    border-left: 1px solid var(--line);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 2px;
    /* Tall enough for a 24px control with a pixel either side. Dense, and
       this is the floor that keeps it usable with a pointer -- see .fold. */
    min-height: 28px;
    border-radius: 4px;
  }

  .row.active {
    background: var(--panel-2);
  }

  .fold,
  .bullet {
    display: grid;
    place-items: center;
    flex: none;
    /* 24x24, which is WCAG 2.2's floor for a pointer target, even though the
       chevron drawn inside is a few pixels across. The alternative -- 18px
       with the spacing exception argued for it -- does not survive the rows
       above and below being the same target 26px away, and it is a fiddly
       thing to hit with a trackpad regardless. */
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    background: none;
  }

  .fold {
    cursor: pointer;
  }

  .chevron {
    width: 0;
    height: 0;
    border-left: 4px solid var(--muted);
    border-top: 3.5px solid transparent;
    border-bottom: 3.5px solid transparent;
    transform: rotate(90deg);
    transition: transform 120ms ease;
  }

  @media (prefers-reduced-motion: reduce) {
    .chevron {
      transition: none;
    }
  }

  .chevron.folded {
    transform: none;
  }

  .fold:hover .chevron {
    border-left-color: var(--text);
  }

  .bullet::after {
    content: "";
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--line);
  }

  input {
    flex: 1;
    min-width: 0;
    padding: 2px 4px;
    border: 1px solid transparent;
    border-radius: 3px;
    background: none;
    color: inherit;
    font: inherit;
  }

  input::placeholder {
    color: var(--muted);
  }

  /*
   * No border until it is focused. A column of forty boxed inputs reads as a
   * form; an outline should read as text you can put a caret in.
   *
   * The ring is a border plus a shadow of the same colour rather than an
   * `outline`, so it is two pixels of 10:1 contrast without the one-pixel
   * jump a thicker border would cause -- and without a hard ring standing off
   * the row, which at forty lines is a lot of gold.
   */
  input:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
    background: var(--bg);
  }

  .hidden-count {
    flex: none;
    min-width: 24px;
    height: 24px;
    padding: 0 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    cursor: pointer;
  }

  /*
   * Present, focusable and announced; simply not drawn until this row is being
   * used.
   *
   * `opacity` and not `visibility` or `display`, which is the whole point of
   * the rule: both of those take a control out of the accessibility tree and
   * out of the tab order, so the buttons would exist for a mouse and not exist
   * for anybody else. Opacity leaves them reachable and keeps the row from
   * changing width when they appear and shoving the text under the caret.
   */
  .actions {
    display: flex;
    flex: none;
    gap: 4px;
    opacity: 0;
    transition: opacity 100ms ease;
  }

  .row:hover .actions,
  .row:focus-within .actions {
    opacity: 1;
  }

  @media (prefers-reduced-motion: reduce) {
    .actions {
      transition: none;
    }
  }

  .ghost {
    height: 24px;
    padding: 0 8px;
    border: 1px solid var(--line);
    border-radius: 3px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .ghost:hover {
    border-color: #3a4250;
    color: var(--text);
  }

  .ghost.danger:hover {
    border-color: var(--err);
    color: var(--err);
  }

  .on-plan {
    align-self: center;
    color: var(--ok);
    font-size: 11px;
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
</style>
