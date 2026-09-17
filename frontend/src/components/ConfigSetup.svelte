<script lang="ts">
  import type { Config } from "../lib/config/config.svelte";
  import type { Workspaces } from "../lib/workspace/workspaces.svelte";
  import WorkspaceDialog, { type Purpose } from "./WorkspaceDialog.svelte";

  let { config, workspaces }: { config: Config; workspaces: Workspaces } = $props();

  /**
   * Setup is two questions, and this is which one is on screen.
   *
   * "folder" is where the team lives, which the app cannot run without.
   * "workspace" is where the work lives, which it used to run without and
   * which is the reason a fresh install could open, show one tab, and refuse
   * every single thing a tab can do -- a new tab, a folder for it, a plan the
   * outline could write to. A workspace is not an advanced feature you
   * graduate to; it is the thing the app is, so setup makes one.
   */
  let step = $state<"folder" | "workspace">("folder");

  /** The cloud dialog, when one is open over this screen. */
  let dialog = $state<Purpose | null>(null);

  /** True once a folder has been picked but not yet accepted. */
  const picked = $derived(config.path !== null);

  /**
   * What the local workspace gets called.
   *
   * The folder's own name, because it is already the name of the thing being
   * worked on and asking a second question to arrive at the same answer is a
   * question not worth asking. It is renameable later like any other.
   */
  const suggested = $derived(config.path?.split(/[\\/]/).filter(Boolean).pop() || "Workspace");

  async function choose() {
    await config.choose();
  }

  /** Accepts the folder and asks the second question. */
  function accept() {
    if (config.path) step = "workspace";
  }

  /** Makes the workspace that stays here, and opens the app. */
  async function alone() {
    if (await workspaces.createLocal(suggested)) config.confirm();
  }

  /**
   * Lets the app through once the dialog has actually produced a workspace.
   *
   * Cancelling it must not: someone who backed out of joining is still in
   * setup and still has no workspace, and dropping them into the app would
   * leave them exactly where this screen exists to stop them being.
   */
  function closeDialog() {
    dialog = null;
    if (workspaces.joined) config.confirm();
  }

  function onKeydown(event: KeyboardEvent) {
    // The whole screen has one obvious next action, so Enter should do it
    // wherever the caret happens to be. Not while a dialog is over it: that
    // has its own submit, and the platform gives it the keyboard.
    if (event.key === "Enter" && !config.busy && dialog === null) {
      event.preventDefault();
      if (step === "workspace") void alone();
      else if (picked) accept();
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
    {#if step === "folder"}
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
        <!-- Setup leaves two agents behind, so say who they are and where the
             rest come from: the next step is a mkdir, not another screen. -->
        <p class="aside">
          You start with Anton, who routes work, and Jared, who thinks with you in
          Idea and Planning. Add a colleague by making a folder next to theirs, then
          Reload config from the wrench.
        </p>
      {:else}
        <p>
          Your team is a folder on disk: one folder per agent, each with a
          <code>PERSONALITY.md</code>, a <code>skills/</code> folder and
          optionally an avatar.
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
            onclick={accept}
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
    {:else}
      <h1 id="setup-title">And where does the work go?</h1>
      <p>
        A workspace holds the tabs, the outline and the plan. One is made for you
        either way — this only decides whether anybody else can see it.
      </p>

      <!--
        Three buttons rather than a form.

        Each of these asks for something different underneath — a name, a
        server, a key — so a single form would have two thirds of it wrong at
        any moment. The answer that needs nothing is first and is focused, so
        the common case is one keystroke and the other two are one click.
      -->
      <ul class="choices">
        <li>
          <button
            class="choice"
            onclick={alone}
            disabled={workspaces.busy}
            {@attach (node) => node.focus()}
          >
            <span class="lead">Just on this machine</span>
            <span class="detail">
              Nothing leaves this computer. Tabs, the outline and the plan all work.
            </span>
          </button>
        </li>
        <li>
          <button
            class="choice"
            onclick={() => (dialog = "create")}
            disabled={workspaces.busy}
          >
            <span class="lead">On a server, so a team can share it</span>
            <span class="detail">
              Makes the workspace and gives you a link to invite people with.
            </span>
          </button>
        </li>
        <li>
          <button
            class="choice"
            onclick={() => (dialog = "join")}
            disabled={workspaces.busy}
          >
            <span class="lead">Join one somebody sent me</span>
            <span class="detail">Paste the key or the link you were given.</span>
          </button>
        </li>
      </ul>

      {#if workspaces.error}
        <p class="error" role="alert">{workspaces.error}</p>
      {/if}

      <div class="actions">
        <button class="ghost" onclick={() => (step = "folder")} disabled={workspaces.busy}>
          Back
        </button>
      </div>
    {/if}
  </div>
</div>

{#if dialog}
  <!-- The same dialog the running app uses. Setup asking for a server and a key
       in its own words would be a second copy of the error handling, the
       read-key warning and the keys panel, kept in step with the first by
       hand. -->
  <WorkspaceDialog purpose={dialog} {workspaces} onclose={closeDialog} />
{/if}

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

  .choices {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* A button per answer, sized like a row rather than like a button: each one
     carries a second line explaining what it does, and a control that is two
     lines tall reads as a choice in a list rather than as a submit. */
  .choice {
    display: flex;
    flex-direction: column;
    gap: 3px;
    width: 100%;
    padding: 11px 13px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel-2);
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }

  .choice:hover:not(:disabled) {
    border-color: var(--accent);
  }

  .choice:disabled {
    opacity: 0.45;
    cursor: default;
  }

  .lead {
    font-size: 13px;
    font-weight: 600;
  }

  .detail {
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
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
