<script lang="ts">
  import type { SyncState } from "../lib/workspace/sync.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let {
    workspace,
    /** Opens the dialog that can share a workspace or replace its key. */
    onfix,
  }: { workspace: Workspace; onfix: (reason: "share" | "rekey") => void } = $props();

  const sync = $derived(workspace.sync);
  const state = $derived<SyncState>(sync.state);
  const pending = $derived(sync.pending);

  /**
   * The badge's words.
   *
   * Every state gets its own sentence rather than a colour and a number. The
   * two that matter most are the two that look alike from the outside: five
   * changes waiting because the network is down is a thing that fixes itself,
   * and five changes waiting because the key was refused is a thing that never
   * will. They are told apart here, in words, before anybody has to notice
   * that one dot is red and the other is amber.
   */
  const label = $derived.by(() => {
    switch (state) {
      case "local":
        return "On this machine";
      case "synced":
        return "Synced";
      case "syncing":
        return pending === 0 ? "Syncing" : `Saving ${pending} ${changes(pending)}`;
      case "offline":
        return pending === 0 ? "Offline" : `Offline — ${pending} ${changes(pending)} waiting`;
      case "rejected":
        return "Key refused";
    }
  });

  const detail = $derived.by(() => {
    switch (state) {
      case "local":
        return "This workspace has never been shared. Nothing leaves this machine.";
      case "synced":
        return `Up to date with ${sync.base}.`;
      case "syncing":
        return "Sending your changes.";
      case "offline":
        return `${sentence(sync.error, "The server could not be reached.")} Retrying by itself.`;
      case "rejected":
        return `${sentence(sync.error, "The server would not accept this key.")} Nothing will sync until a key it accepts is supplied. Your changes are safe here in the meantime.`;
    }
  });

  /**
   * What the badge does when pressed, if anything.
   *
   * A badge that is only ever a label is a label; a badge that is sometimes a
   * button and sometimes a label, both looking the same, is worse than either.
   * So the two states with something to do are buttons and the rest are not,
   * and the button ones say what they do.
   */
  const action = $derived.by(() => {
    if (state === "rejected") return { text: "Use another key", run: () => onfix("rekey") };
    if (state === "local") return { text: "Share", run: () => onfix("share") };
    if (state === "offline") return { text: "Retry now", run: () => sync.retry() };
    return null;
  });

  function changes(n: number): string {
    return n === 1 ? "change" : "changes";
  }

  /**
   * The server's own words, made into a sentence.
   *
   * They arrive as a fragment -- "unknown or expired key" -- and get read out
   * in the middle of ours, so without this the live region says "Key refused.
   * unknown or expired key Nothing will sync until...". The contract says the
   * message is for humans and may change, so it is punctuated here rather
   * than assumed to be punctuated there.
   */
  function sentence(text: string | null, fallback: string): string {
    const trimmed = (text ?? "").trim();
    if (trimmed === "") return fallback;
    const capitalised = trimmed[0].toUpperCase() + trimmed.slice(1);
    return /[.!?]$/.test(capitalised) ? capitalised : `${capitalised}.`;
  }
</script>

<div class="badge" data-state={state}>
  <span class="dot" aria-hidden="true"></span>
  <span class="label" title={detail}>{label}</span>
  {#if action}
    <button onclick={action.run}>{action.text}</button>
  {/if}
</div>

<!--
  The state, announced when it changes.

  Politely, and with the detail: going offline mid-sentence must not interrupt
  anybody, and "Offline" on its own does not say whether the work is safe. It
  is the one thing a person will want to know and the one thing the badge
  cannot fit.
-->
<p class="announce" role="status" aria-live="polite">{label}. {detail}</p>

<style>
  .badge {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--muted);
  }

  /* Colour is never the only cue -- the words beside it say the same thing --
     but a status light is what the eye checks first. */
  .badge[data-state="synced"] .dot {
    background: var(--ok);
  }

  .badge[data-state="syncing"] .dot {
    background: var(--accent);
  }

  .badge[data-state="offline"] {
    border-color: #4a3a2b;
  }

  .badge[data-state="offline"] .dot {
    background: var(--accent);
    /* Hollow, so "waiting" and "fine" are different shapes as well as
       different words. */
    box-shadow: inset 0 0 0 2px var(--panel-2);
  }

  /*
   * A refused key is the one state that will not resolve on its own, so it is
   * the one state that gets to look wrong: the error colour, a filled pill,
   * and an action next to it. Everything else is a status; this is a problem.
   */
  .badge[data-state="rejected"] {
    border-color: var(--err);
    background: #2a1a1a;
    color: var(--err);
  }

  .badge[data-state="rejected"] .dot {
    background: var(--err);
  }

  button {
    padding: 0 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel);
    color: inherit;
    font-size: 10px;
    line-height: 16px;
    cursor: pointer;
  }

  button:hover {
    border-color: currentColor;
  }

  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  .announce {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
