<script lang="ts">
  import { MODES, MODE_HINTS, MODE_LABELS, type Mode } from "../lib/workspace/model";

  let {
    mode = $bindable(),
    /** Namespaced per workspace, so two tabs' radios are not one group. */
    group,
  }: { mode: Mode; group: string } = $props();
</script>

<!--
  Real radio buttons, drawn as a segmented control.

  A row of <button>s with aria-pressed would look the same and behave worse: a
  radio group is one tab stop with the arrow keys moving between the options,
  and it is announced as "Idea, radio button, 1 of 3" — which says both what
  this is and how many other things it could be. Neither is worth
  reimplementing, and every reimplementation of it gets the arrow keys wrong.

  `checked` and `onchange` rather than `bind:group`, which is the obvious way
  to write this and is wrong here. The group's name is per workspace, so
  switching tabs changes it on live inputs; bind:group then holds a value that
  belongs to no radio in the new group, and the control renders with nothing
  selected at all. Worse, the write-back lands on whichever workspace is in
  front by then -- so switching tabs quietly copied one tab's mode onto
  another's. One-way rendering with an explicit handler has neither problem.
-->
<fieldset class="modes">
  <legend class="sr">Mode</legend>
  {#each MODES as option (option)}
    <label class:on={mode === option} title={MODE_HINTS[option]}>
      <input
        type="radio"
        name="mode-{group}"
        value={option}
        checked={mode === option}
        onchange={() => (mode = option)}
      />
      <span>{MODE_LABELS[option]}</span>
    </label>
  {/each}
</fieldset>

<style>
  .modes {
    display: flex;
    gap: 1px;
    margin: 0;
    padding: 1px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--line);
  }

  label {
    padding: 3px 10px;
    background: var(--panel);
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
    cursor: pointer;
  }

  label:first-of-type {
    border-radius: 3px 0 0 3px;
  }

  label:last-of-type {
    border-radius: 0 3px 3px 0;
  }

  label:hover {
    color: var(--text);
  }

  .modes label.on {
    background: var(--panel-2);
    color: var(--text);
    /* Not colour alone: the selected segment is also the only one with a bar
       under it, so the state survives a greyscale screenshot and a display
       that renders the two panel shades the same. */
    box-shadow: inset 0 -2px 0 var(--accent);
  }

  /*
   * The input is hidden from sight and not from anything else: still focusable,
   * still announced, still where the focus ring belongs. `display: none` or
   * `visibility: hidden` here would delete the control and leave a label that
   * only a mouse can use.
   */
  input {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
  }

  /* The ring goes on the label, since that is the part anybody can see. */
  label:has(input:focus-visible) {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
