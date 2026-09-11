<script lang="ts">
  import {
    TASK_STATE_LABELS,
    findInTree,
    labelOf,
    sourceIdOf,
    statusOf,
    textOf,
  } from "@doc/model";
  import OutlineBranch from "./components/OutlineBranch.svelte";
  import { forget, recall, remember, takeKey } from "./lib/key";
  import { ViewState, setView } from "./lib/view.svelte";
  import { Viewer } from "./lib/viewer.svelte";

  const viewer = new Viewer();
  const view = new ViewState();
  setView(view);

  /** What somebody typed into the "paste a link" field. */
  let pasted = $state("");

  /**
   * Whether this browser is holding a key.
   *
   * Tracked rather than read from storage where it is needed: localStorage is
   * not reactive, so a "Forget it" button whose visibility came from reading
   * it would only disappear the next time something else happened to
   * re-render.
   */
  let saved = $state(recall() !== null);

  /**
   * Reads the key out of the address, stores it and takes it back out of the
   * address, then starts reading the log.
   *
   * All of it in one effect, because the strip has to happen whether or not
   * there was a key -- a reload after a failed fetch must not put the key
   * back into the address bar it was just taken out of.
   */
  $effect(() => {
    const key = takeKey();
    saved = key !== null;
    viewer.use(key);
    return viewer.start();
  });

  /** How long ago the last successful read was, in words, ticking. */
  let now = $state(Date.now());
  $effect(() => {
    const timer = setInterval(() => (now = Date.now()), 5000);
    return () => clearInterval(timer);
  });

  const freshness = $derived.by(() => {
    const at = viewer.updatedAt;
    if (at === null) return "";
    const seconds = Math.max(0, Math.round((now - at) / 1000));
    if (seconds < 10) return "just now";
    if (seconds < 60) return `${seconds} seconds ago`;
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) return `${minutes} ${minutes === 1 ? "minute" : "minutes"} ago`;
    const hours = Math.round(minutes / 60);
    return `${hours} ${hours === 1 ? "hour" : "hours"} ago`;
  });

  const statusLine = $derived.by(() => {
    switch (viewer.status) {
      case "starting":
      case "loading":
        return "Loading…";
      case "live":
        return freshness ? `Updated ${freshness}` : "Up to date";
      case "offline":
        return "Cannot reach the server — retrying";
      case "rejected":
        return "This link no longer works";
      case "no-key":
        return "No link";
    }
  });

  function usePasted(event: SubmitEvent) {
    event.preventDefault();
    const key = keyFrom(pasted);
    if (!key) return;
    remember(key);
    saved = true;
    pasted = "";
    viewer.use(key);
  }

  /** Accepts a whole link or a bare key, because both get pasted. */
  function keyFrom(input: string): string | null {
    const text = input.trim();
    if (text === "") return null;
    if (!/^https?:\/\//i.test(text)) return text;
    try {
      const url = new URL(text);
      const key = new URLSearchParams(url.hash.replace(/^#/, "")).get("k");
      return key?.trim() || null;
    } catch {
      return null;
    }
  }

  function forgetKey() {
    forget();
    saved = false;
    viewer.use(null);
  }

</script>

<a class="skip" href="#outline">Skip to the outline</a>

<header>
  <div class="titles">
    <h1>{viewer.name || "Work"}</h1>
    <!--
      Said plainly and at the top, because a page that looks like the app and
      is not is a page somebody will try to type into. It is also the honest
      framing of what a read key is: this is a window, not an account.
    -->
    <p class="readonly">Read-only view. Nothing here changes anything.</p>
  </div>

  <div class="status" data-status={viewer.status}>
    <span class="dot" aria-hidden="true"></span>
    <span>{statusLine}</span>
    {#if viewer.status === "offline"}
      <button class="link" onclick={() => viewer.retry()}>Retry now</button>
    {/if}
  </div>
</header>

<!-- Polite, and only the status: an outline that grew a line while somebody
     was reading it is not worth interrupting them for, and the connection
     going away is. -->
<p class="announce" role="status" aria-live="polite">{statusLine}</p>

{#if viewer.status === "no-key" || viewer.status === "rejected"}
  <main class="gate">
    <h2>{viewer.status === "rejected" ? "That link did not work" : "Nothing to show yet"}</h2>
    <p>
      {#if viewer.error}
        {viewer.error}
      {:else}
        A viewer link looks like <code>https://…/#k=rk_…</code>. Whoever owns the workspace
        can make you one.
      {/if}
    </p>
    <form onsubmit={usePasted}>
      <label for="paste">Paste a viewer link</label>
      <div class="row">
        <input
          id="paste"
          bind:value={pasted}
          spellcheck="false"
          autocomplete="off"
          placeholder="https://…/#k=rk_…"
        />
        <button class="primary" type="submit" disabled={keyFrom(pasted) === null}>Open</button>
      </div>
    </form>
    {#if saved}
      <button class="link" onclick={forgetKey}>Forget the saved link on this device</button>
    {/if}
  </main>
{:else}
  <main>
    <section id="outline" aria-labelledby="outline-heading">
      <h2 id="outline-heading">Outline</h2>
      {#if viewer.rows.length === 0}
        <p class="empty">
          {viewer.status === "loading" ? "Loading the outline…" : "The outline is empty."}
        </p>
      {:else}
        <OutlineBranch nodes={viewer.tree.filter((n) => n.fields.type !== "task")} />
      {/if}

      {#if viewer.detached.length > 0}
        <section class="detached" aria-labelledby="detached-heading">
          <h3 id="detached-heading">Not in the outline</h3>
          <p>
            The line these were under was deleted. They are still part of the workspace.
          </p>
          <ul>
            {#each viewer.detached as node (node.id)}
              <li>{labelOf(node, 200)}</li>
            {/each}
          </ul>
        </section>
      {/if}
    </section>

    <section aria-labelledby="plan-heading">
      <h2 id="plan-heading">Plan</h2>
      {#if viewer.tasks.length === 0}
        <p class="empty">
          {viewer.status === "loading" ? "Loading the plan…" : "Nothing on the plan."}
        </p>
      {:else}
        <!-- An ordered list, because the order is the content. -->
        <ol>
          {#each viewer.tasks as task (task.id)}
            {@const status = statusOf(task)}
            {@const sourceId = sourceIdOf(task)}
            {@const source = sourceId ? findInTree(viewer.tree, sourceId) : null}
            <li data-status={status}>
              <div class="task">
                <!-- The state as a word, not only as a colour. This is the one
                     thing a plan is read for. -->
                <span class="state">{TASK_STATE_LABELS[status]}</span>
                <span class="title">{textOf(task).trim() || "(untitled task)"}</span>
              </div>
              {#if source}
                <!--
                  Follows the link into the outline: opens whatever is folded
                  in the way, scrolls to the line and marks it. All of that is
                  this browser's, and none of it reaches the server.
                -->
                <button class="source" onclick={() => view.jumpTo(viewer.tree, source.id)}>
                  from <span class="from">{textOf(source).trim() || "an empty line"}</span>
                </button>
              {:else if sourceId}
                <p class="source gone">from a line that has since been deleted</p>
              {/if}
            </li>
          {/each}
        </ol>
      {/if}
    </section>
  </main>

  <footer>
    <p>
      This link is saved in this browser so the address bar does not have to carry it.
    </p>
    <button class="link" onclick={forgetKey}>Forget it on this device</button>
  </footer>
{/if}

<style>
  /*
   * The first thing in the tab order, and invisible until it is focused.
   * The outline can be hundreds of lines, and the plan is after it.
   */
  .skip {
    position: absolute;
    left: -9999px;
    top: 0;
    z-index: 10;
    padding: 8px 14px;
    background: var(--accent);
    color: var(--on-accent);
    font-weight: 600;
  }

  .skip:focus {
    left: 8px;
    top: 8px;
  }

  header {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 8px 16px;
    max-width: 1100px;
    margin: 0 auto;
    padding: 20px 16px 12px;
    border-bottom: 1px solid var(--line);
  }

  .titles {
    margin-right: auto;
  }

  h1 {
    margin: 0;
    font-size: 20px;
    line-height: 1.3;
    overflow-wrap: anywhere;
  }

  .readonly {
    margin: 2px 0 0;
    color: var(--muted);
    font-size: 13px;
  }

  .status {
    display: flex;
    align-items: center;
    gap: 7px;
    flex: none;
    padding: 3px 10px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    color: var(--muted);
    font-size: 12px;
  }

  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--muted);
  }

  .status[data-status="live"] .dot {
    background: var(--ok);
  }

  .status[data-status="offline"] .dot {
    background: var(--accent);
  }

  .status[data-status="rejected"] {
    border-color: var(--err);
    color: var(--err);
  }

  .status[data-status="rejected"] .dot {
    background: var(--err);
  }

  main {
    display: grid;
    /* Two columns when there is room, and one when there is not. The plan
       goes under the outline rather than beside it on a phone, which is also
       the order they are written in -- so the reading order and the visual
       order stay the same one. */
    grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
    gap: 8px 32px;
    max-width: 1100px;
    margin: 0 auto;
    padding: 16px;
  }

  @media (max-width: 780px) {
    main {
      grid-template-columns: minmax(0, 1fr);
      gap: 24px;
    }
  }

  main.gate {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    max-width: 560px;
    gap: 14px;
    padding: 40px 16px;
  }

  h2 {
    margin: 0 0 8px;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .gate h2 {
    font-size: 18px;
    letter-spacing: 0;
    text-transform: none;
    color: var(--text);
  }

  .gate p {
    margin: 0;
    color: var(--muted);
    line-height: 1.6;
  }

  .gate form {
    display: grid;
    gap: 6px;
  }

  .gate label {
    color: var(--muted);
    font-size: 12px;
  }

  .gate .row {
    display: flex;
    gap: 8px;
  }

  .gate input {
    flex: 1;
    min-width: 0;
    padding: 8px 10px;
    /* --edge, not --line: an empty text field is identified by its box. */
    border: 1px solid var(--edge);
    border-radius: 6px;
    background: var(--panel);
    color: inherit;
    font: inherit;
  }

  code {
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--panel-2);
    font-size: 0.9em;
    overflow-wrap: anywhere;
  }

  .empty {
    margin: 0;
    color: var(--muted);
  }

  ol {
    margin: 0;
    padding: 0;
    list-style: none;
    counter-reset: task;
  }

  ol li {
    counter-increment: task;
    position: relative;
    padding: 8px 0 8px 26px;
    border-bottom: 1px solid var(--line);
  }

  ol li::before {
    content: counter(task);
    position: absolute;
    left: 0;
    top: 9px;
    width: 20px;
    text-align: right;
    color: var(--muted);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }

  .task {
    display: flex;
    align-items: baseline;
    gap: 8px;
    overflow-wrap: anywhere;
  }

  .state {
    flex: none;
    padding: 0 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 11px;
    line-height: 17px;
    white-space: nowrap;
  }

  li[data-status="doing"] .state {
    border-color: var(--accent);
    color: var(--accent);
  }

  li[data-status="done"] .state {
    border-color: var(--ok);
    color: var(--ok);
  }

  li[data-status="done"] .title {
    color: var(--muted);
    text-decoration: line-through;
  }

  .source {
    display: block;
    max-width: 100%;
    margin: 4px 0 0;
    padding: 2px 0;
    border: none;
    background: none;
    color: var(--muted);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
    overflow-wrap: anywhere;
  }

  /* Underlined because it goes somewhere -- colour alone would be the only
     thing separating a link from the sentence above it. */
  button.source .from {
    text-decoration: underline dotted;
    text-underline-offset: 2px;
  }

  button.source:hover {
    color: var(--text);
  }

  .source.gone {
    cursor: default;
    font-style: italic;
  }

  .detached {
    margin-top: 24px;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel);
  }

  .detached h3 {
    margin: 0 0 4px;
    font-size: 13px;
  }

  .detached p {
    margin: 0 0 8px;
    color: var(--muted);
    font-size: 12px;
  }

  .detached ul {
    margin: 0;
    padding-left: 18px;
    color: var(--muted);
  }

  footer {
    max-width: 1100px;
    margin: 0 auto;
    padding: 24px 16px 40px;
    color: var(--muted);
    font-size: 12px;
  }

  footer p {
    margin: 0 0 4px;
  }

  .link {
    padding: 0;
    border: none;
    background: none;
    color: inherit;
    font-size: inherit;
    text-decoration: underline;
    text-underline-offset: 2px;
    cursor: pointer;
  }

  .link:hover {
    color: var(--text);
  }

  .primary {
    padding: 8px 16px;
    border: 1px solid var(--accent);
    border-radius: 6px;
    background: var(--accent);
    color: var(--on-accent);
    font-weight: 600;
    cursor: pointer;
  }

  .primary:disabled {
    opacity: 0.5;
    cursor: default;
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

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
