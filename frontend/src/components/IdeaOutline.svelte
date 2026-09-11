<script lang="ts">
  import { labelOf } from "../lib/workspace/model";
  import { OutlineKeys, setOutline } from "../lib/workspace/outline.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import OutlineRow from "./OutlineRow.svelte";

  let { workspace }: { workspace: Workspace } = $props();

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

  function unfoldAll() {
    const opened = workspace.unfoldAll();
    keys.say(`Unfolded ${opened} ${opened === 1 ? "branch" : "branches"}.`);
  }
</script>

<section class="idea" tabindex="-1" aria-label="Idea outline" {@attach exitTarget}>
  <header>
    <h2>Outline</h2>
    <p class="hint">
      <!-- The keys, in the order somebody meets them. Written out rather than
           hidden behind a "?" because Tab does not indent anywhere else in
           this app, and a person who does not know that will try it once and
           conclude the outline is broken. -->
      <kbd>Enter</kbd> new line · <kbd>Tab</kbd> / <kbd>Shift</kbd>+<kbd>Tab</kbd> nest ·
      <kbd>Alt</kbd>+<kbd>↑↓</kbd> move · <kbd>Alt</kbd>+<kbd>←→</kbd> fold ·
      <kbd>Ctrl</kbd>+<kbd>Enter</kbd> to plan · <kbd>Esc</kbd> leave
    </p>
    {#if workspace.foldedCount > 0}
      <button class="ghost" onclick={unfoldAll}>
        Unfold all ({workspace.foldedCount})
      </button>
    {/if}
  </header>

  <!--
    Announced, never drawn. Indent, move and fold change a shape rather than
    any text, so without this they are silent keys that appear to do nothing.
    Outside the {#if} below so it exists before it has anything to say: a live
    region announced into being is a region nobody hears.
  -->
  <p class="announce" role="status" aria-live="polite">{keys.said}</p>

  <div class="scroller">
    {#if workspace.rows.length === 0}
      <div class="empty">
        <p>Nothing here yet.</p>
        <button class="primary" onclick={start}>Start the outline</button>
      </div>
    {:else}
      <OutlineRow rows={workspace.rows} />
    {/if}

    {#if workspace.detached.length > 0}
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
    {/if}
  </div>
</section>

<style>
  .idea {
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

  .empty {
    display: grid;
    justify-items: start;
    gap: 8px;
    padding: 24px 0;
    color: var(--muted);
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
</style>
