<script lang="ts">
  import { looksLikeReadKey } from "../lib/workspace/invite";
  import type { Workspaces } from "../lib/workspace/workspaces.svelte";

  /**
   * Which of the four things this dialog is for.
   *
   * One component rather than four, because they are the same three fields in
   * different combinations and four dialogs would be four places to get the
   * error handling and the focus return slightly different from each other.
   *
   * There used to be a fifth, "share": take a workspace that exists only on
   * this machine and put it on a server, sending its contents along. It is
   * gone because there is no call that would do it -- the backend creates a
   * workspace and syncs its own document, and nothing on WorkbenchService
   * takes a document to seed one with. A button that pretended otherwise
   * would lose whatever was in the outline.
   */
  export type Purpose = "create" | "join" | "rekey" | "tab";

  let {
    purpose,
    workspaces,
    onclose,
  }: {
    purpose: Purpose;
    workspaces: Workspaces;
    onclose: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  // Seeds, not bindings. The dialog is mounted fresh each time it opens and
  // these are then the person's to edit -- a field that snapped back while it
  // was being typed would be a form fighting its user.
  let server = $state(workspaces.lastServer);
  let name = $state("");
  let signupToken = $state("");
  let invite = $state("");

  /** The read key, once there is one. The only moment the server will show it. */
  let readKey = $state<string | null>(null);
  let copied = $state(false);

  const TITLES: Record<Purpose, string> = {
    create: "New workspace",
    join: "Join a workspace",
    rekey: "Use a different key",
    tab: "New tab",
  };

  /**
   * A read key pasted where a write key belongs.
   *
   * Said as soon as the prefix is typed, and said again by the store, which
   * refuses the join outright before any tab opens. The server refuses it too.
   * Three places, and all three are the same answer: a read key here produces
   * a workspace that loads fine and silently refuses every edit.
   */
  const readKeyWarning = $derived(
    (purpose === "join" || purpose === "rekey") && looksLikeReadKey(invite.trim()),
  );

  const canSubmit = $derived.by(() => {
    if (workspaces.busy) return false;
    switch (purpose) {
      case "create":
        return name.trim() !== "" && server.trim() !== "" && signupToken.trim() !== "";
      case "join":
      case "rekey":
        return invite.trim() !== "";
      case "tab":
        return name.trim() !== "";
    }
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!canSubmit) return;

    switch (purpose) {
      case "create": {
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
        if (await workspaces.rekey(invite, server.trim())) onclose();
        return;
      case "tab":
        if (await workspaces.newTab(name)) onclose();
        return;
    }
  }

  /**
   * Stops syncing, from the dialog that opens when a key was refused.
   *
   * Here rather than anywhere else because this is where somebody ends up when
   * the key stopped working and they do not have another one. Unsent ops are
   * kept -- the backend leaves the outbox on disk -- so this is reversible,
   * which is why it asks nothing before doing it.
   */
  async function leave() {
    if (await workspaces.leave()) onclose();
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
        Created. This is the only time the server will show the read key, so take it
        now.
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
      {#if purpose === "create" || purpose === "tab"}
        <label>
          <span>Name</span>
          <!-- No autofocus attribute: showModal() already puts the caret in
               the first focusable thing in the dialog, which is this. -->
          <input bind:value={name} autocomplete="off" required />
        </label>
      {/if}

      {#if purpose === "tab"}
        <p class="note">
          A tab is part of the workspace, so everyone in it gets this one. The folder it
          means on this machine is chosen separately, and is never sent anywhere.
        </p>
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

      {#if purpose !== "tab"}
        <label>
          <span>Server</span>
          <input bind:value={server} autocomplete="off" spellcheck="false" required />
        </label>
      {/if}

      {#if purpose === "create"}
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
        {#if purpose === "rekey"}
          <!-- The other way out. Somebody whose key stopped working and who has
               no replacement is otherwise stuck in a dialog that cannot be
               satisfied. Unsent changes are kept, so this is reversible. -->
          <button type="button" class="ghost leave" onclick={leave}>
            Leave this workspace
          </button>
        {/if}
        <button type="button" class="ghost" onclick={onclose}>Cancel</button>
        <button type="submit" class="primary" disabled={!canSubmit}>
          {#if workspaces.busy}
            Working…
          {:else if purpose === "join"}
            Join
          {:else if purpose === "rekey"}
            Use this key
          {:else if purpose === "tab"}
            Open it
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

  /* Away from the two buttons that finish the dialog, so the destructive-
     looking one is not next to the one somebody is reaching for. */
  .leave {
    margin-right: auto;
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
