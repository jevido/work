<script lang="ts">
  import type { Config } from "../lib/config/config.svelte";

  let { config }: { config: Config } = $props();

  let open = $state(false);
  let root: HTMLDivElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();

  function toggle() {
    open = !open;
    // Last reload's result belongs to last reload. Opening the menu fresh and
    // finding "Reloaded -- 4 agents" from ten minutes ago reads as something
    // that just happened.
    if (open) config.note = null;
  }

  function close(focusTrigger = true) {
    if (!open) return;
    open = false;
    if (focusTrigger) trigger?.focus();
  }

  async function reload() {
    await config.reload();
    // Deliberately stays open: reloading an unchanged folder looks identical,
    // and the only evidence it worked is the line this leaves behind.
  }

  async function changeFolder() {
    const changed = await config.choose();
    // Cancelling the picker is not a change, and must not touch the team --
    // the folder in use is still the folder in use.
    if (changed) await config.adopt();
  }

  // Escape closes the menu from the trigger and from either item, so it is
  // bound on both rather than on the wrapper around them -- a div listening
  // for keys is a div pretending to be a control.
  function onEscape(event: KeyboardEvent) {
    if (event.key !== "Escape" || !open) return;
    event.preventDefault();
    event.stopPropagation();
    close();
  }

  // Clicking anywhere else puts the menu away. Pointerdown rather than click so
  // it closes on the way down, before whatever was clicked reacts -- a desk
  // behind the menu should open its panel on the same press.
  $effect(() => {
    if (!open) return;
    const onDown = (event: PointerEvent) => {
      if (root && !root.contains(event.target as Node)) close(false);
    };
    window.addEventListener("pointerdown", onDown, true);
    return () => window.removeEventListener("pointerdown", onDown, true);
  });
</script>

<div class="settings" bind:this={root}>
  <button
    class="wrench"
    onkeydown={onEscape}
    class:on={open}
    bind:this={trigger}
    onclick={toggle}
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label="Settings"
    title="Settings"
  >
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M6.3 2.1a4.1 4.1 0 0 1 4.9 5.3l3.3 3.3a1 1 0 0 1 0 1.4l-1.1 1.1a1 1 0 0 1-1.4 0L8.7 9.9a4.1 4.1 0 0 1-5.3-4.9l1.9 1.9a1.2 1.2 0 0 0 1.7 0l.5-.5a1.2 1.2 0 0 0 0-1.7L6.3 2.1Z"
        fill="currentColor"
      />
    </svg>
  </button>

  {#if open}
    <!-- tabindex so the container itself can hold focus: the items are
         buttons and take it on their own, but Escape has to work the moment
         the menu appears, before anything in it has been tabbed to. -->
    <div
      class="menu"
      role="menu"
      aria-label="Settings"
      tabindex="-1"
      onkeydown={onEscape}
      {@attach (node) => node.focus()}
    >
      <!-- Which folder is about to be re-read, before the item that re-reads
           it. Reloading without being told what you are reloading is a guess. -->
      <div class="where">
        <span class="label">Config folder</span>
        <span class="path" title={config.path}>{config.path}</span>
      </div>

      <button role="menuitem" onclick={reload} disabled={config.busy}>
        {config.busy ? "Reloading…" : "Reload config"}
      </button>
      <button role="menuitem" onclick={changeFolder} disabled={config.busy}>
        Change folder location…
      </button>

      {#if config.error}
        <p class="hint err" role="alert">{config.error}</p>
      {:else if config.note}
        <p class="hint" role="status">{config.note}</p>
      {/if}
    </div>
  {/if}
</div>

<style>
  .settings {
    position: relative;
  }

  .wrench {
    display: flex;
    padding: 5px 6px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    color: var(--muted);
    cursor: pointer;
  }

  .wrench svg {
    width: 14px;
    height: 14px;
  }

  .wrench:hover {
    color: var(--text);
  }

  .wrench.on {
    color: var(--accent);
  }

  .menu {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 3;
    display: grid;
    width: 250px;
    padding: 6px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel);
    box-shadow: 0 10px 26px rgb(0 0 0 / 0.45);
  }

  .where {
    display: grid;
    gap: 2px;
    padding: 5px 7px 8px;
    margin-bottom: 4px;
    border-bottom: 1px solid var(--line);
  }

  .label {
    color: var(--muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  /* Wrapped rather than clipped.

     A one-line path has to lose an end, and the front is the useless end --
     but clipping it with `direction: rtl` moves the leading slash around to
     the back, so "/home/me/agents" reads as "…me/agents/" and looks like it
     ends in a separator. Two or three short lines of a folder you can actually
     read beat one line of a folder that might be the wrong one. */
  .path {
    font-family: ui-monospace, monospace;
    font-size: 11px;
    line-height: 1.4;
    word-break: break-all;
    user-select: text;
  }

  button[role="menuitem"] {
    padding: 6px 7px;
    border: none;
    border-radius: 5px;
    background: none;
    color: var(--text);
    font: inherit;
    font-size: 12.5px;
    text-align: left;
    cursor: pointer;
  }

  button[role="menuitem"]:hover:not(:disabled) {
    background: var(--panel-2);
  }

  button[role="menuitem"]:disabled {
    color: var(--muted);
    cursor: default;
  }

  .hint {
    margin: 6px 7px 3px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .hint.err {
    color: var(--err);
    user-select: text;
  }
</style>
