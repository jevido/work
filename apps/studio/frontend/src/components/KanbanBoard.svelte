<script lang="ts">
  import type { AgentIdentity } from "../lib/claude/session.svelte";
  import type { Card, TaskBoard } from "../lib/board/board.svelte";
  import TaskDialog from "./TaskDialog.svelte";

  let {
    board,
    agents,
  }: { board: TaskBoard; agents: AgentIdentity[] } = $props();

  const roster = $derived(new Map(agents.map((a) => [a.id, a])));

  /** The card being read in full, if any. */
  let openCard = $state<Card | null>(null);
  /** The card that opened the panel, so closing it hands focus back. */
  let opener: HTMLElement | null = null;

  /**
   * The open card as the board has it now.
   *
   * The board arrives as a whole snapshot, so the card object in `openCard` is
   * a stale copy the moment anything changes. Reading it back by id keeps the
   * panel telling the truth while it is open -- and closes it by itself if
   * Anton drops the task.
   */
  const reading = $derived.by(() => {
    const open = openCard;
    if (!open) return null;
    return board.cards.find((c) => c.id === open.id) ?? null;
  });

  function who(agentId: string): AgentIdentity {
    return roster.get(agentId) ?? { id: agentId, name: agentId, colour: "#8b93a3" };
  }

  function open(card: Card, from: EventTarget | null) {
    opener = from instanceof HTMLElement ? from : null;
    openCard = card;
  }

  function close() {
    openCard = null;
    opener?.focus();
    opener = null;
  }

  function onCardClick(card: Card, event: MouseEvent) {
    // The id and the title are selectable on purpose, and selecting them ends
    // in a click. Copying a task id should not also open a panel over it.
    const selection = window.getSelection();
    if (selection && !selection.isCollapsed && selection.toString().trim()) return;
    open(card, event.currentTarget);
  }

  function onCardKeydown(card: Card, event: KeyboardEvent) {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    open(card, event.currentTarget);
  }
</script>

<section class="board" aria-label="Task board">
  {#each board.columns as column (column.status)}
    <div class="column" data-status={column.status}>
      <div class="column-head">
        <span class="label">{column.label}</span>
        <span class="count">{column.cards.length}</span>
      </div>
      <div class="cards">
        {#each column.cards as card (card.id)}
          <!-- A card is a button in everything but markup: it holds text that
               is meant to stay selectable, which a real <button> would take
               away. -->
          <div
            class="card"
            role="button"
            tabindex="0"
            aria-label="Open task {card.id}"
            style:--agent={who(card.agentId).colour}
            onclick={(event) => onCardClick(card, event)}
            onkeydown={(event) => onCardKeydown(card, event)}
          >
            <div class="assignee">
              <span class="dot"></span>
              <span class="name">{who(card.agentId).name}</span>
              <span class="ref">{card.id}</span>
            </div>
            <p class="title">{card.title}</p>
            {#if card.note}
              <p class="note">{card.note}</p>
            {/if}
          </div>
        {/each}
        {#if column.cards.length === 0}
          <div class="empty" aria-hidden="true"></div>
        {/if}
      </div>
    </div>
  {/each}
</section>

{#if reading}
  <TaskDialog card={reading} assignee={who(reading.agentId)} onclose={close} />
{/if}

<style>
  .board {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1px;
    height: 100%;
    min-height: 0;
    background: var(--line);
    border-bottom: 1px solid var(--line);
  }

  .column {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    background: var(--panel);
  }

  .column-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 6px;
    padding: 7px 9px 5px;
    color: var(--muted);
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .count {
    font-variant-numeric: tabular-nums;
  }

  /* Only the columns that mean something get a colour: work in flight and work
     that needs attention. Assigned and Done are the quiet default. */
  .column[data-status="doing"] .label {
    color: var(--accent);
  }

  .column[data-status="blocked"] .label {
    color: var(--err);
  }

  .cards {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 7px 7px;
  }

  .card {
    margin-bottom: 5px;
    padding: 6px 8px;
    border: 1px solid var(--line);
    border-left: 2px solid var(--agent);
    border-radius: 5px;
    background: var(--panel-2);
    /* The whole card opens it, so the whole card has to look like it does. */
    cursor: pointer;
    text-align: left;
  }

  .card:hover {
    border-color: #3a4250;
  }

  .card:focus-visible {
    outline: 1px solid var(--accent);
    outline-offset: 1px;
  }

  .assignee {
    display: flex;
    align-items: center;
    gap: 5px;
    margin-bottom: 3px;
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--agent);
    flex: none;
  }

  .name {
    font-size: 10.5px;
    font-weight: 600;
    color: var(--muted);
  }

  /* The ID is how you refer to a task when talking to Anton, so it has to be
     readable at a glance and selectable to copy. */
  .ref {
    margin-left: auto;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10px;
    color: var(--muted);
    user-select: text;
  }

  .title {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.4;
    /* Anton's task descriptions can run long; the conversation has the full
       text, so the card shows enough to recognise it by. */
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    overflow: hidden;
    user-select: text;
  }

  .note {
    margin: 4px 0 0;
    color: var(--err);
    font-size: 10.5px;
    line-height: 1.35;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
    user-select: text;
  }

  .column[data-status="done"] .card .title {
    color: var(--muted);
  }

  .empty {
    height: 2px;
  }
</style>
