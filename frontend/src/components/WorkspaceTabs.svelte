<script lang="ts">
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import type { Workspaces } from "../lib/workspace/workspaces.svelte";

  let {
    workspaces,
    /** The id of the region the tabs control, for aria-controls. */
    panelId,
    onnew,
    oncreate,
    onjoin,
    onkeys,
  }: {
    workspaces: Workspaces;
    panelId: string;
    /** A tab in the workspace that is open. */
    onnew: () => void;
    /** A new workspace on a server. */
    oncreate: () => void;
    /** Join one with a key. */
    onjoin: () => void;
    /** Show this workspace's keys, so somebody can be invited to it. */
    onkeys: () => void;
  } = $props();

  /** The tab a close would retire for everybody. */
  let confirming = $state<Workspace | null>(null);

  const tabEls = new Map<string, HTMLElement>();

  function register(id: string, el: HTMLElement) {
    tabEls.set(id, el);
    return () => {
      if (tabEls.get(id) === el) tabEls.delete(id);
    };
  }

  /**
   * What is wrong with one tab, as opposed to with the workspace.
   *
   * This used to carry the sync state, which was per workspace when a
   * workspace was a tab. It is not any more: there is one workspace and one
   * sync loop behind all of these, so a refused key is not a fact about the
   * third tab -- putting a red dot on every tab for one key would be noise
   * with no way to act on it. The badge in the mode bar says that, once.
   *
   * What is genuinely per tab is whether this machine has a folder for it. A
   * tab a colleague made arrives unbound, agents cannot run in it until
   * somebody points it at a project, and that is invisible until you try.
   */
  function unbound(workspace: Workspace): boolean {
    return workspaces.joined && !workspace.bound;
  }

  /** What a tab is announced as, which is more than what it is labelled with. */
  function describe(workspace: Workspace): string {
    const parts = [workspace.name];
    if (unbound(workspace)) parts.push("no folder on this machine");
    if (workspaces.joined && workspaces.runsIn === workspace.id) parts.push("agents run here");
    return parts.join(", ");
  }

  /**
   * The tab strip's keyboard, which is the one from the ARIA tabs pattern:
   * one tab stop for the whole strip, arrows between the tabs, Delete to
   * close.
   *
   * Delete rather than a focusable × inside each tab. Putting a second control
   * in a tab means two stops per workspace and an arrow-key story that has to
   * explain which of the two the arrows move between; the × is still there for
   * the pointer, and is skipped by the keyboard because the keyboard has a
   * better way.
   *
   * There is no F2 any more. Renaming was local -- a tab's name is a field on
   * its node in the workspace document, nothing here wrote one, and a rename
   * that the next workspace:changed silently undid would be worse than not
   * offering it. `ApplyWorkspaceEdits` can write that field now, so renaming
   * is buildable again; it has not been built, and `Workspaces.rename` is
   * still local-only, so the key stays off rather than half-working.
   */
  function onKeydown(event: KeyboardEvent, at: number) {
    const list = workspaces.list;
    let target = -1;
    switch (event.key) {
      case "ArrowLeft":
        target = (at - 1 + list.length) % list.length;
        break;
      case "ArrowRight":
        target = (at + 1) % list.length;
        break;
      case "Home":
        target = 0;
        break;
      case "End":
        target = list.length - 1;
        break;
      case "Delete":
        event.preventDefault();
        askClose(list[at]);
        return;
      default:
        return;
    }
    event.preventDefault();
    const next = list[target];
    if (!next) return;
    // Selection follows focus, which is right when switching is instant: the
    // outline behind every tab is already in memory.
    workspaces.select(next.id);
    tabEls.get(next.id)?.focus();
  }

  /**
   * Always asks.
   *
   * Closing a tab retires it for everyone in the workspace -- it tombstones
   * the tab node, and a tombstone is permanent -- so there is no version of
   * this that is safe enough to do on a keystroke without saying so. The
   * previous confirm only appeared when this machine had unsent changes,
   * which was the least of what this does.
   */
  function askClose(workspace: Workspace) {
    if (!workspaces.joined) return;
    confirming = workspace;
  }

  async function close(workspace: Workspace) {
    const list = workspaces.list;
    const at = list.findIndex((w) => w.id === workspace.id);
    confirming = null;
    await workspaces.close(workspace.id);
    // Focus the tab that took its place, or the strip's new last tab.
    const next = workspaces.list[at] ?? workspaces.list[at - 1] ?? null;
    if (next) queueMicrotask(() => tabEls.get(next.id)?.focus());
  }

  function confirmDialog(node: HTMLDialogElement) {
    node.showModal();
    return () => node.close();
  }
</script>

<div class="bar">
  <div
    class="tabs"
    role="tablist"
    aria-label="Workspaces"
    aria-orientation="horizontal"
  >
    {#each workspaces.list as workspace, at (workspace.id)}
      {@const selected = workspaces.active?.id === workspace.id}
      {@const needsFolder = unbound(workspace)}
      <div class="tab" class:selected data-trouble={needsFolder ? "unbound" : "none"}>
        <button
          role="tab"
          id="workspace-tab-{workspace.id}"
          aria-selected={selected}
          aria-controls={panelId}
          aria-label={describe(workspace)}
          tabindex={selected ? 0 : -1}
          onclick={() => workspaces.select(workspace.id)}
          onkeydown={(event) => onKeydown(event, at)}
          {@attach (el) => register(workspace.id, el)}
        >
          {#if needsFolder}
            <span class="mark" aria-hidden="true"></span>
          {/if}
          <span class="name">{workspace.name}</span>
        </button>

        {#if workspaces.joined}
          <!--
            Pointer only, on purpose. The keyboard closes a tab with Delete,
            which the tab pattern already reserves for it; a focusable × here
            would double the number of stops in the strip to save nobody a
            keystroke. aria-hidden because the tab's own Delete is the whole
            of what a screen reader should be told about closing.

            Only when a workspace is joined: the one tab an unjoined machine
            has is not a tab anything could close, and a × that does nothing
            is worse than no ×.
          -->
          <button
            class="close"
            tabindex="-1"
            aria-hidden="true"
            title="Close {workspace.name}"
            onclick={() => askClose(workspace)}
          >
            ✕
          </button>
        {/if}
      </div>
    {/each}

    {#if workspaces.list.length === 0}
      <p class="none">No workspaces open.</p>
    {/if}
  </div>

  <div class="add">
    <!-- What "New" means depends on whether there is a workspace to put a tab
         in, and the word has to follow: a button labelled "New" that makes a
         workspace on a server when you wanted a tab is the same click with two
         meanings. -->
    <button onclick={onnew}>{workspaces.joined ? "New tab" : "New workspace"}</button>
    <!-- The way onto a server, kept on screen rather than behind having left
         the workspace you are in. A workspace that lives on this machine is
         the ordinary starting state now, so "how do I share this" is the
         ordinary next question, and it used to have no answer anywhere. Hidden
         when there is no transport to use, or when this workspace is already
         on a server. -->
    {#if workspaces.available && !workspaces.cloud}
      <button onclick={oncreate}>On a server…</button>
      <button onclick={onjoin}>Join</button>
    {:else if workspaces.cloud}
      <!-- The keys, from the strip as well as from the badge. Inviting somebody
           is a thing you do at the level of the workspace, which is what this
           bar is, and it is not a sync status. -->
      <button onclick={onkeys}>Keys…</button>
    {/if}
  </div>
</div>

{#if confirming}
  <dialog {@attach confirmDialog} onclose={() => (confirming = null)} aria-labelledby="close-title">
    <h2 id="close-title">Close {confirming.name} for everyone?</h2>
    <p>
      This tab belongs to the workspace, not to this machine. Closing it retires it for
      everybody who has joined, and there is no undo — the workspace has no way to bring
      a closed tab back.
    </p>
    <p>
      The notes you have written in it here stay on this machine.
    </p>
    <footer>
      <button class="ghost" onclick={() => (confirming = null)}>Keep it open</button>
      <button class="danger" onclick={() => confirming && close(confirming)}>
        Close it for everyone
      </button>
    </footer>
  </dialog>
{/if}

<style>
  .bar {
    display: flex;
    align-items: stretch;
    gap: 8px;
    min-height: 32px;
    padding: 0 8px;
    border-bottom: 1px solid var(--line);
    background: var(--panel);
  }

  .tabs {
    display: flex;
    flex: 1;
    min-width: 0;
    gap: 2px;
    overflow-x: auto;
    /* A tab bar that grows a scrollbar and shoves the content down is worse
       than one that hides it; the tabs still scroll, and the keyboard reaches
       them all regardless of what is on screen. */
    scrollbar-width: none;
  }

  .tab {
    display: flex;
    align-items: center;
    flex: none;
    max-width: 200px;
    border-bottom: 2px solid transparent;
  }

  .tab.selected {
    border-bottom-color: var(--accent);
    background: var(--panel-2);
  }

  [role="tab"] {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    padding: 5px 4px 5px 10px;
    border: none;
    background: none;
    color: var(--muted);
    font-size: 12px;
    cursor: pointer;
  }

  .tab.selected [role="tab"] {
    color: var(--text);
  }

  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* A tab with no folder on this machine, marked where you can see it without
     switching to it. Hollow rather than filled: it is something still to do,
     not something that went wrong. The label says which, for anyone who
     cannot see the difference between two six-pixel dots. */
  .mark {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: transparent;
    box-shadow: inset 0 0 0 1.5px var(--accent);
  }

  .close {
    padding: 0 7px 0 3px;
    border: none;
    background: none;
    color: var(--muted);
    font-size: 11px;
    line-height: 1;
    cursor: pointer;
    opacity: 0;
  }

  .tab:hover .close,
  .tab.selected .close {
    opacity: 1;
  }

  .close:hover {
    color: var(--err);
  }

  .none {
    align-self: center;
    margin: 0;
    color: var(--muted);
    font-size: 11px;
  }

  .add {
    display: flex;
    align-items: center;
    gap: 4px;
    flex: none;
  }

  .add button {
    padding: 3px 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  .add button:hover {
    border-color: #3a4250;
    color: var(--text);
  }

  dialog {
    width: min(420px, calc(100vw - 32px));
    padding: 16px;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    color: var(--text);
  }

  dialog::backdrop {
    background: rgb(0 0 0 / 0.5);
  }

  dialog h2 {
    margin: 0 0 8px;
    font-size: 14px;
  }

  dialog p {
    margin: 0 0 14px;
    color: var(--muted);
    line-height: 1.6;
  }

  dialog footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }

  .ghost,
  .danger {
    padding: 6px 14px;
    border-radius: 5px;
    font: inherit;
    cursor: pointer;
  }

  .ghost {
    border: 1px solid var(--line);
    background: var(--panel-2);
    color: var(--text);
  }

  .danger {
    border: 1px solid var(--err);
    background: #2a1a1a;
    color: var(--err);
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
