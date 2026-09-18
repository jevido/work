<script lang="ts">
  import CardDialog from "./CardDialog.svelte";
  import { MindmapRenderer, type Drop } from "@mindmap/renderer";
  import { tier } from "@mindmap/layout";
  import { getOutline } from "../lib/workspace/outline.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let {
    workspace,
    /** The line the caret was last on, drawn as selected. */
    focused = null,
    /** False while another view is on screen: the loop stops rather than spins. */
    shown = true,
    /**
     * Only the branches under this line, or the whole map when null.
     *
     * Isolation mode draws the same canvas as orientation mode, cut down to the region
     * a plan came out of. One component rather than two, because a second
     * canvas that drew "the same map but smaller" would be a second renderer to
     * keep the curves, the tiers and the drag behaviour in step with.
     */
    rootedAt = null,
    /**
     * A picture rather than a workspace: no line count, no editing.
     *
     * The plan's map pane is one region drawn small beside a list. They are
     * checking what the plan came out of, and chrome over a 400-pixel box is
     * more chrome than drawing.
     */
    compact = false,
    /**
     * Opens the workspace's vocabularies, when a card wants a word that is not
     * there yet. Absent in the plan's compact map, which edits nothing.
     */
    onmanage,
  }: {
    workspace: Workspace;
    focused?: string | null;
    shown?: boolean;
    rootedAt?: string | null;
    compact?: boolean;
    onmanage?: (which: "guidelines" | "parties") => void;
  } = $props();

  let canvas = $state<HTMLCanvasElement | null>(null);
  let renderer: MindmapRenderer | null = null;
  let said = $state("");

  /**
   * The map is where lines are written, and a card is where one is read.
   *
   * There used to be an indented list of inputs beside the canvas; then the
   * list went and a single input floated over whichever box had the caret. That
   * input is gone too. It was the faster thing to type into and the wrong thing
   * to read: a card is a title, a body, what it is for, who is waiting on it
   * and what it links to, and none of that fits in a box drawn two hundred
   * pixels wide with a sentence already in it.
   *
   * So clicking a box opens CardDialog, and the dialog holds the outline's own
   * keyboard on its title field -- Enter still makes a line, Tab still nests,
   * and the dialog follows the caret onto whatever line those keys land on.
   * Writing a board is still Enter, Tab, Enter; it just happens in a panel
   * beside the map instead of on top of it.
   */
  // svelte-ignore state_referenced_locally
  // Read once, at init, and that is correct: a context can only be read while
  // a component is initialising, and nothing remounts this with a different
  // `compact` -- the plan's map pane and orientation mode's are two different mounts.
  const outline = compact ? null : getOutline();

  /** The line being edited, or null. */
  let editing = $state<string | null>(null);

  /** How far in the map is, mirrored out of the renderer for the chip. */
  let scale = $state(1);

  /**
   * The rows this canvas draws.
   *
   * The whole outline, or one line and its descendants with their depths
   * rebased so the subtree draws as its own map rather than as a column of
   * boxes pushed four tiers to the right.
   */
  const rows = $derived.by(() => {
    const all = workspace.rows;
    if (!rootedAt) return all;
    const at = all.findIndex((row) => row.node.id === rootedAt);
    if (at < 0) return [];
    const base = all[at].depth;
    const within = [all[at]];
    for (let i = at + 1; i < all.length && all[i].depth > base; i++) within.push(all[i]);
    return within.map((row, i) => ({
      ...row,
      depth: row.depth - base,
      // The subtree's own root has no parent inside it, so the branch to its
      // real parent is not drawn -- there is nothing on this canvas to draw it
      // to.
      parentId: i === 0 ? "" : row.parentId,
    }));
  });

  const lines = $derived(rows.length);

  const row = $derived(editing ? (rows.find((r) => r.node.id === editing) ?? null) : null);

  /*
    Everything drawn comes from the workspace the outline reads. A canvas with
    its own copy of the document would be a second model to keep in step, and it
    would drift the first time somebody edited from the other view.
  */
  function scene() {
    return {
      rows,
      regionOf: (id: string) => workspace.regionOf(id),
      tasksOf: (id: string) => workspace.tasksOf(id),
      focused: () => focused,
    };
  }

  /**
   * A drop leaves the cards where they were let go.
   *
   * It writes places and nothing else: the tree is untouched, so a card
   * dragged across the board is still under whatever it was under, and the
   * outline -- which is the same document -- reads exactly as it did. Changing
   * what a line belongs to is Tab and Shift+Tab, where it says what it did.
   *
   * The renderer reports the whole branch, because dragging a card drags what
   * hangs off it. See Drop.
   */
  function drop(where: Drop) {
    if (!workspace.place(where.moves)) return;
    const name = workspace.text(where.moves[0].id).trim() || "an empty line";
    said =
      where.moves.length > 1
        ? `Moved ${name} and ${where.moves.length - 1} under it.`
        : `Moved ${name}.`;
  }

  /**
   * Opens the editor on a line, or closes it.
   *
   * Called by a click on the canvas, and by the outline's own focus queue when
   * a key created a line or stepped to the next one -- see OutlineKeys.wants.
   */
  function edit(id: string | null) {
    if (id === editing) return;
    editing = id;
    if (id) renderer?.reveal(id);
  }

  $effect(() => {
    if (!canvas) return;
    const made = new MindmapRenderer(canvas, scene, drop, (view) => (scale = view.scale), {
      onPick: edit,
    });
    renderer = made;
    made.start();
    made.resize();

    const observer = new ResizeObserver(() => made.resize());
    observer.observe(canvas);

    return () => {
      observer.disconnect();
      made.stop();
      renderer = null;
    };
  });

  // Hidden, the loop stops. Read outside the effect above so that changing it
  // does not tear the renderer down and build a new one -- which is the whole
  // thing this is avoiding.
  $effect(() => {
    renderer?.setShown(shown);
  });

  // The document moved. Repaint once rather than every frame: a map is still
  // most of the time, and painting an unchanged one sixty times a second is a
  // bug the office already had and fixed.
  $effect(() => {
    void workspace.revision;
    void focused;
    void rows;
    renderer?.invalidate();
  });

  /**
   * Follows the caret the outline's keys asked for.
   *
   * `focus` is called for a line that does not exist on screen yet -- one just
   * created, or the next one down. With a list of inputs that resolved itself
   * when the row rendered; with one input it has to be moved there first, and
   * this is what moves it. The register inside OutlineKeys then does the rest
   * exactly as it always did.
   */
  $effect(() => {
    const wanted = outline?.wants;
    if (wanted) edit(wanted);
  });

  /** A line deleted out from under the editor takes the editor with it. */
  $effect(() => {
    if (editing && !rows.some((r) => r.node.id === editing)) edit(null);
  });

  function reset() {
    renderer?.reset();
    said = "Back to the whole map, at 100%.";
  }
</script>

<div class="map" class:hidden={!shown}>
  <!--
    Announced, never drawn into. A drag changes the shape of the document and
    says nothing on its own, so without this the whole view is silent to
    anybody not looking at it.
  -->
  <p class="sr" role="status" aria-live="polite">{said}</p>

  <!--
    aria-hidden, and that is not a shortcut. Everything here is in the outline,
    which is a real tree of inputs, is keyboard-navigable and is what a screen
    reader gets. A canvas duplicated into the accessibility tree would be the
    same document read twice.
  -->
  <canvas bind:this={canvas} aria-hidden="true"></canvas>

  {#if row && outline}
    <!--
      The card, opened.

      This replaced a one-line input floating over the box. The input was the
      faster thing to type into and the wrong thing to read: a card is a title
      and a body, what it is for, who is waiting on it and what it links to, and
      none of that fits in a box drawn 200 pixels wide. Every outline key still
      works, in the dialog's title field -- see CardDialog.onTitleKey -- so
      writing a board is still Enter, Tab, Enter.
    -->
    <CardDialog
      {workspace}
      {row}
      {outline}
      onclose={() => edit(null)}
      onmanage={(which) => onmanage?.(which)}
    />
  {/if}

  <div class="chips">
    {#if !compact}
      <span class="chip">{lines} {lines === 1 ? "line" : "lines"}</span>
    {/if}
    <!-- A button, because it does something: the only way back from a map
         somebody has zoomed and panned away from. Reading 100% and doing
         nothing would make it the one thing on screen that looks live and is
         not. -->
    <button class="chip zoom" onclick={reset} title="Back to 100%">
      {Math.round(scale * 100)}%
    </button>
  </div>
</div>

<style>
  .map {
    position: relative;
    flex: 1;
    min-height: 0;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel);
  }

  /* visibility rather than display: both stop the paint, and this one keeps the
     element's size so coming back does not measure zero and resize to nothing. */
  .map.hidden {
    visibility: hidden;
  }

  canvas {
    display: block;
    width: 100%;
    height: 100%;
    touch-action: none;
    cursor: grab;
  }

  canvas:active {
    cursor: grabbing;
  }

  .chips {
    position: absolute;
    right: 10px;
    bottom: 10px;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .chip {
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 10.5px;
  }

  .zoom {
    font-family: ui-monospace, monospace;
    cursor: pointer;
  }

  .zoom:hover {
    color: var(--text);
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
