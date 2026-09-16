<script lang="ts">
  import type { Purpose } from "./WorkspaceDialog.svelte";
  import type { SyncState, Workspaces } from "../lib/workspace/workspaces.svelte";

  let {
    workspaces,
    /** Opens the dialog that can replace the key. */
    onfix,
  }: { workspaces: Workspaces; onfix: (purpose: Purpose) => void } = $props();

  /**
   * All of it is the backend's.
   *
   * This badge used to read a sync object that lived in the webview, next to a
   * queue that lived in the webview. Both are gone: there is one queue and one
   * loop, in Go, and everything below is a reading of what it reports over
   * workspace:sync.
   */
  const state = $derived<SyncState>(workspaces.state);
  const pending = $derived(workspaces.pending);
  const behind = $derived(workspaces.behind);

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
        // A held workspace with nothing waiting has been saved, which is a
        // different sentence from one that has nothing to say.
        return workspaces.held ? "Saved" : "Synced";
      case "unsaved":
        return `${pending} ${changes(pending)} not saved`;
      case "syncing":
        return pending === 0 ? "Syncing" : `Saving ${pending} ${changes(pending)}`;
      case "offline":
        return pending === 0 ? "Offline" : `Offline — ${pending} ${changes(pending)} waiting`;
      case "rejected":
        return "Key refused";
    }
  });

  const detail = $derived.by(() => {
    const parts: string[] = [];
    switch (state) {
      case "local":
        parts.push(
          workspaces.joined
            ? "This workspace is only on this machine. Nothing is sent anywhere, and nothing is waiting to be."
            : "No workspace is open. Nothing leaves this machine.",
        );
        break;
      case "synced":
        parts.push(`Up to date with ${workspaces.view?.serverUrl ?? "the server"}.`);
        break;
      case "unsaved":
        parts.push(
          `Nothing goes to ${workspaces.view?.serverUrl ?? "the server"} until you press Save. It is all on disk here already.`,
        );
        // Warned well before the cap rather than at it. What an overflow costs
        // is sharing and not the document -- the journal keeps every op
        // regardless -- but it is the one loss here that no amount of waiting
        // undoes, so it is said while there is still room to act on it.
        if (workspaces.limit > 0 && pending >= workspaces.limit / 5) {
          parts.push(
            `${workspaces.limit} is as many as can wait; past that the oldest are dropped and cannot be sent.`,
          );
        }
        break;
      case "syncing":
        parts.push("Sending your changes.");
        if (behind > 0) parts.push(`${behind} ${changes(behind)} still to read back.`);
        break;
      case "offline":
        parts.push(
          `${sentence(workspaces.syncError, "The server could not be reached.")} Retrying by itself.`,
        );
        if (workspaces.held && pending > 0) {
          parts.push(`${pending} unsaved ${changes(pending)} are safe here in the meantime.`);
        }
        break;
      case "rejected":
        parts.push(
          `${sentence(workspaces.syncError, "The server would not accept this key.")} Nothing will sync until a key it accepts is supplied. Your changes are safe here in the meantime.`,
        );
        break;
    }
    // A dropped op is a hole in this machine's history that no amount of
    // waiting fills, so it is said in every joined state rather than only in
    // the one that is already complaining.
    if (state !== "local" && workspaces.dropped > 0) {
      parts.push(
        `${workspaces.dropped} ${changes(workspaces.dropped)} were dropped because the queue filled up, and are gone.`,
      );
    }
    return parts.join(" ");
  });

  /**
   * What the badge does when pressed, if anything.
   *
   * A badge that is only ever a label is a label; a badge that is sometimes a
   * button and sometimes a label, both looking the same, is worse than either.
   * So the two states with something to do are buttons and the rest are not,
   * and the button ones say what they do.
   *
   * "local" has nothing here on purpose. Nothing has gone wrong, and the two
   * things somebody could do about it -- put this on a server, join one --
   * are both in the tab strip, named, whenever a workspace is local.
   */
  const action = $derived.by(() => {
    if (state === "rejected") return { text: "Use another key", run: () => onfix("rekey") };
    // Before offline on purpose: a held workspace that also cannot reach the
    // server should offer Save, which nudges the loop and therefore retries as
    // well. Two buttons where one does strictly more is one too many.
    if (state === "unsaved") return { text: "Save", run: () => workspaces.sendNow() };
    if (state === "offline") return { text: "Retry now", run: () => workspaces.retry() };
    // Nothing is wrong, and this is not a fix -- it is the way to the keys,
    // which is the thing people want from a synced workspace most often and
    // which used to be reachable only in the seconds after creating one.
    if (workspaces.cloud) return { text: "Keys", run: () => onfix("keys") };
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

  /* Held, with something waiting. Its own shape rather than offline's, because
     the two mean opposite things about whether anything is wrong: offline is a
     thing that fixes itself and this is a thing waiting for you. A filled ring
     rather than a hollow dot -- there is something there, it simply has not
     gone anywhere. */
  .badge[data-state="unsaved"] {
    border-color: #3a4250;
  }

  .badge[data-state="unsaved"] .dot {
    background: var(--text);
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
