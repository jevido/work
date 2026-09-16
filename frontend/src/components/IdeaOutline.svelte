<script lang="ts">
  import { labelOf } from "../lib/workspace/model";
  import { OutlineKeys, setOutline } from "../lib/workspace/outline.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import type { Restructuring } from "../lib/workspace/restructure.svelte";
  import MindmapCanvas from "./MindmapCanvas.svelte";
  import AskClaude from "./AskClaude.svelte";

  /** The name being typed for a new region, or null when none is. */
  let grouping = $state<string | null>(null);

  /**
   * The last line the caret was in.
   *
   * Not keys.active, which is null by the time a button is clicked: blur fires
   * before click, so reading the focused row from inside a click handler reads
   * it after the caret has already left. The last line somebody was in is what
   * they mean by "this branch" anyway.
   */
  let lastLine = $state<string | null>(null);
  $effect(() => {
    if (keys.active) lastLine = keys.active;
  });

  let { workspace, restructuring }: { workspace: Workspace; restructuring: Restructuring } =
    $props();

  /**
   * The line the caret is on, for the composer to mention.
   *
   * Named rather than used to narrow what Claude is shown: reorganising a
   * branch usually means moving something out of it or into it, and a state
   * block cut down to the branch would hide the only places the answer could
   * go.
   */
  const focus = $derived.by(() => {
    const id = keys.active;
    if (!id) return null;
    const row = workspace.rows.find((r) => r.node.id === id);
    return row ? { id, text: labelOf(row.node, 80) } : null;
  });

  /**
   * The keyboard, and where the caret is.
   *
   * One per mount rather than one per workspace: what it holds -- the input
   * elements, the pending focus -- is about this screen, not about the
   * document. Switching tabs unmounts the outline and the caret goes with it,
   * which is right; the workspace it was editing carries on regardless.
   */
  // svelte-ignore state_referenced_locally
  // Read once, deliberately: App keys this component on the workspace id, so a
  // different workspace is a different instance rather than the same one
  // holding a stale reference. Making it reactive instead would leave the
  // caret and the input registry pointing at the outline you just left.
  const keys = new OutlineKeys(workspace);
  setOutline(keys.context());

  /**
   * Where Escape lands. See OutlineKeys.leave.
   *
   * An attachment rather than `bind:this` plus an effect: this is one
   * assignment tied to the element's lifetime, which is exactly what an
   * attachment is, and the effect version needed a piece of reactive state
   * that nothing else ever read.
   */
  function exitTarget(node: HTMLElement) {
    keys.exit = node;
    return () => {
      keys.exit = null;
    };
  }

  /**
   * A line the plan sent us to.
   *
   * Consumed here rather than acted on there, because following that link is
   * a mode switch: at the moment it is clicked this component does not exist,
   * so there is nothing to tell to move the caret. The request is the note it
   * finds on arrival, and it is cleared as it is read so that switching back
   * to Idea later does not jump somewhere for no reason.
   */
  $effect(() => {
    const id = workspace.revealRequest;
    if (!id) return;
    workspace.revealRequest = null;
    keys.focus(id, "end");
  });

  function start() {
    const id = workspace.insertAfter(null);
    if (id) keys.focus(id, "start");
  }

  /**
   * Gathers the line the caret is on, and everything under it, into a region.
   *
   * The branch, because the outline has no multi-select and "this branch" is
   * what people mean by a region most of the time. Say what it took, so nobody
   * has to count rows to find out.
   */
  function group() {
    const id = lastLine;
    if (!id) return;
    const name = (grouping ?? "").trim();
    const region = workspace.group(id, name || "Unnamed region");
    grouping = null;
    if (!region) return;
    const row = workspace.rows.find((r) => r.node.id === id);
    keys.say(
      `Grouped ${row ? labelOf(row.node, 40) : "this line"} and everything under it into ` +
        `${name || "an unnamed region"}.`,
    );
  }

  function unfoldAll() {
    const opened = workspace.unfoldAll();
    keys.say(`Unfolded ${opened} ${opened === 1 ? "branch" : "branches"}.`);
  }

  /**
   * The two shortcuts for the things the outline cannot say by nesting.
   *
   * Here rather than on the row, because both act on the line the caret is on
   * and the caret is in a textarea that has its own handler for every key that
   * moves or indents. These two do neither, so they are caught on the way up.
   *
   * Both need a line to act on. With nothing focused they do nothing rather
   * than guessing at the first line, which would put a region around a branch
   * somebody was not looking at.
   */
  /**
   * Binds the shortcuts to the section imperatively.
   *
   * An `onkeydown` in the markup would be a keyboard handler on a `<section>`,
   * which the a11y rules refuse for a good reason: a non-interactive element
   * with a key handler is usually one that should have been a button. This is
   * the other case -- the keys are pressed in the textareas inside, and this is
   * only where they are caught on the way up, because the handler needs the
   * whole outline's state rather than one row's.
   */
  function shortcuts(node: HTMLElement) {
    node.addEventListener("keydown", onShortcut);
    return () => node.removeEventListener("keydown", onShortcut);
  }

  function onShortcut(event: KeyboardEvent) {
    if (!(event.ctrlKey || event.metaKey) || event.shiftKey || event.altKey) return;
    const line = lastLine;
    if (!line) return;
    const key = event.key.toLowerCase();
    if (key === "l") {
      event.preventDefault();
      workspace.requestLink(line);
      keys.say("Pick a line to link to.");
    } else if (key === "g") {
      event.preventDefault();
      grouping = grouping === null ? "" : null;
      keys.say(grouping === null ? "Grouping cancelled." : "Name this region.");
    }
  }
</script>

<section class="idea" tabindex="-1" aria-label="Idea outline" {@attach exitTarget} {@attach shortcuts}>
  <header>
    <h2>Outline</h2>
    <!-- The keys used to be written out here. They are in the strip along the
         bottom of the window now, where planning mode's are too: two lists of
         shortcuts in two different places, each covering half of what the same
         caret can do, is how you end up reading neither. See HintBar. -->
    {#if lastLine}
      <button class="ghost" onclick={() => (grouping = grouping === null ? "" : null)}>
        Group branch
      </button>
    {/if}
    {#if workspace.foldedCount > 0}
      <button class="ghost" onclick={unfoldAll}>
        Unfold all ({workspace.foldedCount})
      </button>
    {/if}
  </header>

  {#if grouping !== null}
    <!-- A region wants a name, and asking for one afterwards is asking twice. -->
    <form
      class="grouping"
      onsubmit={(event) => {
        event.preventDefault();
        group();
      }}
    >
      <label>
        <span class="sr">Name for this region</span>
        <input
          bind:value={grouping}
          placeholder="name this region…"
          {@attach (el: HTMLInputElement) => el.focus()}
          onkeydown={(event) => event.key === "Escape" && (grouping = null)}
        />
      </label>
      <button type="submit">Group</button>
      <button type="button" onclick={() => (grouping = null)}>Cancel</button>
    </form>
  {/if}

  <AskClaude
    {workspace}
    {restructuring}
    mode="idea"
    {focus}
    label="Ask Claude to reorganise"
    placeholder="group these by which part of the app they touch"
  />

  <!--
    Announced, never drawn. Indent, move and fold change a shape rather than
    any text, so without this they are silent keys that appear to do nothing.
    Outside the {#if} below so it exists before it has anything to say: a live
    region announced into being is a region nobody hears.
  -->
  <p class="announce" role="status" aria-live="polite">{keys.said}</p>

  <!--
    The map, and nothing beside it.

    There was an indented list of inputs here until now, with the canvas as a
    second view you switched to. It is gone: the map is the document. Clicking a
    box opens a real input over it and every key the list understood works in
    it -- the same handler, see MindmapCanvas.
  -->
  <MindmapCanvas {workspace} focused={lastLine} />

  {#if workspace.rows.length === 0}
    <div class="empty">
      <p>Nothing here yet.</p>
      <button class="primary" onclick={start}>Start the outline</button>
    </div>
  {/if}

  {#if workspace.detached.length > 0}
    <div class="strays">
      <!--
        Lines the tree cannot reach: their parent was deleted somewhere else,
        or two people moved the same branch at once. The merge keeps them and
        says so rather than dropping them, and a viewer that hid them would be
        showing an incomplete workspace while looking complete -- which is the
        worst thing a shared outline can do.
      -->
      <section class="detached" aria-label="Lines that are no longer in the outline">
        <h3>Not in the outline</h3>
        <p>
          The line these were under was deleted somewhere else. They are still here; move
          them or delete them.
        </p>
        <ul>
          {#each workspace.detached as node (node.id)}
            <li>
              <span class="text">{labelOf(node, 120)}</span>
              <button class="ghost danger" onclick={() => workspace.remove(node.id)}>
                Remove
              </button>
            </li>
          {/each}
        </ul>
      </section>
    </div>
  {/if}
</section>

<style>
  .idea {
    position: relative;
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    background: var(--bg);
  }

  /* The container is only focusable so Escape has somewhere to land. It is not
     a control, so it does not get a ring of its own -- the ring would appear
     on a whole panel and mean nothing. */
  .idea:focus {
    outline: none;
  }

  header {
    display: flex;
    align-items: baseline;
    gap: 10px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--line);
  }

  h2 {
    /* Takes the slack the keyboard hint used to, so the view switch and the
       buttons stay against the right edge of the header. */
    margin: 0 auto 0 0;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.02em;
    text-transform: uppercase;
    color: var(--muted);
  }

  /* Over the map rather than instead of it. An empty map is a legitimate thing
     to be looking at -- it is what a new tab is -- and a panel that replaced it
     would tear the canvas down every time somebody deleted the last line, then
     build a new one with no pan and no zoom when they wrote another. */
  .empty {
    position: absolute;
    inset: 0;
    z-index: 1;
    display: grid;
    place-content: center;
    justify-items: center;
    gap: 8px;
    color: var(--muted);
    pointer-events: none;
  }

  .empty button {
    pointer-events: auto;
  }

  .empty p {
    margin: 0;
  }

  .detached {
    margin-top: 24px;
    padding: 10px 12px;
    border: 1px solid #4a3a2b;
    border-radius: 6px;
    background: #211c15;
  }

  .detached h3 {
    margin: 0 0 4px;
    font-size: 12px;
  }

  .detached p {
    margin: 0 0 8px;
    color: var(--muted);
    font-size: 11px;
  }

  .detached ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .detached li {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 0;
  }

  .detached .text {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .announce {
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

  .ghost.danger:hover {
    border-color: var(--err);
    color: var(--err);
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

  .grouping {
    display: flex;
    gap: 6px;
    padding: 4px 0;
  }

  .grouping input {
    flex: 1;
    min-width: 0;
    padding: 3px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel);
    color: inherit;
    font: inherit;
    font-size: 12px;
  }

  .grouping button {
    padding: 3px 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  /* Lines the tree cannot reach, under the map rather than in it: they have no
     parent, so there is nowhere on a tree to draw them. */
  .strays {
    flex: none;
    max-height: 30%;
    overflow-y: auto;
    padding: 8px 12px;
    border-top: 1px solid var(--line);
  }
</style>
