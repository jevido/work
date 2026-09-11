<script lang="ts">
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import type { Workspaces } from "../lib/workspace/workspaces.svelte";

  let {
    workspaces,
    /** The id of the region the tabs control, for aria-controls. */
    panelId,
    onnew,
    onjoin,
  }: {
    workspaces: Workspaces;
    panelId: string;
    onnew: () => void;
    onjoin: () => void;
  } = $props();

  /** The tab being renamed, if any. */
  let renaming = $state<string | null>(null);
  /** The tab a close would strand unsent changes in. */
  let confirming = $state<Workspace | null>(null);

  const tabEls = new Map<string, HTMLElement>();

  function register(id: string, el: HTMLElement) {
    tabEls.set(id, el);
    return () => {
      if (tabEls.get(id) === el) tabEls.delete(id);
    };
  }

  /**
   * Whether a tab has something wrong with it.
   *
   * A tab bar exists so the things behind the other tabs keep going, which
   * means the other tabs are exactly where a problem will happen unwatched.
   * The mark is on the tab and the sentence is on the badge: a dot is enough
   * to make somebody look, and not enough to say what happened.
   */
  function trouble(workspace: Workspace): "none" | "waiting" | "refused" {
    // A workspace that was never shared cannot be behind: there is nowhere
    // for it to be behind. Marking one would put a warning dot on every tab
    // of an app nobody has connected to a server.
    if (!workspace.sync.shared) return "none";
    const state = workspace.sync.state;
    if (state === "rejected") return "refused";
    if (state === "offline" || workspace.sync.pending > 0) return "waiting";
    return "none";
  }

  /** What a tab is announced as, which is more than what it is labelled with. */
  function describe(workspace: Workspace): string {
    switch (trouble(workspace)) {
      case "refused":
        return `${workspace.name}, key refused`;
      case "waiting": {
        const n = workspace.sync.pending;
        return n === 0
          ? `${workspace.name}, offline`
          : `${workspace.name}, ${n} unsaved ${n === 1 ? "change" : "changes"}`;
      }
      default:
        return workspace.name;
    }
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
      case "F2":
        event.preventDefault();
        renaming = list[at].id;
        return;
      default:
        return;
    }
    event.preventDefault();
    const next = list[target];
    if (!next) return;
    // Selection follows focus, which is right when switching is instant and
    // cheap: every tab is already live and already caught up.
    workspaces.select(next.id);
    tabEls.get(next.id)?.focus();
  }

  function askClose(workspace: Workspace) {
    if (workspaces.unsentIn(workspace.id) > 0) {
      confirming = workspace;
      return;
    }
    close(workspace);
  }

  function close(workspace: Workspace) {
    const list = workspaces.list;
    const at = list.findIndex((w) => w.id === workspace.id);
    confirming = null;
    workspaces.close(workspace.id);
    // Focus the tab that took its place, or the strip's new first tab.
    const next = workspaces.list[at] ?? workspaces.list[at - 1] ?? null;
    if (next) queueMicrotask(() => tabEls.get(next.id)?.focus());
  }

  function commitRename(id: string, value: string) {
    const name = value.trim();
    if (name !== "") workspaces.rename(id, name);
    renaming = null;
    queueMicrotask(() => tabEls.get(id)?.focus());
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
      {@const mark = trouble(workspace)}
      <div class="tab" class:selected data-trouble={mark}>
        {#if renaming === workspace.id}
          <!-- The rename replaces the tab rather than opening over it: the tab
               is the label, and a dialog to change a label is a dialog to
               change one word. -->
          <input
            class="rename"
            value={workspace.name}
            aria-label="Rename {workspace.name}"
            {@attach (el) => {
              el.focus();
              el.select();
            }}
            onblur={(event) => commitRename(workspace.id, event.currentTarget.value)}
            onkeydown={(event) => {
              if (event.key === "Enter") commitRename(workspace.id, event.currentTarget.value);
              if (event.key === "Escape") {
                renaming = null;
                queueMicrotask(() => tabEls.get(workspace.id)?.focus());
              }
            }}
          />
        {:else}
          <button
            role="tab"
            id="workspace-tab-{workspace.id}"
            aria-selected={selected}
            aria-controls={panelId}
            aria-label={describe(workspace)}
            tabindex={selected ? 0 : -1}
            onclick={() => workspaces.select(workspace.id)}
            ondblclick={() => (renaming = workspace.id)}
            onkeydown={(event) => onKeydown(event, at)}
            {@attach (el) => register(workspace.id, el)}
          >
            {#if mark !== "none"}
              <span class="mark" aria-hidden="true"></span>
            {/if}
            <span class="name">{workspace.name}</span>
          </button>

          <!--
            Pointer only, on purpose. The keyboard closes a tab with Delete,
            which the tab pattern already reserves for it; a focusable × here
            would double the number of stops in the strip to save nobody a
            keystroke. aria-hidden because the tab's own Delete is the whole
            of what a screen reader should be told about closing.
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
    <button onclick={onnew}>New</button>
    <button onclick={onjoin}>Join</button>
  </div>
</div>

{#if confirming}
  {@const unsent = workspaces.unsentIn(confirming.id)}
  <dialog {@attach confirmDialog} onclose={() => (confirming = null)} aria-labelledby="close-title">
    <h2 id="close-title">Close {confirming.name}?</h2>
    <p>
      {unsent} {unsent === 1 ? "change has" : "changes have"} not reached the server yet.
      Closing this tab forgets {unsent === 1 ? "it" : "them"} — this machine is the only
      place {unsent === 1 ? "it exists" : "they exist"}.
    </p>
    <footer>
      <button class="ghost" onclick={() => (confirming = null)}>Keep it open</button>
      <button class="danger" onclick={() => confirming && close(confirming)}>
        Close and lose {unsent === 1 ? "it" : "them"}
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

  /* A workspace that is not keeping up, marked where you can see it without
     switching to it. The colour says which kind; the badge on the tab you are
     in says the rest. */
  .mark {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
  }

  .tab[data-trouble="refused"] .mark {
    background: var(--err);
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

  .rename {
    width: 160px;
    margin: 3px 4px;
    padding: 2px 6px;
    border: 1px solid var(--accent);
    border-radius: 4px;
    background: var(--bg);
    color: inherit;
    font: inherit;
    font-size: 12px;
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
