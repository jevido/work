<script lang="ts">
  import { STATUS_LABELS, type Card } from "../lib/board/board.svelte";
  import type { AgentIdentity } from "../lib/claude/session.svelte";
  import { PanelDrag, dragHandle } from "../lib/ui/drag.svelte";

  let {
    card,
    assignee,
    onclose,
  }: { card: Card; assignee: AgentIdentity; onclose: () => void } = $props();

  const drag = new PanelDrag();

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    onclose();
  }
</script>

<!-- A card, read out in full. Nothing here is editable: the board is Anton's
     to keep, and this is for the times the card is too small to read. Like the
     desk panel it deliberately has no backdrop -- the office and the board go
     on updating behind it, and the panel can be dragged out of the way. -->
<div
  class="dialog"
  role="dialog"
  aria-label="Task {card.id}"
  tabindex="-1"
  style:transform={drag.transform}
  onkeydown={onKeydown}
  {@attach (node) => node.focus()}
>
  <header {@attach dragHandle(drag)}>
    <span class="dot" style:background={assignee.colour}></span>
    <span class="name">{assignee.name}</span>
    <span class="status" data-status={card.status}>{STATUS_LABELS[card.status]}</span>
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">✕</button>
  </header>

  <div class="body">
    <p class="title">{card.title}</p>
    {#if card.note}
      <p class="note">{card.note}</p>
    {/if}
  </div>

  <footer>
    <span class="ref">{card.id}</span>
  </footer>
</div>

<style>
  .dialog {
    /* Fixed rather than absolute: the board it opens from is a short strip
       with scrolling columns, and a task worth reading in full needs more room
       than that strip has. */
    position: fixed;
    z-index: 20;
    --w: min(520px, calc(100vw - 32px));
    top: 14vh;
    left: 50%;
    margin-left: calc(var(--w) / -2);
    width: var(--w);
    display: flex;
    flex-direction: column;
    max-height: min(64vh, 460px);
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    box-shadow: 0 12px 32px rgb(0 0 0 / 0.45);
    outline: none;
    overflow: hidden;
  }

  header {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 9px 10px 9px 12px;
    border-bottom: 1px solid var(--line);
    background: var(--panel-2);
  }

  .dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex: none;
  }

  .name {
    font-weight: 600;
  }

  .status {
    padding: 1px 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  /* Same two columns that earn a colour on the board, coloured the same way. */
  .status[data-status="doing"] {
    color: var(--accent);
  }

  .status[data-status="blocked"] {
    color: var(--err);
  }

  .close {
    margin-left: auto;
    padding: 2px 7px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }

  .close:hover {
    border-color: var(--line);
    color: var(--text);
  }

  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 12px;
  }

  /* No clamping anywhere in here -- being able to read the whole thing is the
     entire reason the panel exists. */
  .title {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .note {
    margin: 10px 0 0;
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
    line-height: 1.45;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  footer {
    padding: 7px 12px 8px;
    border-top: 1px solid var(--line);
    background: var(--panel-2);
  }

  .ref {
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 10.5px;
    color: var(--muted);
    user-select: text;
  }
</style>
