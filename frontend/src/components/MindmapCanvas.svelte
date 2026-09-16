<script lang="ts">
  import { MindmapRenderer, type Drop } from "../lib/mindmap/renderer";
  import { LAYOUTS, LAYOUT_LABELS, tier, type LayoutKind } from "../lib/mindmap/layout";
  import { getOutline } from "../lib/workspace/outline.svelte";
  import { labelOf } from "../lib/workspace/model";
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
     * Planning mode draws the same canvas as idea mode, cut down to the region
     * a plan came out of. One component rather than two, because a second
     * canvas that drew "the same map but smaller" would be a second renderer to
     * keep the curves, the tiers and the drag behaviour in step with.
     */
    rootedAt = null,
    /**
     * A picture rather than a workspace: no shape switch, no counts.
     *
     * The plan's map pane is one region drawn small beside a list. Its shape is
     * not a decision anybody is making there -- they are checking what the plan
     * came out of -- and three controls over a 400-pixel box is more chrome
     * than drawing.
     */
    compact = false,
  }: {
    workspace: Workspace;
    focused?: string | null;
    shown?: boolean;
    rootedAt?: string | null;
    compact?: boolean;
  } = $props();

  let canvas = $state<HTMLCanvasElement | null>(null);
  let renderer: MindmapRenderer | null = null;
  let said = $state("");

  /**
   * The map is where lines are written now.
   *
   * There used to be an indented list of inputs beside it and the canvas was a
   * second view of the same thing. The list is gone, so this is not a picture
   * any more: clicking a box opens a real input over it and every key the
   * outline understood works in it, because it is the outline's own handler --
   * see OutlineKeys, which this hands the event straight to.
   *
   * One input, moved, rather than one per line. A map of four hundred boxes
   * with four hundred textareas floating over it is four hundred elements to
   * position on every pan, and only one of them can have the caret.
   */
  // svelte-ignore state_referenced_locally
  // Read once, at init, and that is correct: a context can only be read while
  // a component is initialising, and nothing remounts this with a different
  // `compact` -- the plan's map pane and idea mode's are two different mounts.
  const outline = compact ? null : getOutline();

  /** The line being edited, or null. */
  let editing = $state<string | null>(null);

  /** Where its box is on screen, recomputed whenever the map moves under it. */
  let at = $state<{ x: number; y: number; width: number; height: number } | null>(null);

  /** True while the link picker is open on the line being edited. */
  let linking = $state(false);
  let filter = $state("");

  /**
   * Which shape the map is in.
   *
   * This side's, and deliberately not the document's. Two people looking at one
   * workspace can want different views of it at the same moment, and a shape
   * stored as a field would have one of them changing the other's screen. It is
   * also not worth an op: nothing about it survives being wrong.
   */
  let shape = $state<LayoutKind>("tidy");

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

  /**
   * Lines this one could be linked to: everything but itself, what it already
   * links to, and its own branch. A link to a child says nothing the tree does
   * not already say.
   */
  const candidates = $derived.by(() => {
    if (!editing) return [];
    const already = new Set(workspace.linksOf(editing).map((l) => l.other));
    const under = new Set<string>();
    const mine = workspace.rows.find((r) => r.node.id === editing);
    if (mine) {
      const walk = (node: { id: string; children: { id: string; children: unknown[] }[] }) => {
        under.add(node.id);
        for (const child of node.children) walk(child as never);
      };
      walk(mine.node as never);
    }
    const needle = filter.trim().toLowerCase();
    return workspace.rows
      .filter((r) => !under.has(r.node.id) && !already.has(r.node.id))
      .filter((r) => needle === "" || labelOf(r.node, 200).toLowerCase().includes(needle))
      .slice(0, 8);
  });


  /*
    Everything drawn comes from the workspace the outline reads. A canvas with
    its own copy of the document would be a second model to keep in step, and it
    would drift the first time somebody edited from the other view.
  */
  function scene() {
    return {
      rows,
      linksOf: (id: string) => workspace.linksOf(id),
      regionOf: (id: string) => workspace.regionOf(id),
      tasksOf: (id: string) => workspace.tasksOf(id),
      focused: () => focused,
      shape: () => shape,
    };
  }

  /**
   * A drop is a move-node and nothing else.
   *
   * There are no coordinates to write. The position on a node is a sort key,
   * not a place, so dragging is how somebody changes the tree rather than how
   * they arrange a plane -- and the tree is what both views draw.
   */
  function drop(where: Drop) {
    const moved =
      where.onto === "" ? workspace.moveToTop(where.node) : workspace.moveUnder(where.node, where.onto);
    if (!moved) {
      said = "That line cannot go there.";
      return;
    }
    const name = workspace.text(where.node).trim() || "an empty line";
    said =
      where.onto === ""
        ? `Moved ${name} to the top level.`
        : `Moved ${name} under ${workspace.text(where.onto).trim() || "an empty line"}.`;
  }

  /** Moves the editor to wherever its box has ended up. */
  function reposition() {
    at = editing ? (renderer?.screenOf(editing) ?? null) : null;
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
    linking = false;
    filter = "";
    if (id) renderer?.reveal(id);
    reposition();
  }

  $effect(() => {
    if (!canvas) return;
    const made = new MindmapRenderer(canvas, scene, drop, (view) => {
      scale = view.scale;
      reposition();
    }, { onPick: edit });
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
    void shape;
    void rows;
    renderer?.invalidate();
    // The document moved, so the box being edited has too -- an indent shifts
    // it a column across, a reorder a row down. Positioned after the repaint
    // for the same reason the focus queue waits for one: the layout this reads
    // is built during the paint.
    requestAnimationFrame(reposition);
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

  /**
   * Every key the outline had, on the box.
   *
   * Handed straight to OutlineKeys rather than reimplemented: this is the same
   * document, the same operations and the same things worth announcing, and a
   * second copy of that logic would be a second set of rules about what
   * Backspace does to a branch.
   */
  function onEdit(event: KeyboardEvent) {
    if (!row || !outline) return;
    if ((event.ctrlKey || event.metaKey) && !event.shiftKey && !event.altKey) {
      const key = event.key.toLowerCase();
      if (key === "l") {
        event.preventDefault();
        linking = !linking;
        return;
      }
    }
    outline.keydown(event, row);
  }

  function link(other: string) {
    if (!editing) return;
    if (workspace.link(editing, other)) {
      said = `Linked to ${workspace.text(other).trim() || "an empty line"}.`;
    }
    linking = false;
    filter = "";
  }

  function reset() {
    renderer?.reset();
    said = "Back to the top left, at 100%.";
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

  {#if row && at && outline}
    {@const style = tier(row.depth)}
    <!--
      The editor, over the box it belongs to.

      A real input, labelled, with the outline's own keydown behind it -- so
      Enter still makes a line, Tab still nests, Alt+arrows still move and fold,
      and a screen reader is told what each of them did. The canvas underneath
      stays out of the accessibility tree; this is the part that is in it.
    -->
    <div
      class="editor"
      style:left="{at.x}px"
      style:top="{at.y}px"
      style:width="{at.width}px"
      style:height="{at.height}px"
      style:border-radius="{style.radius}px"
    >
      <input
        type="text"
        value={workspace.text(row.node.id)}
        aria-label="Line at level {row.depth + 1}"
        spellcheck="false"
        autocomplete="off"
        placeholder="Write a line…"
        style:font={style.font}
        oninput={(event) => workspace.setText(row.node.id, event.currentTarget.value)}
        onkeydown={(event) => onEdit(event)}
        onfocus={() => workspace.enter(row.node.id)}
        onblur={() => workspace.leave(row.node.id)}
        {@attach (el: HTMLInputElement) => outline.register(row.node.id, el)}
      />
    </div>

    <!--
      What the line is attached to, beside it rather than on it. A box wide
      enough to hold a sentence and its links and its region and three buttons
      is a box nothing else fits next to.
    -->
    <div class="beside" style:left="{at.x + at.width + 8}px" style:top="{at.y}px">
      <button onclick={() => outline.promote(row)}>
        {workspace.taskFor(row.node.id) ? "On the plan" : "To plan"}
      </button>
      <button onclick={() => (linking = !linking)}>Link</button>
      <button class="danger" onclick={() => outline.remove(row)}>Remove</button>
    </div>

    {#if linking}
      <div class="picker" style:left="{at.x}px" style:top="{at.y + at.height + 8}px">
        <label>
          <span class="sr">Link this line to</span>
          <input
            bind:value={filter}
            placeholder="link to…"
            autocomplete="off"
            spellcheck="false"
            onkeydown={(event) => event.key === "Escape" && (linking = false)}
            {@attach (el: HTMLInputElement) => el.focus()}
          />
        </label>
        {#if candidates.length === 0}
          <p class="none">Nothing to link to.</p>
        {:else}
          <ul>
            {#each candidates as option (option.node.id)}
              <li>
                <button onclick={() => link(option.node.id)}>{labelOf(option.node, 60)}</button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}
  {/if}

  <!--
    Over the canvas rather than in it.

    Everything here is a control or a readout, and the canvas is deliberately
    out of the accessibility tree -- so drawing these into it would put the only
    way to change the shape of the map somewhere no keyboard can reach. They are
    real buttons, in the DOM, positioned over the drawing.
  -->
  {#if !compact}
    <div class="tools">
      <div class="shapes" role="group" aria-label="Map shape">
        {#each LAYOUTS as kind (kind)}
          <button
            class:on={shape === kind}
            aria-pressed={shape === kind}
            onclick={() => (shape = kind)}
          >
            {LAYOUT_LABELS[kind]}
          </button>
        {/each}
      </div>
    </div>
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

  /* Over the box, and shaped like it. The fill is the accent's, because this
     is the box the caret is in and the renderer draws that one in the accent
     too -- the editor replacing it must not look like a different thing having
     appeared on top. */
  .editor {
    position: absolute;
    display: flex;
    align-items: center;
    padding: 0 2px;
    border: 1.5px solid var(--accent);
    background: #242a36;
    box-shadow: 0 0 0 3px rgb(242 181 68 / 0.12);
  }

  .editor input {
    width: 100%;
    min-width: 0;
    padding: 0 8px;
    border: none;
    background: none;
    color: var(--text);
    outline: none;
  }

  .editor input::placeholder {
    color: var(--muted);
  }

  .beside {
    position: absolute;
    display: flex;
    gap: 3px;
    align-items: center;
    height: 24px;
  }

  .beside button {
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    white-space: nowrap;
    cursor: pointer;
  }

  .beside button:hover {
    color: var(--text);
  }

  .beside .danger:hover {
    border-color: var(--err);
    color: var(--err);
  }

  .picker {
    position: absolute;
    z-index: 2;
    width: 260px;
    padding: 8px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--panel-2);
    box-shadow: 0 8px 24px rgb(0 0 0 / 0.4);
  }

  .picker input {
    width: 100%;
    padding: 4px 6px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-size: 12px;
  }

  .picker ul {
    margin: 6px 0 0;
    padding: 0;
    list-style: none;
    max-height: 200px;
    overflow-y: auto;
  }

  .picker li button {
    display: block;
    width: 100%;
    padding: 4px 6px;
    border: none;
    border-radius: 4px;
    background: none;
    color: var(--text);
    font: inherit;
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }

  .picker li button:hover {
    background: var(--panel);
  }

  .picker .none {
    margin: 8px 0 0;
    color: var(--muted);
    font-size: 11px;
  }

  .tools {
    position: absolute;
    top: 10px;
    right: 10px;
    display: flex;
    gap: 6px;
  }

  .shapes {
    display: flex;
    gap: 1px;
    padding: 1px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--line);
  }

  .shapes button {
    padding: 3px 10px;
    border: none;
    background: var(--panel);
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .shapes button:first-child {
    border-radius: 3px 0 0 3px;
  }

  .shapes button:last-child {
    border-radius: 0 3px 3px 0;
  }

  .shapes button.on {
    background: var(--panel-2);
    color: var(--text);
    box-shadow: inset 0 -2px 0 var(--accent);
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
