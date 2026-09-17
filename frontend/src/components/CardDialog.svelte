<script lang="ts">
  import { ICON_LABELS, ICON_NAMES } from "../lib/mindmap/icons";
  import { labelOf } from "../lib/workspace/model";
  import type { OutlineContext } from "../lib/workspace/outline.svelte";
  import type { Row } from "../lib/workspace/model";
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import { PanelDrag, dragHandle } from "../lib/ui/drag.svelte";

  let {
    workspace,
    row,
    outline,
    onclose,
    onmanage,
  }: {
    workspace: Workspace;
    row: Row;
    outline: OutlineContext;
    onclose: () => void;
    /** Opens the workspace's vocabularies, for when the right word is not there yet. */
    onmanage: (which: "guidelines" | "parties") => void;
  } = $props();

  const drag = new PanelDrag();

  /**
   * The body, held locally while it is being typed.
   *
   * A title is one op per 400ms because the map redraws around it and somebody
   * has to see what they are writing. A body is a paragraph in a panel that
   * draws nothing, and an op per keystroke there would be a paragraph's worth
   * of ops in the log for one paragraph of text. So it is held here and written
   * when the caret leaves it, when the dialog moves to another card, and when
   * it closes.
   */
  let detail = $state("");

  /** Which card `detail` was loaded from, so a move to another one can flush. */
  let loaded = $state("");

  function flush() {
    if (loaded === "") return;
    workspace.setDetail(loaded, detail);
  }

  $effect(() => {
    const id = row.node.id;
    if (id === loaded) return;
    // The card under the dialog changed -- Enter made a new line, or the
    // caret stepped to the next one. What was typed into the old one is
    // written before the new one is read, or it is lost on a keypress.
    flush();
    loaded = id;
    detail = workspace.detail(id);
  });

  // Closing, switching tabs and switching modes all unmount this. None of them
  // is a reason to throw away what somebody wrote.
  $effect(() => () => flush());

  const guidelines = $derived(workspace.guidelinesOf(row.node.id));
  const parties = $derived(workspace.partiesOf(row.node.id));
  const links = $derived(workspace.linksOf(row.node.id));
  const region = $derived(workspace.regionOf(row.node.id));

  /** Guidelines this card is not already under. */
  const freeGuidelines = $derived(
    workspace.guidelines.filter((term) => !guidelines.some((g) => g.id === term.id)),
  );
  const freeParties = $derived(
    workspace.parties.filter((term) => !parties.some((p) => p.id === term.id)),
  );

  /** Lines this one could be linked to: not itself, not already, not its own branch. */
  let filter = $state("");
  const candidates = $derived.by(() => {
    const already = new Set(links.map((l) => l.other));
    const under = new Set<string>();
    const walk = (node: { id: string; children: { id: string; children: unknown[] }[] }) => {
      under.add(node.id);
      for (const child of node.children) walk(child as never);
    };
    walk(row.node as never);
    const needle = filter.trim().toLowerCase();
    return workspace.rows
      .filter((r) => !under.has(r.node.id) && !already.has(r.node.id))
      .filter((r) => needle === "" || labelOf(r.node, 200).toLowerCase().includes(needle))
      .slice(0, 8);
  });

  const icon = $derived(workspace.iconOf(row.node.id));

  /**
   * Every outline key, on the title.
   *
   * Handed straight to OutlineKeys, which is the same handler the floating
   * editor used before this dialog replaced it: Enter still makes a line, Tab
   * still nests, Alt+arrows still move and fold, and the dialog follows the
   * caret onto whatever line those keys land on. Without this, opening a card
   * would be the end of writing a board with the keyboard.
   *
   * Escape is the one it does not get. In an outline Escape means "put the
   * caret down"; here it means "close this", which is what somebody pressing it
   * at a dialog expects, and OutlineKeys.leave would send focus somewhere
   * behind a panel that was still open.
   */
  function onTitleKey(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onclose();
      return;
    }
    outline.keydown(event, row);
  }
</script>

<!--
  A card, opened.

  No backdrop and draggable, like TaskDialog: what is behind it is the board
  this card is on, and half of reading a card is seeing where it sits. A modal
  would black that out to show a panel about one note.
-->
<div
  class="dialog"
  role="dialog"
  aria-label="Card: {labelOf(row.node, 60)}"
  style:transform={drag.transform}
>
  <header {@attach dragHandle(drag)}>
    <span class="level">Level {row.depth + 1}</span>
    {#if region}
      <span class="chip region">{region.name || "unnamed region"}</span>
    {/if}
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">✕</button>
  </header>

  <div class="body">
    <label class="field">
      <span class="what">Title</span>
      <input
        class="title"
        type="text"
        value={workspace.text(row.node.id)}
        spellcheck="false"
        autocomplete="off"
        placeholder="Write a line…"
        oninput={(event) => workspace.setText(row.node.id, event.currentTarget.value)}
        onkeydown={onTitleKey}
        onfocus={() => workspace.enter(row.node.id)}
        onblur={() => workspace.leave(row.node.id)}
        {@attach (el: HTMLInputElement) => outline.register(row.node.id, el)}
      />
    </label>

    <label class="field">
      <span class="what">Detail</span>
      <!-- Newlines are kept here and nowhere else. A title is a line; this is
           where the paragraph that did not fit on it goes. -->
      <textarea
        bind:value={detail}
        rows="5"
        placeholder="What this is, at length…"
        onblur={flush}
      ></textarea>
    </label>

    {#if row.depth === 0}
      <!--
        A select, not the grid of eleven buttons this was. The grid showed every
        glyph at once, which sounds like the helpful thing and in a panel this
        tall meant a third of the card was decoration -- above the guidelines
        and the people waiting on it, which are what the card is for. One row,
        and the list is one click away.
      -->
      <label class="field">
        <span class="what">Glyph</span>
        <select
          value={icon}
          onchange={(event) => workspace.setIcon(row.node.id, event.currentTarget.value)}
        >
          <option value="">No glyph</option>
          {#each ICON_NAMES as name (name)}
            <option value={name}>{ICON_LABELS[name]}</option>
          {/each}
        </select>
      </label>
    {/if}

    <!--
      What this card is for, and who is waiting for it. Two lists rather than
      one: they answer different questions, and the reverse view reads them
      separately.
    -->
    <div class="field">
      <span class="what">Guidelines</span>
      {#if guidelines.length > 0}
        <ul class="tags">
          {#each guidelines as tag (tag.join)}
            <li>
              <span>{tag.name}</span>
              <button
                aria-label="Take this card out from under {tag.name}"
                title="Remove"
                onclick={() => workspace.unguide(tag.join)}>✕</button
              >
            </li>
          {/each}
        </ul>
      {/if}
      <div class="add">
        {#if freeGuidelines.length > 0}
          <label>
            <span class="sr">Put this card under a guideline</span>
            <select
              value=""
              onchange={(event) => {
                workspace.guide(row.node.id, event.currentTarget.value);
                event.currentTarget.value = "";
              }}
            >
              <option value="" disabled>Add a guideline…</option>
              {#each freeGuidelines as term (term.id)}
                <option value={term.id}>{term.name}</option>
              {/each}
            </select>
          </label>
        {:else if workspace.guidelines.length === 0}
          <p class="none">This workspace has no guidelines yet.</p>
        {:else}
          <p class="none">Under all of them.</p>
        {/if}
        <button class="ghost" onclick={() => onmanage("guidelines")}>Manage…</button>
      </div>
    </div>

    <div class="field">
      <span class="what">Interested</span>
      {#if parties.length > 0}
        <ul class="tags">
          {#each parties as tag (tag.join)}
            <li>
              <span>{tag.name}</span>
              <button
                aria-label="{tag.name} is no longer interested"
                title="Remove"
                onclick={() => workspace.removeInterest(tag.join)}>✕</button
              >
            </li>
          {/each}
        </ul>
      {/if}
      <div class="add">
        {#if freeParties.length > 0}
          <label>
            <span class="sr">Record who is interested in this card</span>
            <select
              value=""
              onchange={(event) => {
                workspace.addInterest(row.node.id, event.currentTarget.value);
                event.currentTarget.value = "";
              }}
            >
              <option value="" disabled>Add who is interested…</option>
              {#each freeParties as term (term.id)}
                <option value={term.id}>{term.name}</option>
              {/each}
            </select>
          </label>
        {:else if workspace.parties.length === 0}
          <p class="none">Nobody has been named yet.</p>
        {:else}
          <p class="none">Everyone is on it.</p>
        {/if}
        <button class="ghost" onclick={() => onmanage("parties")}>Manage…</button>
      </div>
    </div>

    <div class="field">
      <span class="what">Links</span>
      {#if links.length > 0}
        <ul class="links">
          {#each links as link (link.edge)}
            {@const other = workspace.text(link.other).trim() || "an empty line"}
            <li>
              <div class="other">
                <span>{other}</span>
                <button
                  aria-label="Unlink from {other}"
                  title="Unlink"
                  onclick={() => workspace.unlink(link.edge)}>✕</button
                >
              </div>
              <input
                value={link.text}
                placeholder="because…"
                autocomplete="off"
                spellcheck="false"
                aria-label="Why this card is linked to {other}"
                oninput={(event) => workspace.setLinkText(link.edge, event.currentTarget.value)}
              />
            </li>
          {/each}
        </ul>
      {/if}
      <label class="add">
        <span class="sr">Link this card to</span>
        <input bind:value={filter} placeholder="link to…" autocomplete="off" spellcheck="false" />
      </label>
      {#if filter.trim() !== ""}
        {#if candidates.length === 0}
          <p class="none">Nothing matches.</p>
        {:else}
          <ul class="candidates">
            {#each candidates as option (option.node.id)}
              <li>
                <button
                  onclick={() => {
                    workspace.link(row.node.id, option.node.id);
                    filter = "";
                  }}
                >
                  {labelOf(option.node, 60)}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </div>
  </div>

  <footer>
    <button onclick={() => outline.promote(row)}>
      {workspace.taskFor(row.node.id) ? "On the plan" : "To plan"}
    </button>
    <button
      class="danger"
      onclick={() => {
        outline.remove(row);
        onclose();
      }}
    >
      Remove
    </button>
  </footer>
</div>

<style>
  .dialog {
    /* Fixed and to one side, not centred: what is behind it is the board this
       card sits on, and covering the middle of the map would hide the thing
       the card is being read against. */
    position: fixed;
    z-index: 20;
    --w: min(400px, calc(100vw - 32px));
    top: 8vh;
    left: 24px;
    width: var(--w);
    display: flex;
    flex-direction: column;
    max-height: 82vh;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    box-shadow: 0 12px 32px rgb(0 0 0 / 0.45);
    overflow: hidden;
  }

  header {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 7px 8px 7px 12px;
    border-bottom: 1px solid var(--line);
    cursor: grab;
  }

  header:active {
    cursor: grabbing;
  }

  .level {
    margin-right: auto;
    color: var(--muted);
    font-size: 11px;
    letter-spacing: 0.02em;
    text-transform: uppercase;
  }

  .chip {
    padding: 1px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .close {
    flex: none;
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
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 12px;
    overflow-y: auto;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .what {
    color: var(--muted);
    font-size: 10.5px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  input,
  textarea,
  select {
    width: 100%;
    min-width: 0;
    padding: 5px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: inherit;
    font: inherit;
    font-size: 12.5px;
  }

  textarea {
    resize: vertical;
    line-height: 1.5;
  }

  .title {
    font-size: 14px;
    font-weight: 600;
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .tags li {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 2px 4px 2px 9px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    font-size: 11.5px;
  }

  .tags li button,
  .links .other button {
    padding: 0 3px;
    border: none;
    border-radius: 3px;
    background: transparent;
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .tags li button:hover,
  .links .other button:hover {
    color: var(--err);
  }

  .add {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .add label {
    flex: 1;
    min-width: 0;
  }

  .links li {
    padding: 3px 0;
  }

  .links .other {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .links .other span {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    color: var(--muted);
    font-size: 10.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .links input {
    margin-top: 2px;
    font-size: 11.5px;
  }

  .candidates li button {
    display: block;
    width: 100%;
    padding: 3px 8px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: inherit;
    font: inherit;
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }

  .candidates li button:hover {
    background: var(--panel-2);
  }

  .none {
    flex: 1;
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
  }

  .ghost {
    flex: none;
    padding: 4px 8px;
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

  footer {
    display: flex;
    gap: 6px;
    padding: 8px 12px;
    border-top: 1px solid var(--line);
  }

  footer button {
    padding: 4px 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11.5px;
    cursor: pointer;
  }

  footer button:hover {
    border-color: #3a4250;
    color: var(--text);
  }

  footer .danger {
    margin-left: auto;
  }

  footer .danger:hover {
    border-color: var(--err);
    color: var(--err);
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
