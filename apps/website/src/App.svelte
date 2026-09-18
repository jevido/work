<script lang="ts">
  import { labelOf, textOf, type DocNode } from "./lib/doc";
  import Install from "./components/Install.svelte";
  import MapView from "./components/MapView.svelte";
  import { forget, recall, remember, takeKey } from "./lib/key";
  import { Viewer } from "./lib/viewer.svelte";

  const viewer = new Viewer();

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

  /** Why the pasted link was not accepted, or null. Cleared by typing. */
  let pasteError = $state<string | null>(null);

  function usePasted(event: SubmitEvent) {
    event.preventDefault();
    const key = keyFrom(pasted);
    // Answered on submit rather than by greying the button out. A disabled
    // button says "no" without saying why, is skipped by a screen reader
    // entirely, and cannot be pressed to find out -- which leaves somebody
    // who pasted a link with the fragment already stripped staring at a
    // control that will not respond and no idea what is wrong with it.
    if (!key) {
      pasteError =
        pasted.trim() === ""
          ? "Paste the whole link, or just the key from the end of it."
          : "There is no key in that. A viewer link ends with #k=rk_… — if yours does not, the part after #k= was dropped somewhere on the way to you.";
      return;
    }
    pasteError = null;
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
    // The address holds the key now -- see lib/key.ts -- so forgetting it
    // while leaving it in the bar would be a button that undoes itself on the
    // next reload.
    history.replaceState(null, "", window.location.pathname + window.location.search);
  }

</script>

<a class="skip" href="#board">Skip to the board</a>

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
          aria-invalid={pasteError !== null}
          aria-describedby={pasteError !== null ? "paste-error" : undefined}
          oninput={() => (pasteError = null)}
        />
        <button class="primary" type="submit">Open</button>
      </div>
      {#if pasteError}
        <p class="paste-error" id="paste-error">{pasteError}</p>
      {/if}
    </form>
    {#if saved}
      <button class="link" onclick={forgetKey}>Forget the saved link on this device</button>
    {/if}
  </main>
{:else}
  <main>
    <section id="board" aria-labelledby="board-heading">
      <!--
        The tabs, when there is more than one.

        A workspace is several tabs and the desktop app has one of them on
        screen; this page draws the same one thing. With a single tab there is
        nothing to choose, and a strip of one is a control that answers a
        question nobody asked.
      -->
      {#if viewer.tabs.length > 1}
        <div class="tabs" role="tablist" aria-label="Tabs">
          {#each viewer.tabs as tab (tab.id)}
            {@const on = (viewer.tab || viewer.tabs[0].id) === tab.id}
            <button
              role="tab"
              class:on
              aria-selected={on}
              onclick={() => (viewer.tab = tab.id)}
            >
              {tab.name}
            </button>
          {/each}
        </div>
      {/if}

      <h2 id="board-heading" class="sr">Board</h2>

      {#if viewer.outline.length === 0}
        <p class="empty">
          {viewer.status === "loading" ? "Loading the board…" : "This board is empty."}
        </p>
      {:else}
        <MapView {viewer} />
      {/if}

      <!--
        The same lines, for a reader who cannot see a canvas.

        The board is a canvas and a canvas is a picture: `aria-hidden`, because
        a screen reader handed one gets an element with a name and no content.
        The page used to answer that with a lines view anybody could switch to;
        it does not have one any more, so the text lives here instead --
        present in the accessibility tree, absent from the page, and with
        nothing focusable in it so a keyboard never lands somewhere invisible.
      -->
      <div class="sr">
        {@render lines(viewer.outline)}
      </div>

      {#if viewer.detached.length > 0}
        <section class="detached" aria-labelledby="detached-heading">
          <h3 id="detached-heading">Not on the board</h3>
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
  </main>

  <!--
    A nested list, which is what the board is underneath the paper: the tree,
    in order, with the nesting carried by the markup rather than by a picture.
  -->
  {#snippet lines(nodes: readonly DocNode[])}
    <ul>
      {#each nodes as node (node.id)}
        <li>
          {textOf(node).trim() || "(an empty line)"}
          {#if node.children.length > 0}
            {@render lines(node.children)}
          {/if}
        </li>
      {/each}
    </ul>
  {/snippet}

  <footer>
    <p>
      This link is saved in this browser so the address bar does not have to carry it.
    </p>
    <button class="link" onclick={forgetKey}>Forget it on this device</button>

    <Install />
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

  /*
   * In the accessibility tree, off the page.
   *
   * Not display:none and not visibility:hidden -- both take an element out of
   * the accessibility tree as well, which is the half that has to stay. The
   * board is a canvas and a canvas has no text in it; this is where the text
   * is, for a reader who is not looking at a picture.
   */
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: -1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
    border: 0;
  }

  /* One column, because there is one thing on the page. It used to be two --
     the outline beside the plan -- and both of those are gone: this page is
     the board now, and a board wants the width. */
  main {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    max-width: 1400px;
    margin: 0 auto;
    padding: 16px;
  }

  /* The tab strip, above the view switch: which document, then which view of
     it. Scrolls rather than wraps -- a workspace with eight tabs must not push
     the board down the page. */
  .tabs {
    display: flex;
    gap: 4px;
    margin-bottom: 10px;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .tabs button {
    flex: none;
    padding: 4px 12px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    color: var(--muted);
    font: inherit;
    font-size: 13px;
    white-space: nowrap;
    cursor: pointer;
  }

  .tabs button:hover {
    color: var(--text);
  }

  .tabs button.on {
    background: var(--panel-2);
    color: var(--text);
    box-shadow: inset 0 -2px 0 var(--accent);
  }

  /* The board is the page and wants the room to be one.

     Most of the window rather than a fixed box: a board in a 420-pixel strip
     reads as a thumbnail of one. Capped so it does not run past a tall
     monitor, and vh rather than % because the section's parent is the document
     flow and has no height of its own to take a share of. */
  #board {
    min-height: min(76vh, 820px);
    display: flex;
    flex-direction: column;
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

  .paste-error {
    margin: 6px 0 0;
    color: var(--err);
    font-size: 13px;
    line-height: 1.5;
  }

  .empty {
    margin: 0;
    color: var(--muted);
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
