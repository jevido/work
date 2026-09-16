<script lang="ts">
  import type { Mode } from "../lib/workspace/model";

  let { mode }: { mode: Mode } = $props();

  /**
   * One strip, along the bottom, saying what the keyboard does here.
   *
   * It used to be a line inside the outline's own header, which meant planning
   * mode had none at all and the map -- where the only way to move anything is
   * a gesture nobody is told about -- had none either. A strip outside all
   * three panes can say the right thing for whichever one is in front, and is
   * in the same place every time somebody looks for it.
   *
   * Everything below is a key that exists. A hint bar is read once, believed,
   * and then relied on; one that lists a shortcut the app does not have costs
   * more than no hint bar, because it sends somebody looking for a bug in their
   * keyboard.
   */
  interface Hint {
    keys: string[];
    /** Joined with "+" when the keys are pressed together, " " when in turn. */
    together?: boolean;
    what: string;
  }

  /**
   * The map is where lines are written, so its strip is both halves at once:
   * the keys that work in the box the caret is in, and the two gestures that
   * move the map itself. They are not alternatives any more -- there is no
   * other view to be in.
   */
  const IDEA: Hint[] = [
    { keys: ["Click"], what: "a box to edit it" },
    { keys: ["Enter"], what: "new line" },
    { keys: ["Tab"], what: "nest" },
    { keys: ["Alt", "↑↓"], together: true, what: "move" },
    { keys: ["Alt", "←→"], together: true, what: "fold" },
    { keys: ["Ctrl", "L"], together: true, what: "link" },
    { keys: ["Ctrl", "G"], together: true, what: "group" },
    { keys: ["Ctrl", "Enter"], together: true, what: "to plan" },
    { keys: ["Drag"], what: "a box to reparent it" },
    { keys: ["Scroll"], what: "to zoom" },
  ];


  const PLANNING: Hint[] = [
    { keys: ["Enter"], what: "new task" },
    { keys: ["Alt", "↑↓"], together: true, what: "reorder" },
    { keys: ["Esc"], what: "leave" },
  ];

  const hints = $derived<Hint[]>(mode === "planning" ? PLANNING : IDEA);
</script>

<!--
  aria-hidden, and not by accident.

  Every control named here is reachable and labelled where it lives: the keys
  are on the elements that handle them, the buttons say what they do, and a
  screen reader announces the outline as a tree. Reading this strip out as well
  would be the same information a second time, in the least useful order, every
  time somebody moved between modes.
-->
<div class="hints" aria-hidden="true">
  {#each hints as hint, at (at)}
    <span class="hint">
      <span class="keys">
        {#each hint.keys as key, i (i)}
          {#if i > 0}<span class="join">{hint.together ? "+" : " "}</span>{/if}<kbd>{key}</kbd>
        {/each}
      </span>
      {hint.what}
    </span>
  {/each}
  <span class="hint modes">
    <kbd>Ctrl</kbd><span class="join">+</span><kbd>1</kbd> <kbd>2</kbd> <kbd>3</kbd>
    switch mode
  </span>
</div>

<style>
  .hints {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 18px;
    flex: none;
    padding: 7px 12px;
    border-top: 1px solid var(--line);
    background: var(--panel);
    color: var(--muted);
    font-size: 11px;
  }

  .hint {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    white-space: nowrap;
  }

  /* Pushed to the far end because it is the only one that is true in every
     mode: the rest of the strip changes under it, and this does not. */
  .modes {
    margin-left: auto;
  }

  .keys {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }

  .join {
    color: var(--muted);
  }

  kbd {
    padding: 0 4px;
    border: 1px solid var(--line);
    border-radius: 3px;
    background: var(--panel-2);
    color: #c8cedb;
    font-family: ui-monospace, monospace;
    font-size: 10px;
    line-height: 16px;
  }
</style>
