<script lang="ts">
  import type { AgentIdentity } from "../lib/claude/session.svelte";
  import type { TaskBoard } from "../lib/board/board.svelte";

  let {
    board,
    agents,
  }: { board: TaskBoard; agents: AgentIdentity[] } = $props();

  const roster = $derived(new Map(agents.map((a) => [a.id, a])));

  function who(agentId: string): AgentIdentity {
    return roster.get(agentId) ?? { id: agentId, name: agentId, colour: "#8b93a3" };
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
          <article class="card" style:--agent={who(card.agentId).colour}>
            <div class="assignee">
              <span class="dot"></span>
              <span class="name">{who(card.agentId).name}</span>
            </div>
            <p class="title">{card.title}</p>
            {#if card.note}
              <p class="note">{card.note}</p>
            {/if}
          </article>
        {/each}
        {#if column.cards.length === 0}
          <div class="empty" aria-hidden="true"></div>
        {/if}
      </div>
    </div>
  {/each}
</section>

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
