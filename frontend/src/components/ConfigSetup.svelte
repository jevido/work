<script lang="ts">
  import type { Config } from "../lib/config/config.svelte";

  let { config }: { config: Config } = $props();

  /** True once a folder has been picked but not yet accepted. */
  const picked = $derived(config.path !== null);

  async function choose() {
    await config.choose();
  }

  function onKeydown(event: KeyboardEvent) {
    // The whole screen has one obvious next action, so Enter should do it
    // wherever the caret happens to be.
    if (event.key === "Enter" && !config.busy) {
      event.preventDefault();
      if (picked) config.confirm();
      else void choose();
    }
  }
</script>

<!-- Takes the window rather than sitting over the app.

     There is no app to sit over: with no config folder there are no agents, so
     no desks, so the office behind a modal would be an empty room implying the
     team is missing rather than unconfigured. This is the first-run screen, and
     it is the only thing on screen until a folder is accepted. -->
<div
  class="setup"
  role="dialog"
  aria-modal="true"
  aria-labelledby="setup-title"
  tabindex="-1"
  onkeydown={onKeydown}
>
  <div class="card">
    <h1 id="setup-title">Where does your team live?</h1>

    {#if picked}
      <p>
        Read from this folder. Each agent is a folder inside it, holding a
        <code>PERSONALITY.md</code>, a <code>skills/</code> folder and
        optionally an avatar.
      </p>
      <!-- The path is the whole decision, so it is the biggest thing here and
           selectable: a mistyped or wrong-level folder is the one failure this
           screen exists to catch, and it is caught by reading it. -->
      <p class="path" aria-label="Chosen folder">{config.path}</p>
      <!-- Setup leaves one agent behind, so say where the rest come from:
           the next step is a mkdir, not another screen. -->
      <p class="aside">
        You start with Anton alone. Add a colleague by making a folder next to
        his, then Reload config from the wrench.
      </p>
    {:else}
      <p>
        Your team is a folder on disk: one folder per agent, each with a
        <code>PERSONALITY.md</code>, a <code>skills/</code> folder and
        optionally an avatar. You edit them in your editor -- work reads them.
      </p>
    {/if}

    {#if config.error}
      <p class="error" role="alert">{config.error}</p>
    {/if}

    <div class="actions">
      {#if picked}
        <button class="ghost" onclick={choose} disabled={config.busy}>
          Choose another…
        </button>
        <button
          class="primary"
          onclick={() => config.confirm()}
          disabled={config.busy}
          {@attach (node) => node.focus()}
        >
          Use this folder
        </button>
      {:else}
        <button
          class="primary"
          onclick={choose}
          disabled={config.busy}
          {@attach (node) => node.focus()}
        >
          {config.busy ? "Choosing…" : "Choose folder…"}
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
  .setup {
    display: grid;
    place-items: center;
    height: 100%;
    padding: 24px;
    background: #0b0d11;
  }

  .card {
    width: min(520px, 100%);
    padding: 22px 24px 18px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--panel);
  }

  h1 {
    margin: 0 0 10px;
    font-size: 17px;
    font-weight: 600;
  }

  p {
    margin: 0 0 12px;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.55;
  }

  /* Unpadded on purpose: a filename in the middle of a sentence is followed by
     a comma as often as not, and box padding puts a visible gap in front of
     it. The monospace is enough to mark it as a name. */
  code {
    color: var(--text);
    font-family: ui-monospace, monospace;
    font-size: 0.92em;
  }

  /* Not in a box.

     A bordered, filled path reads as a text field, and this one cannot be
     typed into -- the picker is the only way to change it. Plain, larger and
     in the foreground colour, it reads as the answer to the heading. */
  .path {
    color: var(--text);
    font-family: ui-monospace, monospace;
    font-size: 13px;
    word-break: break-all;
    /* The shell disables selection like an app frame; a path is there to be
       read closely and copied. */
    user-select: text;
  }

  .aside {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .error {
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    user-select: text;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }

  button.primary {
    padding: 6px 14px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
    cursor: pointer;
  }

  button.ghost {
    padding: 6px 12px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }

  button.ghost:hover:not(:disabled) {
    color: var(--text);
  }

  button.primary:hover:not(:disabled) {
    filter: brightness(1.12);
  }

  button.primary:disabled,
  button.ghost:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
