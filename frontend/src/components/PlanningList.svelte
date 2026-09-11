<script lang="ts">
  import { tick } from "svelte";
  import {
    TASK_STATES,
    TASK_STATE_LABELS,
    labelOf,
    sourceIdOf,
    statusOf,
    type TaskState,
  } from "../lib/workspace/model";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let { workspace }: { workspace: Workspace } = $props();

  /** What was just done, for the live region. */
  let said = $state("");

  /**
   * The inputs, by task id, so a reorder can keep the caret on the task that
   * moved. Same problem the outline has: the row is a new element afterwards,
   * so the thing to remember is an id.
   */
  const inputs = new Map<string, HTMLInputElement>();
  let want: string | null = null;

  function register(id: string, el: HTMLInputElement) {
    inputs.set(id, el);
    if (want === id) apply();
    return () => {
      if (inputs.get(id) === el) inputs.delete(id);
    };
  }

  /**
   * Asks for the caret to land in a task.
   *
   * After Svelte has updated the DOM, not now: every caller has just
   * reordered the list, and the input for that task is about to be moved or
   * replaced. Focusing the element still on screen means focusing the one
   * about to come off it -- WebKit blurs an element when it is re-inserted,
   * so the caret ends up on the document body. Same reasoning, and the same
   * fix, as OutlineKeys.focus.
   */
  function focus(id: string) {
    want = id;
    void tick().then(apply);
  }

  function apply() {
    if (!want) return;
    const el = inputs.get(want);
    if (!el) return;
    want = null;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }

  /** The live region only fires on a change, and moving twice says the same thing. */
  function say(text: string) {
    said = said === text ? `${text} ` : text;
  }

  function move(id: string, delta: -1 | 1, at: number) {
    if (!workspace.moveTaskBy(id, delta)) {
      say(delta === -1 ? "Already first." : "Already last.");
      return;
    }
    focus(id);
    say(`Moved to position ${at + 1 + delta} of ${workspace.tasks.length}.`);
  }

  function add() {
    const id = workspace.addTask();
    focus(id);
  }

  function remove(id: string, title: string) {
    const tasks = workspace.tasks;
    const at = tasks.findIndex((t) => t.id === id);
    const back = tasks[at + 1]?.id ?? tasks[at - 1]?.id ?? null;
    workspace.removeTask(id);
    say(`Removed ${title}.`);
    if (back) focus(back);
  }

  function onKeydown(event: KeyboardEvent, id: string, at: number) {
    if (event.isComposing) return;

    // The same keys the outline uses, for the same reason: this is the other
    // ordered list in the app, and two lists with different keys for "move
    // this down" is two things to remember for one idea.
    if (event.altKey && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
      event.preventDefault();
      move(id, event.key === "ArrowDown" ? 1 : -1, at);
      return;
    }
    if (event.key === "ArrowUp" || event.key === "ArrowDown") {
      event.preventDefault();
      const next = workspace.tasks[at + (event.key === "ArrowDown" ? 1 : -1)];
      if (next) focus(next.id);
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      add();
      return;
    }
    if (event.key === "Backspace") {
      const input = event.currentTarget as HTMLInputElement;
      if (input.value !== "" || input.selectionStart !== 0) return;
      event.preventDefault();
      remove(id, "the empty task");
    }
  }

  function setState(id: string, value: string) {
    const state = TASK_STATES.find((s) => s === value) ?? "todo";
    workspace.setTaskState(id, state as TaskState);
  }
</script>

<section class="plan" aria-label="Plan">
  <header>
    <h2>Plan</h2>
    <p class="hint">
      In order. <kbd>Alt</kbd>+<kbd>↑↓</kbd> to reorder, <kbd>Enter</kbd> for a new task.
    </p>
    <button class="ghost" onclick={add}>Add task</button>
  </header>

  <p class="announce" role="status" aria-live="polite">{said}</p>

  <div class="scroller">
    {#if workspace.tasks.length === 0}
      <div class="empty">
        <p>Nothing on the plan yet.</p>
        <p class="quiet">
          Most tasks come from the outline: put the caret on a line in Idea and press
          <kbd>Ctrl</kbd>+<kbd>Enter</kbd>. The task keeps a link back to the line it came
          from, so later you can read why it is on here.
        </p>
        <button class="primary" onclick={add}>Add one by hand</button>
      </div>
    {:else}
      <!-- An ordered list, because the order is the content. A screen reader
           saying "3 of 9" on every task is the plan's main fact. -->
      <ol>
        {#each workspace.tasks as task, at (task.id)}
          {@const status = statusOf(task)}
          {@const source = workspace.sourceOf(task)}
          {@const deletedSource = source === null && sourceIdOf(task) !== null}
          <li data-status={status}>
            <div class="head">
              <label class="status">
                <span class="sr">Status of {labelOf(task, 40)}</span>
                <!-- A native select. A custom three-state control here would
                     be reimplementing a listbox badly: this one already knows
                     about the keyboard, the touch picker and the platform's
                     own conventions. -->
                <select
                  value={status}
                  onchange={(event) => setState(task.id, event.currentTarget.value)}
                >
                  {#each TASK_STATES as option (option)}
                    <option value={option}>{TASK_STATE_LABELS[option]}</option>
                  {/each}
                </select>
              </label>

              <input
                type="text"
                value={workspace.text(task.id)}
                aria-label="Task {at + 1} of {workspace.tasks.length}"
                spellcheck="false"
                autocomplete="off"
                placeholder="What needs doing?"
                oninput={(event) => workspace.setTaskTitle(task.id, event.currentTarget.value)}
                onkeydown={(event) => onKeydown(event, task.id, at)}
                {@attach (el) => register(task.id, el)}
              />

              <div class="actions">
                <button
                  class="icon"
                  aria-label="Move {labelOf(task, 40)} up"
                  disabled={at === 0}
                  onclick={() => move(task.id, -1, at)}
                >
                  ↑
                </button>
                <button
                  class="icon"
                  aria-label="Move {labelOf(task, 40)} down"
                  disabled={at === workspace.tasks.length - 1}
                  onclick={() => move(task.id, 1, at)}
                >
                  ↓
                </button>
                <button
                  class="icon danger"
                  aria-label="Remove {labelOf(task, 40)}"
                  onclick={() => remove(task.id, labelOf(task, 40))}
                >
                  ×
                </button>
              </div>
            </div>

            <!--
              Where the task came from. The point of the whole extraction: six
              weeks later "why is this on here" is answered by the line it came
              out of, not by the eight words that fitted on the task.
            -->
            {#if source}
              <button class="source" onclick={() => workspace.requestReveal(source.id)}>
                <span class="from">from</span>
                <span class="text">{workspace.text(source.id).trim() || "an empty line"}</span>
              </button>
            {:else if deletedSource}
              <!-- Not a link, because there is nothing to go to. Said rather
                   than hidden: a task whose reason was deleted is a task worth
                   looking at twice. -->
              <p class="source gone">from a line that has since been deleted</p>
            {/if}
          </li>
        {/each}
      </ol>
    {/if}
  </div>
</section>

<style>
  .plan {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    background: var(--bg);
  }

  header {
    display: flex;
    align-items: baseline;
    gap: 10px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--line);
  }

  h2 {
    margin: 0;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.02em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .hint {
    margin: 0;
    margin-right: auto;
    color: var(--muted);
    font-size: 11px;
  }

  kbd {
    padding: 0 3px;
    border: 1px solid var(--line);
    border-radius: 3px;
    background: var(--panel-2);
    font-family: inherit;
    font-size: 10px;
  }

  .scroller {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 8px 12px 40px;
  }

  ol {
    margin: 0;
    padding: 0;
    list-style: none;
    counter-reset: task;
  }

  li {
    counter-increment: task;
    padding: 5px 0 5px 26px;
    border-bottom: 1px solid var(--line);
    position: relative;
  }

  /* The number is drawn rather than read out: the input's own label already
     says "Task 3 of 9", and a list marker would say the number twice. */
  li::before {
    content: counter(task);
    position: absolute;
    left: 0;
    top: 10px;
    width: 20px;
    text-align: right;
    color: var(--muted);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }

  li[data-status="done"] .head input {
    color: var(--muted);
    text-decoration: line-through;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  select {
    flex: none;
    padding: 2px 4px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--muted);
    font: inherit;
    font-size: 11px;
  }

  /* Status carries a colour as well as its own words, and the words come
     first: the select says "In progress" whether or not anyone can see that
     it is also amber. */
  li[data-status="doing"] select {
    border-color: var(--accent);
    color: var(--accent);
  }

  li[data-status="done"] select {
    border-color: #2f5548;
    color: var(--ok);
  }

  input {
    flex: 1;
    min-width: 0;
    padding: 3px 5px;
    border: 1px solid transparent;
    border-radius: 3px;
    background: none;
    color: inherit;
    font: inherit;
  }

  input::placeholder {
    color: var(--muted);
  }

  /* Two pixels of ring without a layout jump. Same reasoning as the
     outline's rows. */
  input:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
    background: var(--panel);
  }

  .actions {
    display: flex;
    flex: none;
    gap: 2px;
    /* Opacity, not visibility: these have to stay in the tab order and in the
       accessibility tree whether or not the pointer is over the row. */
    opacity: 0;
    transition: opacity 100ms ease;
  }

  li:hover .actions,
  li:focus-within .actions {
    opacity: 1;
  }

  @media (prefers-reduced-motion: reduce) {
    .actions {
      transition: none;
    }
  }

  .icon {
    width: 22px;
    height: 22px;
    padding: 0;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--muted);
    line-height: 1;
    cursor: pointer;
  }

  .icon:hover:not(:disabled) {
    border-color: #3a4250;
    color: var(--text);
  }

  .icon.danger:hover:not(:disabled) {
    border-color: var(--err);
    color: var(--err);
  }

  .icon:disabled {
    opacity: 0.35;
    cursor: default;
  }

  .source {
    display: flex;
    align-items: baseline;
    gap: 5px;
    max-width: 100%;
    margin: 2px 0 0 32px;
    padding: 1px 4px;
    border: none;
    border-radius: 3px;
    background: none;
    color: var(--muted);
    font-size: 11px;
    text-align: left;
    cursor: pointer;
  }

  button.source:hover .text {
    color: var(--text);
    text-decoration-color: var(--text);
  }

  .source .from {
    flex: none;
    opacity: 0.7;
  }

  .source .text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* Underlined, because it goes somewhere. Colour alone would be the only
       cue that this line is a link and the one above it is not. */
    text-decoration: underline dotted;
    text-underline-offset: 2px;
  }

  .source.gone {
    cursor: default;
    font-style: italic;
  }

  .empty {
    display: grid;
    justify-items: start;
    gap: 10px;
    max-width: 46ch;
    padding: 24px 0;
  }

  .empty p {
    margin: 0;
    color: var(--muted);
  }

  .empty .quiet {
    font-size: 12px;
    line-height: 1.6;
  }

  .announce,
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  .ghost {
    flex: none;
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .ghost:hover {
    border-color: #3a4250;
    color: var(--text);
  }

  .primary {
    padding: 5px 12px;
    border: 1px solid var(--accent);
    border-radius: 4px;
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
    cursor: pointer;
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
