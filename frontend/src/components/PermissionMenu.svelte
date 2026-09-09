<script lang="ts">
  import type { PermissionMode } from "../../bindings/dev.jevido/work/internal/claude/models.js";
  import type { Permissions } from "../lib/permissions/permissions.svelte";

  let { permissions }: { permissions: Permissions } = $props();

  let open = $state(false);
  let root: HTMLDivElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();

  /**
   * The dangerous mode, while it is waiting to be confirmed.
   *
   * Turning every check off is one click away from a row of ordinary menu
   * items, and a menu that does it on the way past is not a choice anybody
   * made. So that one asks again, in place, and the menu says what it is about
   * to allow before it allows it.
   */
  let confirming = $state<PermissionMode | null>(null);

  const choice = $derived(permissions.current);

  function toggle() {
    open = !open;
    if (!open) confirming = null;
  }

  function close(focusTrigger = true) {
    if (!open) return;
    open = false;
    confirming = null;
    if (focusTrigger) trigger?.focus();
  }

  async function pick(mode: PermissionMode, dangerous: boolean) {
    if (dangerous && confirming !== mode) {
      confirming = mode;
      return;
    }
    confirming = null;
    await permissions.set(mode);
    // Left open on a failure: the error line under the list is the only thing
    // that says the mode did not change, and closing the menu would take it
    // away in the same gesture.
    if (!permissions.error) close();
  }

  // Escape closes from the trigger and from any item, so it is bound on the
  // controls rather than on the wrapper around them. A pending confirmation is
  // dropped first: the first Escape means "not that", not "not this menu".
  function onEscape(event: KeyboardEvent) {
    if (event.key !== "Escape" || !open) return;
    event.preventDefault();
    event.stopPropagation();
    if (confirming) {
      confirming = null;
      return;
    }
    close();
  }

  // Clicking anywhere else puts the menu away, on the way down, so a desk
  // behind it opens its panel on the same press.
  $effect(() => {
    if (!open) return;
    const onDown = (event: PointerEvent) => {
      if (root && !root.contains(event.target as Node)) close(false);
    };
    window.addEventListener("pointerdown", onDown, true);
    return () => window.removeEventListener("pointerdown", onDown, true);
  });
</script>

<!-- What the agents may do, in the corner they are watched from.

     It carries its mode on its face rather than behind an icon: this is the
     one setting in Work that decides whether a run can change the project, and
     a person about to give a task to somebody should not have to open a menu
     to find out which. -->
<div class="permissions" bind:this={root}>
  <button
    class="current"
    class:danger={choice?.dangerous}
    class:on={open}
    bind:this={trigger}
    onclick={toggle}
    onkeydown={onEscape}
    aria-haspopup="menu"
    aria-expanded={open}
    title={choice ? `${choice.label} — ${choice.detail}` : "What agents are allowed to do"}
  >
    <span class="what">Agents</span>
    <!-- Until the backend has answered there is no honest label to show, and
         guessing one would be a claim about what a run is allowed to do. -->
    <span class="mode">{choice?.label ?? "…"}</span>
    {#if choice?.dangerous && !permissions.bypassAccepted}
      <!-- The mode is on but the CLI is ignoring it. Marked on the closed
           button, because that is the state somebody is about to rely on. -->
      <span class="warn" aria-hidden="true">!</span>
    {/if}
  </button>

  {#if open}
    <div
      class="menu"
      role="menu"
      aria-label="What agents are allowed to do"
      tabindex="-1"
      onkeydown={onEscape}
      {@attach (node) => node.focus()}
    >
      {#each permissions.choices as item (item.id)}
        {@const active = item.id === permissions.mode}
        <button
          role="menuitemradio"
          aria-checked={active}
          class:active
          class:danger={item.dangerous}
          disabled={permissions.busy}
          onclick={() => pick(item.id, item.dangerous ?? false)}
        >
          <span class="row">
            <span class="tick" aria-hidden="true">{active ? "●" : "○"}</span>
            <span class="label">{item.label}</span>
          </span>
          <span class="detail">{item.detail}</span>

          {#if confirming === item.id}
            <!-- In place rather than in a dialog: the sentence that matters is
                 the detail line directly above, and a modal would cover it. -->
            <span class="confirm">Click again to turn the checks off.</span>
          {/if}

          {#if item.dangerous && !permissions.bypassAccepted}
            <!-- Said here rather than only after it fails: the CLI drops this
                 mode when its disclaimer has not been accepted, and it does it
                 without telling anybody. -->
            <span class="detail note">
              Your Claude CLI has not accepted this yet, so it will ignore it.
              Run <code>{permissions.acceptCommand}</code> once in a terminal.
            </span>
          {/if}
        </button>
      {/each}

      {#if permissions.error}
        <p class="hint err" role="alert">{permissions.error}</p>
      {:else}
        <p class="hint">Applies to Anton and every specialist, from the next task.</p>
      {/if}
    </div>
  {/if}
</div>

<style>
  .permissions {
    position: relative;
  }

  /* Reads as a status first and a control second, which is the order it is
     used in: the mode is glanced at far more often than it is changed. */
  .current {
    display: flex;
    align-items: baseline;
    gap: 5px;
    padding: 4px 8px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    color: var(--text);
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
  }

  .current:hover,
  .current.on {
    border-color: var(--accent);
  }

  .what {
    color: var(--muted);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .mode {
    font-weight: 600;
  }

  /* The one mode that removes a check looks different with the menu shut. A
     setting this consequential should not be discoverable only by opening
     something. */
  .current.danger {
    border-color: var(--err);
  }

  .current.danger .mode {
    color: var(--err);
  }

  .warn {
    color: var(--err);
    font-weight: 700;
  }

  .menu {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 3;
    display: grid;
    gap: 2px;
    width: 290px;
    padding: 6px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel);
    box-shadow: 0 10px 26px rgb(0 0 0 / 0.45);
  }

  button[role="menuitemradio"] {
    display: grid;
    gap: 2px;
    padding: 6px 7px;
    border: none;
    border-radius: 5px;
    background: none;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }

  button[role="menuitemradio"]:hover:not(:disabled) {
    background: var(--panel-2);
  }

  button[role="menuitemradio"]:disabled {
    cursor: default;
    opacity: 0.6;
  }

  button[role="menuitemradio"].active {
    background: var(--panel-2);
  }

  .row {
    display: flex;
    align-items: baseline;
    gap: 6px;
  }

  .tick {
    color: var(--accent);
    font-size: 10px;
  }

  .label {
    font-size: 12.5px;
    font-weight: 600;
  }

  button.danger .label {
    color: var(--err);
  }

  /* Indented to the label rather than the bullet, so the two lines read as one
     item instead of as a list of four things. */
  .detail {
    padding-left: 18px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
  }

  .note {
    margin-top: 3px;
    color: var(--err);
  }

  .confirm {
    padding-left: 18px;
    color: var(--err);
    font-size: 11px;
    font-weight: 600;
    line-height: 1.4;
  }

  code {
    font-family: ui-monospace, monospace;
    font-size: 0.95em;
    user-select: text;
  }

  .hint {
    margin: 5px 7px 3px;
    padding-top: 5px;
    border-top: 1px solid var(--line);
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .hint.err {
    color: var(--err);
    user-select: text;
  }
</style>
