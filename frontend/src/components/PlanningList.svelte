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
  import MindmapCanvas from "./MindmapCanvas.svelte";

  let { workspace }: { workspace: Workspace } = $props();

  /** What was just done, for the live region. */
  let said = $state("");

  /**
   * The region the plan is being read through, or null for the whole plan.
   *
   * A plan is one flat ordered list, and past about fifteen items the question
   * stops being "what is next" and becomes "what is next *about this*". A
   * region already answers that: it is a cluster somebody drew around a set of
   * lines and said was one piece of work, and the tasks extracted from those
   * lines are that piece's plan.
   *
   * A filter and not a separate document. Position is global -- number 3 is
   * number 3 of the plan whether or not the other six are on screen -- so what
   * this hides is rows, never order.
   */
  let through = $state<string | null>(null);

  /** The regions that still exist, so a filter cannot outlive the one it names. */
  const regions = $derived(workspace.regions);

  $effect(() => {
    if (through !== null && !regions.some((region) => region.id === through)) through = null;
  });

  /** The line the map pane draws from: the branch the chosen region hangs off. */
  const mapRoot = $derived(through ? workspace.regionRootOf(through) : null);

  /**
   * The tasks on screen, which is all of them or the ones from one region.
   *
   * Their number is the position in the whole plan, not in the filtered view.
   * Renumbering under a filter would make "take number 3 next" mean two
   * different tasks depending on what somebody had clicked.
   */
  const shown = $derived.by(() => {
    const all = workspace.tasks.map((task, at) => ({ task, at }));
    if (!through) return all;
    const members = new Set(workspace.regionMembers(through));
    return all.filter(({ task }) => {
      const source = sourceIdOf(task);
      return source !== null && members.has(source);
    });
  });

  const done = $derived(workspace.tasks.filter((task) => statusOf(task) === "done").length);

  /** The first task not finished, which is what work mode picks up. */
  const next = $derived(workspace.tasks.findIndex((task) => statusOf(task) !== "done"));

  function extract() {
    if (!through) return;
    const made = workspace.extractRegion(through);
    say(
      made === 0
        ? "Every line in that region is already on the plan."
        : `Added ${made} ${made === 1 ? "task" : "tasks"} from the region.`,
    );
  }

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
    <!-- Counts rather than a sentence about how the list works. The keys are
         in the strip along the bottom of the window now, and what this line is
         worth saying is how much there is and how much of it is finished. -->
    <p class="counts">
      {workspace.tasks.length}
      {workspace.tasks.length === 1 ? "task" : "tasks"}{#if done > 0} · {done} done{/if} · order is
      the content
    </p>
    {#if through}
      <button class="ghost" onclick={extract}>Extract from region</button>
    {/if}
    <button class="ghost" onclick={add}>Add task</button>
  </header>

  <p class="announce" role="status" aria-live="polite">{said}</p>

  <div class="columns">
    <!--
      The map of the region the plan is being read through.

      Only when there is one. A pane that was always there would be a third of
      the width of the plan spent on an empty box for everybody who has never
      drawn a region -- and the plan, unlike the outline, is a list whose whole
      job is to be read straight down.
    -->
    {#if through && mapRoot}
      <aside class="map-pane" aria-label="The region this plan came from">
        <p class="pane-title">Map · the region this plan came from</p>
        <MindmapCanvas {workspace} rootedAt={mapRoot} compact />
        <p class="pane-note">
          Only the tasks extracted from this region are listed. <strong>Extract from
          region</strong> puts the rest of its lines on the plan.
        </p>
      </aside>
    {/if}

    <div class="list">
      {#if regions.length > 0}
        <!--
          Radios rather than a select, and one of them is "everything".
          Filtering a plan is a thing somebody flicks between while reading, and
          a dropdown makes each flick two clicks and a menu over the list they
          are trying to read.
        -->
        <fieldset class="through">
          <legend class="sr">Read the plan through</legend>
          <label class:on={through === null}>
            <input
              type="radio"
              name="plan-region-{workspace.id}"
              checked={through === null}
              onchange={() => (through = null)}
            />
            <span>All</span>
          </label>
          {#each regions as region (region.id)}
            <label class:on={through === region.id}>
              <input
                type="radio"
                name="plan-region-{workspace.id}"
                checked={through === region.id}
                onchange={() => (through = region.id)}
              />
              <span>{region.name}</span>
            </label>
          {/each}
        </fieldset>
      {/if}

  <div class="scroller">
    {#if workspace.tasks.length > 0 && shown.length === 0}
      <div class="empty">
        <p>Nothing on the plan from that region yet.</p>
        <p class="quiet">
          <strong>Extract from region</strong> puts each of its lines on the plan, in the
          order they are written.
        </p>
      </div>
    {:else if workspace.tasks.length === 0}
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
        {#each shown as { task, at } (task.id)}
          {@const status = statusOf(task)}
          {@const source = workspace.sourceOf(task)}
          {@const deletedSource = source === null && sourceIdOf(task) !== null}
          <li data-status={status} style="--number: '{at + 1}'">
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
                onfocus={() => workspace.enter(task.id)}
                onblur={() => workspace.leave(task.id)}
                {@attach (el) => register(task.id, el)}
              />

              {#if workspace.marks[task.id] !== undefined}
                <!--
                  The same rule the outline follows, because a task's title is a
                  line somebody types into like any other: what arrived is shown
                  and offered, never taken.
                -->
                <p class="changed" role="status">
                  Changed elsewhere — now &ldquo;{workspace.marks[task.id]}&rdquo;
                  <button onclick={() => workspace.takeArrived(task.id)}>Use theirs</button>
                </p>
              {/if}

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

      {#if next >= 0}
        <!-- The one thing the plan is for, said once at the end of it rather
             than as a badge on a row: which of these is next is a fact about
             the order, and the order is the whole list. -->
        <p class="next">Work mode takes number {next + 1} next.</p>
      {/if}
    </div>
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

  .counts {
    margin: 0 auto 0 0;
    color: var(--muted);
    font-size: 11.5px;
  }

  /* Two columns when a region is being read through, one when not. The map is
     a fixed width because it is a picture: letting it share the slack would
     shrink the plan every time somebody clicked a region. */
  .columns {
    display: flex;
    flex: 1;
    min-height: 0;
  }

  .map-pane {
    display: flex;
    flex: none;
    flex-direction: column;
    gap: 8px;
    width: 430px;
    min-height: 0;
    padding: 12px;
    border-right: 1px solid var(--line);
  }

  .pane-title,
  .pane-note {
    margin: 0;
    flex: none;
    color: var(--muted);
    font-size: 11px;
  }

  .pane-note {
    padding: 10px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: var(--panel-2);
    line-height: 1.55;
  }

  .pane-note strong {
    color: var(--text);
    font-weight: 500;
  }

  .list {
    display: flex;
    flex: 1;
    min-width: 0;
    min-height: 0;
    flex-direction: column;
  }

  .through {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    gap: 4px;
    margin: 0;
    padding: 8px 12px 0;
    border: none;
  }

  .through label {
    padding: 2px 9px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .through label.on {
    border-color: var(--accent);
    color: var(--accent);
  }

  /* The radio itself is off screen, not hidden: the label is what is drawn, and
     an input with display:none is an input the keyboard cannot reach. */
  .through input {
    position: absolute;
    width: 1px;
    height: 1px;
    opacity: 0;
  }

  .through label:has(:focus-visible) {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  .next {
    flex: none;
    margin: 0;
    padding: 8px 12px;
    border-top: 1px solid var(--line);
    color: var(--muted);
    font-size: 11.5px;
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
  }

  li {
    padding: 5px 0 5px 26px;
    border-bottom: 1px solid var(--line);
    position: relative;
  }

  /* The number is drawn rather than read out: the input's own label already
     says "Task 3 of 9", and a list marker would say the number twice.

     It comes from the row rather than from a CSS counter, because filtering
     the list to one region must not renumber it: these are positions in the
     plan, and "take number 3 next" has to mean the same task whatever somebody
     has clicked. A counter counts what is on screen. */
  li::before {
    content: var(--number, "");
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

  /* Drawn as a pill, and still a native select underneath. A hand-rolled
     three-state control here would be reimplementing a listbox badly: this one
     already knows the keyboard, the touch picker and the platform's own
     conventions, and the only thing the design wanted from it was its shape. */
  select {
    flex: none;
    padding: 1px 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
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

  .changed {
    grid-column: 1 / -1;
    margin: 2px 0 0;
    font-size: 11px;
    color: var(--muted);
  }

  .changed button {
    margin-left: 6px;
    padding: 1px 6px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    font-size: 11px;
    cursor: pointer;
  }
</style>
