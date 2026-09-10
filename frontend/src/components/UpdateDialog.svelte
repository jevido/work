<script lang="ts">
  import { PanelDrag, dragHandle } from "../lib/ui/drag.svelte";
  import type { AppUpdate } from "../lib/update/update.svelte";

  let { update }: { update: AppUpdate } = $props();

  const drag = new PanelDrag();
  const titleId = $props.id();

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    event.preventDefault();
    // The console's own Escape cancels the run. Closing this is not a decision
    // to throw the work out, so it must not reach it.
    event.stopPropagation();
    // Escape puts the popup away for this session, the same as the ✕ -- it
    // does not decline the update, which is still on offer next launch.
    update.dismiss();
  }
</script>

<!-- Not modal, and no backdrop, for the same reason as the task and changes
     panels: this window opens on its own, in the middle of whatever was being
     read. Nothing behind it is disabled, a run carries on, and the panel can
     be dragged out of the way.

     Focus does land in here on mount, so Escape and Tab work without clicking
     first. It goes to the panel rather than to Update now: a popup that
     appears unasked must not put a button that restarts the app under the
     next Enter keypress. -->
<div
  class="dialog"
  role="dialog"
  aria-labelledby={titleId}
  tabindex="-1"
  style:transform={drag.transform}
  onkeydown={onKeydown}
  {@attach (node) => node.focus()}
>
  <header {@attach dragHandle(drag)}>
    <span class="title" id={titleId}>Update available</span>
    <button
      class="close"
      onclick={() => update.dismiss()}
      aria-label="Close"
      title="Close (Esc)"
    >
      ✕
    </button>
  </header>

  <div class="body">
    <p class="version">
      Version <strong>{update.version}</strong> is ready to install.
    </p>

    <!-- What the version number does not say. A button in the browser rather
         than a link: this window is the app, and following a link in it
         navigates the app away from itself. -->
    {#if update.releaseUrl}
      <button class="notes" onclick={() => update.openReleaseNotes()}>
        Release notes
      </button>
    {/if}

    {#if update.applied}
      <!-- The window stays up through the restart, because the restart is the
           part that needs explaining. -->
      <p class="applied" role="status">
        Installing — Work will restart to finish.
      </p>
    {/if}

    {#if update.error}
      <p class="error" role="alert">{update.error}</p>
    {/if}
  </div>

  <footer>
    <button
      class="ghost"
      onclick={() => update.dismiss()}
      disabled={update.applying || update.applied}
    >
      Later
    </button>
    <button
      class="primary"
      onclick={() => update.apply()}
      disabled={update.applying || update.applied}
    >
      {update.applying ? "Updating…" : "Update now"}
    </button>
  </footer>
</div>

<style>
  .dialog {
    /* Fixed, like the other panels: it opens over the office and the board
       rather than inside either of them. */
    position: fixed;
    /* Above the task and changes panels, which sit at 20 -- this one is the
       app talking about itself. */
    z-index: 30;
    --w: min(420px, calc(100vw - 32px));
    top: 14vh;
    left: 50%;
    margin-left: calc(var(--w) / -2);
    width: var(--w);
    display: flex;
    flex-direction: column;
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
    padding: 12px;
  }

  .version {
    margin: 0;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.55;
  }

  /* The version number is the one thing in here worth copying out. */
  .version strong {
    color: var(--text);
    font-weight: 600;
    user-select: text;
  }

  .notes {
    margin-top: 8px;
    padding: 0;
    border: none;
    background: none;
    color: var(--accent);
    font-size: 12.5px;
    text-decoration: underline;
    text-underline-offset: 2px;
    cursor: pointer;
  }

  .applied {
    margin: 10px 0 0;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.45;
  }

  .error {
    margin: 10px 0 0;
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
    line-height: 1.45;
    user-select: text;
  }

  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 10px 12px 11px;
    border-top: 1px solid var(--line);
    background: var(--panel-2);
  }

  .primary {
    padding: 6px 14px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
    cursor: pointer;
  }

  .primary:hover:not(:disabled) {
    filter: brightness(1.12);
  }

  .ghost {
    padding: 6px 12px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }

  .ghost:hover:not(:disabled) {
    color: var(--text);
  }

  .primary:disabled,
  .ghost:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
