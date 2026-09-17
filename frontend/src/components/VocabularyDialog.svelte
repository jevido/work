<script lang="ts">
  import { labelOf } from "../lib/workspace/model";
  import type { Term, Workspace } from "../lib/workspace/workspace.svelte";
  import { PanelDrag, dragHandle } from "../lib/ui/drag.svelte";

  export type Which = "guidelines" | "parties";

  let {
    workspace,
    which = $bindable<Which>("guidelines"),
    onclose,
    onreveal,
  }: {
    workspace: Workspace;
    which: Which;
    onclose: () => void;
    /** Takes the caret to a card, which is what the reverse list is for. */
    onreveal: (id: string) => void;
  } = $props();

  const drag = new PanelDrag();

  /*
   * Two vocabularies behind one switch, and they stay two things.
   *
   * The switch is here because they are the same *shape* of panel and a
   * workspace's words are one thing to go and set up, not two. Everything the
   * panel does goes through the pair of methods for whichever is showing --
   * there is no shared add() or remove() underneath, so either can grow a
   * field the other has no use for without this file arguing about it.
   */
  const LABELS: Record<Which, { title: string; one: string; add: string; empty: string }> = {
    guidelines: {
      title: "Guidelines",
      one: "guideline",
      add: "improves…",
      empty:
        "A guideline is what this workspace is trying to be — “improves performance”, " +
        "“improves onboarding”. Cards are put under them, and the list below then answers " +
        "which cards are worth doing.",
    },
    parties: {
      title: "Interested",
      one: "party",
      add: "who is waiting…",
      empty:
        "A person, a team or a customer waiting on something — “Sales”, “the Berlin pilot”. " +
        "Cards say who is interested, and the list below then answers what each of them is " +
        "waiting on.",
    },
  };

  const copy = $derived(LABELS[which]);
  const terms = $derived<Term[]>(which === "guidelines" ? workspace.guidelines : workspace.parties);

  let adding = $state("");
  /** The term whose cards are open, or null. */
  let open = $state<string | null>(null);
  let said = $state("");

  function add() {
    const name = adding.trim();
    if (name === "") return;
    const id = which === "guidelines" ? workspace.addGuideline(name) : workspace.addParty(name);
    if (id === "") return;
    adding = "";
    said = `Added ${name}.`;
  }

  function rename(id: string, name: string) {
    if (which === "guidelines") workspace.renameGuideline(id, name);
    else workspace.renameParty(id, name);
  }

  function remove(term: Term) {
    if (which === "guidelines") workspace.removeGuideline(term.id);
    else workspace.removeParty(term.id);
    if (open === term.id) open = null;
    said =
      term.count === 0
        ? `Removed ${term.name}.`
        : `Removed ${term.name}, and took it off ${term.count} ${term.count === 1 ? "card" : "cards"}.`;
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    onclose();
  }
</script>

<div
  class="dialog"
  role="dialog"
  aria-label="{copy.title} for this workspace"
  tabindex="-1"
  style:transform={drag.transform}
  onkeydown={onKeydown}
  {@attach (node: HTMLElement) => node.focus()}
>
  <header {@attach dragHandle(drag)}>
    <div class="switch" role="group" aria-label="Which vocabulary">
      <button
        class:on={which === "guidelines"}
        aria-pressed={which === "guidelines"}
        onclick={() => ((which = "guidelines"), (open = null))}
      >
        Guidelines
      </button>
      <button
        class:on={which === "parties"}
        aria-pressed={which === "parties"}
        onclick={() => ((which = "parties"), (open = null))}
      >
        Interested
      </button>
    </div>
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">✕</button>
  </header>

  <p class="announce" role="status" aria-live="polite">{said}</p>

  <div class="body">
    {#if terms.length === 0}
      <p class="blurb">{copy.empty}</p>
    {/if}

    <ul class="terms">
      {#each terms as term (term.id)}
        <li>
          <div class="row">
            <input
              value={term.name}
              aria-label="Name of this {copy.one}"
              spellcheck="false"
              autocomplete="off"
              onchange={(event) => rename(term.id, event.currentTarget.value)}
            />
            <!--
              The count is the reverse search, and it is a button because that
              is what it does: "seven cards improve scalability" is only useful
              if the next click is those seven cards.
            -->
            <button
              class="count"
              aria-expanded={open === term.id}
              disabled={term.count === 0}
              onclick={() => (open = open === term.id ? null : term.id)}
            >
              {term.count}
              {term.count === 1 ? "card" : "cards"}
            </button>
            <button
              class="danger"
              aria-label="Remove {term.name}"
              title="Remove"
              onclick={() => remove(term)}>✕</button
            >
          </div>

          {#if open === term.id}
            <ul class="cards">
              {#each workspace.cardsUnder(term.id) as card (card.id)}
                <li>
                  <button
                    onclick={() => {
                      onreveal(card.id);
                      onclose();
                    }}
                  >
                    {labelOf(card, 70)}
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </li>
      {/each}
    </ul>
  </div>

  <footer>
    <form
      onsubmit={(event) => {
        event.preventDefault();
        add();
      }}
    >
      <label>
        <span class="sr">A new {copy.one}</span>
        <input bind:value={adding} placeholder={copy.add} autocomplete="off" spellcheck="false" />
      </label>
      <button type="submit" disabled={adding.trim() === ""}>Add</button>
    </form>
  </footer>
</div>

<style>
  .dialog {
    position: fixed;
    z-index: 21;
    --w: min(420px, calc(100vw - 32px));
    top: 12vh;
    left: 50%;
    margin-left: calc(var(--w) / -2);
    width: var(--w);
    display: flex;
    flex-direction: column;
    max-height: 72vh;
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
    padding: 7px 8px 7px 10px;
    border-bottom: 1px solid var(--line);
    cursor: grab;
  }

  header:active {
    cursor: grabbing;
  }

  .switch {
    display: flex;
    gap: 2px;
    margin-right: auto;
  }

  .switch button {
    padding: 3px 10px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font-size: 11.5px;
    cursor: pointer;
  }

  .switch button:hover {
    color: var(--text);
  }

  /* Not colour alone, as everywhere else that shows which of a pair is on. */
  .switch button.on {
    border-color: var(--line);
    background: var(--panel-2);
    color: var(--text);
    box-shadow: inset 0 -2px 0 var(--accent);
  }

  .close {
    padding: 1px 6px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    cursor: pointer;
  }

  .close:hover {
    color: var(--text);
  }

  .body {
    flex: 1;
    min-height: 0;
    padding: 10px 12px;
    overflow-y: auto;
  }

  .blurb {
    margin: 0 0 10px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.55;
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .terms > li {
    padding: 3px 0;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  input {
    min-width: 0;
    padding: 4px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: inherit;
    font: inherit;
    font-size: 12.5px;
  }

  .row input {
    flex: 1;
  }

  .count {
    flex: none;
    padding: 3px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: transparent;
    color: var(--muted);
    font-size: 10.5px;
    white-space: nowrap;
    cursor: pointer;
  }

  .count:disabled {
    cursor: default;
    opacity: 0.55;
  }

  .count[aria-expanded="true"] {
    border-color: var(--accent);
    color: var(--text);
  }

  .danger {
    flex: none;
    padding: 2px 6px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    cursor: pointer;
  }

  .danger:hover {
    color: var(--err);
  }

  .cards {
    margin: 2px 0 6px;
    border-left: 1px solid var(--line);
  }

  .cards li button {
    display: block;
    width: 100%;
    padding: 3px 10px;
    border: none;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 11.5px;
    text-align: left;
    cursor: pointer;
  }

  .cards li button:hover {
    background: var(--panel-2);
    color: var(--text);
  }

  footer {
    border-top: 1px solid var(--line);
    padding: 8px 12px;
  }

  form {
    display: flex;
    gap: 6px;
  }

  form label {
    flex: 1;
    min-width: 0;
  }

  form input {
    width: 100%;
  }

  form button {
    flex: none;
    padding: 4px 12px;
    border: 1px solid var(--accent);
    border-radius: 4px;
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
    font-size: 11.5px;
    cursor: pointer;
  }

  form button:disabled {
    border-color: var(--line);
    background: var(--panel-2);
    color: var(--muted);
    font-weight: 400;
    cursor: default;
  }

  .announce {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
