<script lang="ts">
  import { PanelDrag, dragHandle } from "@ui/drag.svelte";
  import type { Viewer } from "../lib/viewer.svelte";

  let {
    viewer,
    id,
    /** How many cards were already open when this one was, for the stagger. */
    at = 0,
    onclose,
  }: { viewer: Viewer; id: string; at?: number; onclose: () => void } = $props();

  /**
   * The card, read fresh.
   *
   * Derived rather than copied, so a card left open while the document polls
   * shows what arrived rather than what was on screen when it was clicked --
   * which on a page whose whole job is to be current would be the one stale
   * thing in the window.
   */
  const card = $derived(viewer.cardOf(id));

  const drag = new PanelDrag();

  /** Where an unmoved card opens, stepping down and right per card. */
  const STEP = 28;
  const offset = $derived(Math.min(at, 8) * STEP);

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    event.stopPropagation();
    onclose();
  }
</script>

<!--
  No backdrop and draggable, the way the app draws the same card: what is
  behind it is the board the note is on, and half of reading a note is seeing
  where it sits. Several can be open at once for the same reason -- a reader
  comparing two branches should be able to put both on screen, which is the
  thing a modal makes impossible.
-->
<div
  class="card"
  role="dialog"
  aria-label="Note: {card?.title || 'an empty line'}"
  style:top="{offset}px"
  style:left="{offset}px"
  style:transform={drag.transform}
  class:dragging={drag.dragging}
  onkeydown={onKeydown}
  tabindex="-1"
  {@attach (node: HTMLElement) => void node.focus()}
>
  <header {@attach dragHandle(drag)}>
    {#if card?.region}
      <span class="chip">{card.region || "unnamed region"}</span>
    {/if}
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">✕</button>
  </header>

  {#if !card}
    <!-- The line went while its card was open: deleted on somebody else's
         machine, which is a thing that happens to a page that polls. Said
         rather than closed from under the reader. -->
    <p class="gone">This line is no longer in the workspace.</p>
  {:else}
    <h3>{card.title.trim() || "(an empty line)"}</h3>

    {#if card.detail.trim()}
      <!-- Newlines are kept here and nowhere else, because this is the field
           they are written into. -->
      <p class="detail">{card.detail}</p>
    {/if}

    {#if card.guidelines.length > 0}
      <section>
        <h4>Guidelines</h4>
        <ul class="tags">
          {#each card.guidelines as name, n (n)}
            <li>{name}</li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if card.parties.length > 0}
      <section>
        <h4>Waiting on it</h4>
        <ul class="tags">
          {#each card.parties as name, n (n)}
            <li>{name}</li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if card.links.length > 0}
      <section>
        <h4>Links</h4>
        <ul class="links">
          {#each card.links as link (link.other)}
            <li class:dangling={link.dangling}>
              {link.text || "(an empty line)"}
              {#if link.dangling}<span class="why">no longer on the board</span>{/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if card.tasks > 0}
      <p class="tasks">
        Broken into {card.tasks} {card.tasks === 1 ? "task" : "tasks"} on the plan.
      </p>
    {/if}
  {/if}
</div>

<style>
  .card {
    position: absolute;
    z-index: 2;
    display: grid;
    gap: 10px;
    width: min(320px, calc(100% - 24px));
    max-height: calc(100% - 24px);
    padding: 0 12px 12px;
    overflow: auto;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel);
    box-shadow: 0 10px 28px rgb(0 0 0 / 0.35);
  }

  /* Lifted while it is being moved, so two overlapping cards cannot leave the
     one in hand underneath the other. */
  .card.dragging {
    z-index: 3;
  }

  header {
    position: sticky;
    top: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 -12px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--line);
    background: var(--panel);
  }

  .chip {
    padding: 1px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .close {
    margin-left: auto;
    padding: 2px 6px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: none;
    color: var(--muted);
    font: inherit;
    line-height: 1;
    cursor: pointer;
  }

  .close:hover {
    border-color: var(--line);
    color: var(--text);
  }

  h3 {
    margin: 0;
    font-size: 14px;
    line-height: 1.35;
    overflow-wrap: anywhere;
  }

  h4 {
    margin: 0 0 4px;
    color: var(--muted);
    font-size: 10.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  section {
    margin: 0;
  }

  .detail {
    margin: 0;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  ul {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .tags li {
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    font-size: 11px;
  }

  .links {
    display: grid;
    gap: 4px;
    font-size: 12px;
  }

  .links .dangling {
    color: var(--muted);
  }

  .why {
    color: var(--muted);
    font-size: 11px;
  }

  .tasks,
  .gone {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
