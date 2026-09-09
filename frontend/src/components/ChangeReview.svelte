<script lang="ts">
  import { untrack } from "svelte";
  import type { ChangeReview } from "../lib/changes/changes.svelte";
  import { PanelDrag, dragHandle } from "../lib/ui/drag.svelte";
  import DiffView from "./DiffView.svelte";

  let { review, onclose }: { review: ChangeReview; onclose: () => void } = $props();

  const drag = new PanelDrag();

  /**
   * Which file's diff is showing.
   *
   * Read once, as the panel opens: this window exists to be read, so it opens
   * on a diff rather than on a list of rows that all have to be clicked before
   * anything can be seen. It stays a list with one open row -- the diff gets
   * the panel's whole width that way, and Revert stays on the row it reverts
   * rather than moving into a detail pane a file has to be selected in first.
   *
   * Untracked deliberately: which row is open is the reader's, not the list's.
   * A run that changes another file while this is open must not move the diff
   * being read out from under it.
   */
  let openPath = $state<string | null>(untrack(() => review.files[0]?.path ?? null));

  function toggle(path: string) {
    openPath = openPath === path ? null : path;
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    event.preventDefault();
    // The console's own Escape cancels the run. Putting a window away is not a
    // decision to throw the work out, so this must not reach it.
    event.stopPropagation();
    onclose();
  }
</script>

<!-- Reading a diff and following a conversation are two different jobs: one
     wants width and a fixed frame to scan, the other wants to stay where it
     was. This used to be a block inside the conversation, which gave the diff
     a 340px column and took half the transcript with it.

     Same shape as the task panel: fixed, centred, dragged by its header, no
     backdrop. Nothing behind it is disabled -- a run carries on, and the list
     in here is republished as it does. -->
<div
  class="dialog"
  role="dialog"
  aria-label="Changes on disk"
  tabindex="-1"
  style:transform={drag.transform}
  onkeydown={onKeydown}
  {@attach (node) => node.focus()}
>
  <header {@attach dragHandle(drag)}>
    <span class="title">Changes on disk</span>
    <span class="count">{review.files.length}</span>
    <button class="close" onclick={onclose} aria-label="Close" title="Close (Esc)">✕</button>
  </header>

  <div class="body">
    {#each review.files as file (file.path)}
      <div class="file">
        <div class="row" class:reading={openPath === file.path}>
          <button class="open" onclick={() => toggle(file.path)} aria-expanded={openPath === file.path}>
            <span class="chevron" class:open={openPath === file.path}>▸</span>
            <span class="status" data-status={file.status}>{file.status}</span>
            <span class="path">{file.path}</span>
            {#if !file.binary}
              <span class="stat">
                <span class="added">+{file.added}</span><span class="removed">−{file.removed}</span>
              </span>
            {/if}
          </button>
          <button
            class="revert"
            onclick={() => review.revert(file.path)}
            disabled={file.reverting || !file.restorable}
            title={file.restorable
              ? "Restore this file to what it was before the run"
              : "Work did not record enough to restore this file exactly"}
          >
            {file.reverting ? "Reverting…" : "Revert"}
          </button>
        </div>

        {#if file.error}
          <div class="error" role="alert">{file.error}</div>
        {/if}

        {#if openPath === file.path}
          {#if file.binary}
            <p class="binary">Binary file — no diff to show.</p>
          {:else if file.diff.length === 0}
            <p class="binary">No textual diff available.</p>
          {:else}
            <!-- Uncapped: the window is the scroller, and a diff with its own
                 scrollbar inside one that also scrolls means the wheel does
                 different things a few pixels apart. -->
            <div class="diff"><DiffView lines={file.diff} capped={false} /></div>
          {/if}
        {/if}
      </div>
    {/each}

    <!-- Reverting the last file empties the list under you. The panel stays and
         says so rather than vanishing: it disappearing at the moment you
         pressed something reads as a crash, not as a job finished. -->
    {#if review.isEmpty}
      <p class="empty">
        Nothing to review — every file the run touched is back the way it was.
      </p>
    {/if}
  </div>
</div>

<style>
  .dialog {
    /* Fixed rather than absolute: it is opened from the console, which is the
       narrowest column on screen and the one thing a diff must not be squeezed
       into again. */
    position: fixed;
    z-index: 20;
    --w: min(780px, calc(100vw - 32px));
    top: 8vh;
    left: 50%;
    margin-left: calc(var(--w) / -2);
    width: var(--w);
    display: flex;
    flex-direction: column;
    max-height: min(80vh, 640px);
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

  .title {
    font-weight: 600;
  }

  .count {
    padding: 1px 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 10.5px;
    font-variant-numeric: tabular-nums;
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
  }

  .file + .file {
    border-top: 1px solid var(--line);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding-right: 9px;
  }

  /* The row of the file being read stays at the top of the list while its diff
     scrolls under it.

     The one thing about this list that did not survive being put in a window: a
     diff is routinely taller than 640px, and scrolling one carried its filename
     off the top, leaving a wall of hunks and a header that says "Changes on
     disk" rather than which change. Sticking to the scroll container means it
     gives way to the next file rather than piling up, and it needs its own
     background or the diff shows through it. */
  .row.reading {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--panel);
  }

  .open {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    padding: 7px 12px;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    font-size: 12px;
  }

  .open:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  .chevron {
    flex: none;
    color: var(--muted);
    font-size: 9px;
    transition: transform 120ms ease;
  }

  .chevron.open {
    transform: rotate(90deg);
  }

  @media (prefers-reduced-motion: reduce) {
    .chevron {
      transition: none;
    }
  }

  /* The word, not just a colour, so the state survives a screenshot and a
     colour-blind reader. */
  .status {
    flex: none;
    width: 4.6em;
    color: var(--muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .status[data-status="added"] {
    color: var(--ok);
  }

  .status[data-status="deleted"] {
    color: var(--err);
  }

  .path {
    flex: 1;
    min-width: 0;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    direction: rtl;
    text-align: left;
    user-select: text;
  }

  .stat {
    flex: none;
    display: flex;
    gap: 5px;
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 11px;
  }

  .added {
    color: var(--ok);
  }

  .removed {
    color: var(--err);
  }

  .revert {
    flex: none;
    padding: 3px 9px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel-2);
    cursor: pointer;
    font-size: 11px;
  }

  .revert:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .revert:hover:not(:disabled) {
    border-color: var(--err);
    color: var(--err);
  }

  .diff {
    padding: 0 12px 10px;
  }

  .binary {
    margin: 0;
    padding: 0 12px 10px;
    color: var(--muted);
    font-size: 12px;
  }

  .error {
    margin: 0 12px 8px;
    padding: 6px 8px;
    border: 1px solid #4a2b2b;
    border-radius: 5px;
    background: #241a1a;
    color: var(--err);
    font-size: 11.5px;
    user-select: text;
  }

  .empty {
    margin: 0;
    padding: 14px 12px;
    color: var(--muted);
    font-size: 12px;
  }
</style>
