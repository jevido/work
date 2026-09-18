<script lang="ts">
  import { looksLikeReadKey } from "../lib/workspace/invite";
  import type { KnownWorkspace } from "../../bindings/dev.jevido/work/internal/workbench/models.js";
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
  export type Purpose = "create" | "join" | "rekey" | "tab" | "keys";

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

  /** True once a workspace has been made, so the dialog can show its keys. */
  let made = $state(false);
  /** Which field was last copied, so one button at a time says "Copied". */
  let copied = $state<string | null>(null);

  /**
   * Every workspace this machine has keys for, whenever anybody asks.
   *
   * This used to be a read key shown once, at creation, with a line saying the
   * server would never show it again. The second half of that was true and the
   * first half was the wrong thing to build on it: a key is a door, not a
   * password. Everyone who is in the workspace holds one already, and somebody
   * who wants to send a colleague a read-only link an hour later should not
   * have to make a new workspace to get one.
   *
   * And it showed one workspace: the one that is open. The keys of every
   * workspace this machine has been in are on this machine -- the server keeps
   * hashes and cannot show any of them again -- so the panel that would not
   * list them was hiding the only copy that exists.
   */
  $effect(() => {
    if (!showingKeys) return;
    void workspaces.loadKnown();
  });

  const showingKeys = $derived(purpose === "keys" || made);

  /** Which workspaces have their keys on screen. Closed until asked. */
  let revealed = $state<Record<string, boolean>>({});

  function reveal(id: string) {
    revealed = { ...revealed, [id]: !revealed[id] };
  }

  /** The viewer link for a workspace, or "" when this machine has no read key. */
  function viewerLinkFor(entry: KnownWorkspace): string {
    const at = (entry.serverUrl || "").trim().replace(/\/+$/, "");
    return entry.readKey && at ? `${at}/#k=${entry.readKey}` : "";
  }

  /** The same for somebody who is meant to be able to change things. */
  function joinLinkFor(entry: KnownWorkspace): string {
    const at = (entry.serverUrl || "").trim().replace(/\/+$/, "");
    return entry.writeKey && at ? `${at}/#k=${entry.writeKey}` : "";
  }

  /** The workspace whose keys are being taken off this machine, or null. */
  let forgetting = $state<KnownWorkspace | null>(null);

  async function forget(entry: KnownWorkspace) {
    forgetting = null;
    await workspaces.forgetKeys(entry.id);
  }

  const TITLES: Record<Purpose, string> = {
    create: "New workspace",
    join: "Join a workspace",
    rekey: "Use a different key",
    tab: "New tab",
    keys: "Keys on this machine",
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
        // No signup token in the condition. Servers do not gate creation by
        // default, so requiring one here would disable the button for
        // everybody whose server is perfectly happy to make them a workspace.
        // A server that does gate it refuses the call and says so.
        return name.trim() !== "" && server.trim() !== "";
      case "join":
      case "rekey":
        return invite.trim() !== "";
      case "tab":
        return name.trim() !== "";
      case "keys":
        return false;
    }
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!canSubmit) return;

    switch (purpose) {
      case "create": {
        // The dialog stays open on success and turns into the keys panel: the
        // next thing anybody does after making a workspace is invite somebody
        // to it. Not because this is the last chance to see anything -- the
        // same panel opens from the tab strip whenever you want it.
        if (await workspaces.createShared(server.trim(), signupToken.trim(), name)) made = true;
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
      case "keys":
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

  async function copy(what: string, text: string) {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      copied = what;
    } catch {
      // No clipboard permission. Every field here is selectable, which is what
      // the button was a shortcut for.
      copied = null;
    }
  }

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

    {#if showingKeys}
      <!--
        Every workspace whose keys this machine kept, the one that is open
        first.

        A key is a door, not a password: it is what somebody needs to be in the
        workspace at all, and everyone already in it is holding one. The server
        keeps hashes, so what is on this machine is the only copy of any of
        them -- which is why they are all listed here, and why this is the only
        place in the app that offers them. It used to be two buttons in two
        places, both showing one workspace.
      -->
      {#if made}
        <p class="lede">Created. Its keys are below — this panel opens again from the tab strip.</p>
      {/if}

      {#if workspaces.known.length === 0}
        <p class="note none">
          No keys on this machine. A workspace made here or joined here keeps its keys,
          and a workspace that lives only on this machine has none to keep.
        </p>
      {/if}

      {#each workspaces.known as entry (entry.id)}
        {@const open = revealed[entry.id] === true}
        {@const viewerLink = viewerLinkFor(entry)}
        {@const joinLink = joinLinkFor(entry)}
        <section class="known">
          <div class="known-head">
            <div class="known-name">
              <strong>{entry.name || "Untitled workspace"}</strong>
              {#if entry.joined}<span class="here">open here</span>{/if}
              <span class="where">{entry.serverUrl}</span>
            </div>
            <button
              type="button"
              class="ghost"
              aria-expanded={open}
              onclick={() => reveal(entry.id)}
            >
              {open ? "Hide keys" : "Keys"}
            </button>
          </div>

          {#if open}
            <label>
              <span>Read-only link</span>
              <input
                type="text"
                value={viewerLink}
                readonly
                placeholder="no read key on this machine"
                onfocus={(e) => e.currentTarget.select()}
              />
            </label>
            <div class="row">
              <button
                type="button"
                class="ghost"
                disabled={!viewerLink}
                onclick={() => copy(`read:${entry.id}`, viewerLink)}
              >
                {copied === `read:${entry.id}` ? "Copied" : "Copy"}
              </button>
              <!-- Only when there is nothing to copy. The server cannot show a
                   key twice, so a machine that joined with a write key has no
                   read key until it asks for one -- and asking for a second
                   one when the first is right there in the field above is what
                   made this panel look as though looking at keys issued
                   them. -->
              {#if !viewerLink}
                <button
                  type="button"
                  class="ghost"
                  disabled={workspaces.busy}
                  onclick={() => workspaces.mintFor(entry.id, "read")}
                >
                  Get a read key
                </button>
              {/if}
            </div>

            <label>
              <span>Write key</span>
              <input
                type="text"
                value={entry.writeKey}
                readonly
                onfocus={(e) => e.currentTarget.select()}
              />
            </label>
            <p class="note">
              Anyone holding this can change the workspace. The link copies as an invitation
              somebody can join with.
            </p>
            <div class="row">
              <button
                type="button"
                class="ghost"
                disabled={!joinLink}
                onclick={() => copy(`write:${entry.id}`, joinLink)}
              >
                {copied === `write:${entry.id}` ? "Copied" : "Copy link"}
              </button>
              {#if !entry.joined}
                <!-- A key off the machine, which has to be possible or keeping
                     it was not a decision anybody could reverse. The joined
                     workspace has no such button: leaving it is what that is,
                     and it is in the badge. -->
                <button
                  type="button"
                  class="ghost"
                  disabled={workspaces.busy}
                  onclick={() => (forgetting = entry)}
                >
                  Forget these keys
                </button>
              {/if}
            </div>
          {/if}
        </section>
      {/each}

      {#if forgetting}
        <p class="warn" role="alert">
          Forget the keys for <strong>{forgetting.name || "that workspace"}</strong>? The
          server cannot show them again, so this machine would need somebody to send them
          before it could open that workspace.
        </p>
        <div class="row">
          <button type="button" class="ghost" onclick={() => (forgetting = null)}>Keep them</button>
          <button type="button" class="ghost danger" onclick={() => forgetting && forget(forgetting)}>
            Forget them
          </button>
        </div>
      {/if}

      <p class="note">
        Asking for a key never takes one away: every key already in use keeps working.
      </p>

      {#if workspaces.error}
        <p class="error" role="alert">{workspaces.error}</p>
      {/if}

      <footer>
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
        <!-- Folded away, because almost nobody needs it. Most servers create
             workspaces for anyone who asks; the ones that do not say so when
             they refuse, and this is where somebody sent back here goes. A
             required-looking field in front of a step that usually needs
             nothing was the whole reason creating a workspace felt broken. -->
        <details>
          <summary>Advanced</summary>
          <label>
            <span>Signup token</span>
            <input bind:value={signupToken} type="password" autocomplete="off" spellcheck="false" />
            <em class="help">
              Only for a server that has closed signup. Leave it empty otherwise.
            </em>
          </label>
        </details>
      {/if}

      {#if purpose === "create" && workspaces.localOnly}
        <!-- Creating replaces the workspace that is open, and the one that is
             open is this machine's. Nothing on this side can seed a server
             with an existing document, so the tabs and the plan in it stay
             where they are and the new workspace starts empty. Somebody who
             has been working locally for a week has to be told that before
             they press Create, not after. -->
        <p class="warn" role="status">
          You have a workspace on this machine. Creating one on a server opens that one
          instead — what you have here stays on this machine and does not come with it.
        </p>
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

  <!--
    Inside the dialog element rather than beside it: this one is modal, and a
    panel rendered outside it would be painted under its backdrop. The top
    layer covers everything in here.
  -->
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

  details {
    margin: -4px 0 0;
  }

  /* The two buttons that belong to the field above them: copy it, or ask the
     server for a new one. Right-aligned so they read as attached to the field
     rather than as the dialog's own actions, which are in the footer. */
  .row {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin: -6px 0 0;
  }

  .row button {
    padding: 4px 10px;
    border-radius: 5px;
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  .row button:disabled {
    opacity: 0.45;
    cursor: default;
  }

  .known {
    display: grid;
    gap: 10px;
    padding: 10px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel-2);
  }

  .known-head {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .known-name {
    display: grid;
    min-width: 0;
    gap: 2px;
    margin-right: auto;
  }

  .known-name strong {
    overflow-wrap: anywhere;
  }

  .here {
    justify-self: start;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent);
    color: #1a1408;
    font-size: 10px;
  }

  .where {
    color: var(--muted);
    font-size: 11px;
    overflow-wrap: anywhere;
  }

  .known-head .ghost {
    flex: none;
    padding: 4px 10px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel);
    color: var(--muted);
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  .none {
    margin: 0;
  }

  .danger {
    border-color: var(--err);
    color: var(--err);
  }

  summary {
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  details[open] summary {
    margin-bottom: 10px;
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
