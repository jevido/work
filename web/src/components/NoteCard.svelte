<script lang="ts">
  import { PanelDrag, dragHandle } from "@ui/drag.svelte";
  import type { Viewer } from "../lib/viewer.svelte";

  let {
    viewer,
    id,
    /** Where the note is on the canvas, in its pixels. See MapView.places. */
    x,
    y,
    /** How many cards were open before this one, so the newest is on top. */
    stack = 0,
    onraise,
    onclose,
  }: {
    viewer: Viewer;
    id: string;
    x: number;
    y: number;
    stack?: number;
    /** Pressed anywhere on the card: bring it in front of the others. */
    onraise?: () => void;
    onclose: () => void;
  } = $props();

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

  /**
   * Sat on its note rather than beside it.
   *
   * A few pixels up and left of the note's own corner, so the paper shows
   * around two edges of the card and the card reads as that note opened rather
   * than as a panel that happens to be there. It moves with the board and goes
   * off the edge with it; the map clips both.
   */
  const LIFT = 6;

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
  style:left="{x - LIFT}px"
  style:top="{y - LIFT}px"
  style:z-index={2 + stack}
  style:transform={drag.transform}
  class:dragging={drag.dragging}
  onkeydown={onKeydown}
  onpointerdown={() => onraise?.()}
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
    display: grid;
    gap: 10px;
    width: min(320px, calc(100% - 24px));
    max-height: calc(100% - 24px);
    padding: 0 12px 12px;
    overflow: auto;
    /* A surface of its own rather than the page's panel colour. The board is
       the darkest thing in the window and the panel colour is two shades off
       it, so a card drawn in it read as a slightly different patch of dark
       with writing on it. This lifts, and the edge is the one meant to be
       seen -- --line is a hairline between things. */
    border: 1px solid var(--card-line);
    border-radius: 8px;
    background: var(--card);
    box-shadow: 0 14px 36px rgb(0 0 0 / 0.5);
  }

  /* Lifted while it is being moved, so two overlapping cards cannot leave the
     one in hand underneath the other. */
  .card.dragging {
    z-index: 3;
  }

  header {
    position: sticky;
    top: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 -12px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--card-line);
    background: var(--card);
  }

  .chip {
    padding: 1px 8px;
    border: 1px solid var(--card-line);
    border-radius: 999px;
    color: var(--text);
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

  .close {
    color: var(--text);
  }

  .close:hover {
    border-color: var(--card-line);
    background: var(--card-2);
  }

  h3 {
    margin: 0;
    font-size: 14px;
    line-height: 1.35;
    overflow-wrap: anywhere;
  }

  h4 {
    margin: 0 0 4px;
    color: var(--card-label);
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
    /* The body of the card, in the colour the title is in. It was --muted,
       which is a label colour: a paragraph somebody opened the card to read
       should not be the faintest thing on it. */
    color: var(--text);
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
    border: 1px solid var(--card-line);
    border-radius: 999px;
    background: var(--card-2);
    font-size: 11px;
  }

  .links {
    display: grid;
    gap: 4px;
    font-size: 12px;
  }

  .links .dangling {
    color: var(--card-label);
  }

  .why {
    color: var(--card-label);
    font-size: 11px;
  }

  .tasks,
  .gone {
    margin: 0;
    color: var(--card-label);
    font-size: 12px;
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
