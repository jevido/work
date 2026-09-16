<script lang="ts">
  import { MindmapRenderer, type Drop } from "../lib/mindmap/renderer";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let {
    workspace,
    /** The line the caret was last on, drawn as selected. */
    focused = null,
    /** False while another view is on screen: the loop stops rather than spins. */
    shown = true,
  }: { workspace: Workspace; focused?: string | null; shown?: boolean } = $props();

  let canvas = $state<HTMLCanvasElement | null>(null);
  let renderer: MindmapRenderer | null = null;
  let said = $state("");

  /*
    Everything drawn comes from the workspace the outline reads. A canvas with
    its own copy of the document would be a second model to keep in step, and it
    would drift the first time somebody edited from the other view.
  */
  function scene() {
    return {
      rows: workspace.rows,
      linksOf: (id: string) => workspace.linksOf(id),
      regionOf: (id: string) => workspace.regionOf(id),
      focused: () => focused,
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

  $effect(() => {
    if (!canvas) return;
    const made = new MindmapRenderer(canvas, scene, drop);
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
    renderer?.invalidate();
  });
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
