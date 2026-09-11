<script lang="ts">
  import { looksLikeReadKey } from "../lib/workspace/transport";
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import type { Workspaces } from "../lib/workspace/workspaces.svelte";

  /**
   * Which of the four things this dialog is for.
   *
   * One component rather than four, because they are the same three fields in
   * different combinations and four dialogs would be four places to get the
   * error handling and the focus return slightly different from each other.
   */
  export type Purpose = "new" | "join" | "share" | "rekey";

  let {
    purpose,
    workspaces,
    /** The workspace being shared or re-keyed. Absent for "new" and "join". */
    target = null,
    onclose,
  }: {
    purpose: Purpose;
    workspaces: Workspaces;
    target?: Workspace | null;
    onclose: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  // Seeds, not bindings. The dialog is mounted fresh each time it opens and
  // these are then the person's to edit -- a name that snapped back to the
  // workspace's while it was being typed would be a form fighting its user.
  let name = $state(target?.name ?? "");
  // svelte-ignore state_referenced_locally
  let server = $state(target?.base ?? workspaces.lastServer);
  let signupToken = $state("");
  let invite = $state("");
  /** "new" only: whether to make it on a server at all. */
  let shared = $state(false);

  /** The read key, once there is one. The only moment the server will show it. */
  let readKey = $state<string | null>(null);
  let copied = $state(false);

  const TITLES: Record<Purpose, string> = {
    new: "New workspace",
    join: "Join a workspace",
    share: "Share this workspace",
    rekey: "Use a different key",
  };

  /**
   * A read key pasted where a write key belongs.
   *
   * Warned about rather than blocked: the server decides, and it is the only
   * thing that actually knows. But a read key here produces a workspace that
   * reads fine and silently refuses every edit, and "rk_" is enough to say so
   * before somebody spends ten minutes typing into it.
   */
  const readKeyWarning = $derived(
    (purpose === "join" || purpose === "rekey") && looksLikeReadKey(invite.trim()),
  );

  const canSubmit = $derived.by(() => {
    if (workspaces.busy) return false;
    switch (purpose) {
      case "new":
        return name.trim() !== "" && (!shared || server.trim() !== "");
      case "join":
      case "rekey":
        return invite.trim() !== "";
      case "share":
        return server.trim() !== "";
    }
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!canSubmit) return;

    switch (purpose) {
      case "new": {
        if (!shared) {
          workspaces.createLocal(name);
          onclose();
          return;
        }
        const made = await workspaces.createShared(server.trim(), signupToken.trim(), name);
        // The dialog stays open on success, because it is now holding the one
        // copy of the read key that will ever exist.
        if (made) readKey = made.readKey;
        return;
      }
      case "join":
        if (await workspaces.join(invite, server.trim())) onclose();
        return;
      case "rekey":
        if (target && (await workspaces.rekey(target, invite, server.trim()))) onclose();
        return;
      case "share": {
        if (!target) return;
        const key = await workspaces.share(target, server.trim(), signupToken.trim());
        if (key !== null) readKey = key;
        return;
      }
    }
  }

  async function copy() {
    if (!readKey) return;
    try {
      await navigator.clipboard.writeText(viewerLink);
      copied = true;
    } catch {
      // No clipboard permission. The field is selectable, which is what the
      // button was a shortcut for.
      copied = false;
    }
  }

  /** What you actually send somebody: the viewer, with the read key in the fragment. */
  const viewerLink = $derived(`${server.trim().replace(/\/+$/, "")}/#k=${readKey ?? ""}`);

  /**
   * Native <dialog>, opened modally.
   *
   * That gets the focus trap, the Escape key, the backdrop and the inertness
   * of everything behind it from the platform. Every one of those is a thing
   * hand-rolled modals get subtly wrong, and none of them is worth writing
   * again here.
   */
  function open(node: HTMLDialogElement) {
    node.showModal();
    return () => node.close();
  }
</script>

<dialog {@attach open} onclose={onclose} aria-labelledby="workspace-dialog-title">
  <form onsubmit={submit}>
    <h2 id="workspace-dialog-title">{TITLES[purpose]}</h2>

    {#if readKey !== null}
      <!-- Done, and now holding something that cannot be recovered. -->
      <p class="lede">
        {purpose === "share" ? "Shared." : "Created."} This is the only time the server will
        show the read key, so take it now.
      </p>

      <label>
        <span>Link for a read-only viewer</span>
        <input type="text" value={viewerLink} readonly onfocus={(e) => e.currentTarget.select()} />
      </label>
      <p class="note">
        Anyone with this link can read the outline and the plan, and change nothing.
      </p>

      <footer>
        <button type="button" class="ghost" onclick={copy}>
          {copied ? "Copied" : "Copy link"}
        </button>
        <button type="button" class="primary" onclick={onclose}>Done</button>
      </footer>
    {:else}
      {#if purpose === "new"}
        <label>
          <span>Name</span>
          <!-- No autofocus attribute: showModal() already puts the caret in
               the first focusable thing in the dialog, which is this. -->
          <input bind:value={name} autocomplete="off" required />
        </label>

        <fieldset>
          <legend>Where it lives</legend>
          <label class="choice">
            <input type="radio" name="where" checked={!shared} onchange={() => (shared = false)} />
            <span>
              <strong>Just on this machine</strong>
              <em>No server, no keys. You can share it later without losing anything.</em>
            </span>
          </label>
          <label class="choice">
            <input type="radio" name="where" checked={shared} onchange={() => (shared = true)} />
            <span>
              <strong>On a server</strong>
              <em>Syncs across your machines, and can be given a read-only link.</em>
            </span>
          </label>
        </fieldset>
      {/if}

      {#if purpose === "join" || purpose === "rekey"}
        <label>
          <span>Write key, or a link you were sent</span>
          <input
            bind:value={invite}
            autocomplete="off"
            spellcheck="false"
            placeholder="wk_… or https://work.jevido.app/#k=…"
            required
          />
        </label>
        {#if readKeyWarning}
          <!-- Not an alert: nothing has failed, and this is a guess. -->
          <p class="warn" role="status">
            That looks like a <strong>read</strong> key. It will open the workspace and refuse
            every edit.
          </p>
        {/if}
      {/if}

      {#if purpose !== "new" || shared}
        <label>
          <span>Server</span>
          <input bind:value={server} autocomplete="off" spellcheck="false" required />
        </label>
      {/if}

      {#if (purpose === "new" && shared) || purpose === "share"}
        <label>
          <span>Signup token</span>
          <input bind:value={signupToken} type="password" autocomplete="off" spellcheck="false" />
          <em class="help">
            The token the server was configured with. A server that is not handing out new
            workspaces will refuse this whatever you type.
          </em>
        </label>
      {/if}

      {#if workspaces.error}
        <p class="error" role="alert">{workspaces.error}</p>
      {/if}

      <footer>
        <button type="button" class="ghost" onclick={onclose}>Cancel</button>
        <button type="submit" class="primary" disabled={!canSubmit}>
          {#if workspaces.busy}
            Working…
          {:else if purpose === "join"}
            Join
          {:else if purpose === "rekey"}
            Use this key
          {:else if purpose === "share"}
            Share
          {:else}
            Create
          {/if}
        </button>
      </footer>
    {/if}
  </form>
</dialog>

<style>
  dialog {
    width: min(480px, calc(100vw - 32px));
    padding: 0;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    color: var(--text);
    box-shadow: 0 12px 32px rgb(0 0 0 / 0.45);
  }

  dialog::backdrop {
    background: rgb(0 0 0 / 0.5);
  }

  form {
    display: grid;
    gap: 12px;
    padding: 16px;
  }

  h2 {
    margin: 0;
    font-size: 14px;
  }

  .lede {
    margin: 0;
    color: var(--text);
  }

  label {
    display: grid;
    gap: 4px;
  }

  label > span {
    color: var(--muted);
    font-size: 11px;
  }

  input[type="text"],
  input[type="password"],
  input:not([type]) {
    padding: 6px 8px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--bg);
    color: inherit;
    font: inherit;
  }

  input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -1px;
    border-color: var(--accent);
  }

  input[readonly] {
    color: var(--muted);
  }

  fieldset {
    display: grid;
    gap: 8px;
    margin: 0;
    padding: 10px;
    border: 1px solid var(--line);
    border-radius: 6px;
  }

  legend {
    padding: 0 4px;
    color: var(--muted);
    font-size: 11px;
  }

  .choice {
    display: flex;
    align-items: start;
    gap: 8px;
    cursor: pointer;
  }

  .choice span {
    display: grid;
    gap: 1px;
  }

  .choice strong {
    font-weight: 600;
  }

  .choice em,
  .help {
    color: var(--muted);
    font-size: 11px;
    font-style: normal;
    line-height: 1.5;
  }

  .note {
    margin: -6px 0 0;
    color: var(--muted);
    font-size: 11px;
  }

  .warn {
    margin: -6px 0 0;
    color: var(--accent);
    font-size: 11px;
  }

  .error {
    margin: 0;
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 5px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
  }

  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }

  .ghost,
  .primary {
    padding: 6px 14px;
    border-radius: 5px;
    font: inherit;
    cursor: pointer;
  }

  .ghost {
    border: 1px solid var(--line);
    background: var(--panel-2);
    color: var(--muted);
  }

  .ghost:hover {
    border-color: #3a4250;
    color: var(--text);
  }

  .primary {
    border: 1px solid var(--accent);
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
  }

  .primary:disabled {
    opacity: 0.5;
    cursor: default;
  }

  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
