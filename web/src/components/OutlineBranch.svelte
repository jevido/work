<script lang="ts">
  import { countDescendants, labelOf, textOf } from "@doc/model";
  import type { TreeNode } from "@doc/ops";
  import { getView } from "../lib/view.svelte";
  import Self from "./OutlineBranch.svelte";

  let { nodes, depth = 0 }: { nodes: readonly TreeNode[]; depth?: number } = $props();

  const view = getView();

  /**
   * What the fold control is called.
   *
   * Built here rather than interpolated across three lines of the template,
   * where the indentation ends up inside the string. Screen readers collapse
   * the whitespace, but a label with a line break in the middle of it is one
   * nobody trusts when they see it in an accessibility inspector.
   */
  function foldLabel(node: TreeNode, open: boolean, hidden: number): string {
    const lines = `${hidden} ${hidden === 1 ? "line" : "lines"}`;
    return `${open ? "Hide" : "Show"} the ${lines} under ${labelOf(node)}`;
  }

  /**
   * Scrolls a line into view when it becomes the one being pointed at.
   *
   * An attachment on the marked line rather than a lookup by id from the
   * plan, because at the moment the link is clicked the line may be inside a
   * folded branch and so not rendered at all -- there is nothing to scroll to
   * until the fold opens, which is the same update that sets the mark.
   */
  function reveal(node: HTMLElement) {
    node.scrollIntoView({ block: "center", behavior: "smooth" });
  }
</script>

<!-- A real nested list. Level and sibling counts are what an outline is, and
     a screen reader gets both from the markup rather than from an aria-level
     attribute repeating what the nesting already said. -->
<ul class:nested={depth > 0}>
  {#each nodes as node (node.id)}
    {@const open = view.isOpen(node)}
    {@const hidden = countDescendants(node)}
    {@const marked = view.highlight === node.id}
    <li>
      <div class="row" class:marked {@attach marked ? reveal : undefined}>
        {#if node.children.length > 0}
          <!--
            The one control on this page, and it changes nothing but this
            browser's idea of what to draw. A fold is a field on the node, so
            writing it would change what everybody else sees -- which a
            read-only viewer must not do, however convenient.
          -->
          <button
            aria-expanded={open}
            aria-label={foldLabel(node, open, hidden)}
            onclick={() => view.toggle(node)}
          >
            <span class="chevron" class:closed={!open} aria-hidden="true"></span>
          </button>
        {:else}
          <span class="bullet" aria-hidden="true"></span>
        {/if}

        <span class="text" class:empty={textOf(node).trim() === ""}>
          {textOf(node).trim() || "(empty line)"}
        </span>

        {#if !open && hidden > 0}
          <button class="count" onclick={() => view.toggle(node)}>
            {hidden} hidden
          </button>
        {/if}
      </div>

      {#if open && node.children.length > 0}
        <Self nodes={node.children} depth={depth + 1} />
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

  /* Keyed off this component's own depth, not `ul ul`: each level is a
     separate instance and so a separate style scope. */
  ul.nested {
    margin-left: 10px;
    padding-left: 12px;
    border-left: 1px solid var(--line);
  }

  .row {
    display: flex;
    align-items: baseline;
    gap: 4px;
    padding: 3px 4px;
    border-radius: 4px;
    /* Long lines wrap rather than being cut off. This is a reading view, on
       a phone as often as not, and an outline whose text is elided is an
       outline you cannot read. */
    overflow-wrap: anywhere;
  }

  .row.marked {
    background: color-mix(in srgb, var(--accent) 18%, transparent);
    box-shadow: inset 2px 0 0 var(--accent);
  }

  button {
    display: grid;
    place-items: center;
    flex: none;
    /* A real touch target. This page opens on phones, where the desktop's
       dense 18px chevron is a coin toss. */
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: none;
    cursor: pointer;
  }

  .bullet {
    display: grid;
    place-items: center;
    flex: none;
    width: 24px;
    height: 24px;
  }

  .bullet::after {
    content: "";
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--line);
  }

  .chevron {
    width: 0;
    height: 0;
    border-left: 5px solid var(--muted);
    border-top: 4px solid transparent;
    border-bottom: 4px solid transparent;
    transform: rotate(90deg);
    transition: transform 120ms ease;
  }

  .chevron.closed {
    transform: none;
  }

  button:hover .chevron {
    border-left-color: var(--text);
  }

  .text {
    min-width: 0;
  }

  .text.empty {
    color: var(--muted);
    font-style: italic;
  }

  .count {
    flex: none;
    width: auto;
    height: auto;
    padding: 0 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    line-height: 18px;
  }

  .count:hover {
    color: var(--text);
    border-color: var(--muted);
  }

  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
</style>
