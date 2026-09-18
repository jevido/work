<script lang="ts">
  import { search, type Hit } from "../lib/api";
  import { href, type Route } from "../lib/router";

  let { navigate }: { navigate: (to: Route) => void } = $props();

  let open = $state(false);
  let query = $state("");
  let hits = $state<Hit[]>([]);
  let at = $state(0);
  let failed = $state("");
  let input = $state<HTMLInputElement | undefined>();

  /**
   * One request in flight, and the one before it abandoned.
   *
   * Typing is faster than the network, so without this the results are
   * whichever response happened to land last -- which is not the same as the
   * response to what is in the box.
   */
  let inFlight: AbortController | null = null;

  $effect(() => {
    const text = query.trim();
    inFlight?.abort();
    if (!open || text.length < 2) {
      hits = [];
      failed = "";
      return;
    }

    const controller = new AbortController();
    inFlight = controller;
    // Debounced, because Postgres is fast and a keystroke is faster. 120ms is
    // below what reads as lag and above a fast typist's gap between letters.
    const timer = setTimeout(async () => {
      try {
        const answer = await search(text, controller.signal);
        hits = answer.hits;
        at = 0;
        failed = "";
      } catch (err) {
        if (controller.signal.aborted) return;
        hits = [];
        failed = err instanceof Error ? err.message : "the search did not answer";
      }
    }, 120);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  });

  function show() {
    open = true;
    queueMicrotask(() => input?.focus());
  }

  function hide() {
    open = false;
    query = "";
    hits = [];
  }

  function keys(event: KeyboardEvent) {
    // The shortcut everyone already has in their fingers, and Escape to leave.
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
      event.preventDefault();
      open ? hide() : show();
      return;
    }
    if (!open) return;
    if (event.key === "Escape") {
      event.preventDefault();
      hide();
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      at = Math.min(at + 1, hits.length - 1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      at = Math.max(at - 1, 0);
    } else if (event.key === "Enter" && hits[at]) {
      event.preventDefault();
      pick(hits[at]);
    }
  }

  function pick(hit: Hit) {
    navigate({ space: hit.space, slug: hit.slug });
    hide();
  }
</script>

<svelte:window onkeydown={keys} />

<button class="opener" onclick={show}>
  <span>Search</span>
  <kbd>⌘K</kbd>
</button>

{#if open}
  <!-- A dialog over the page, dismissed by the backdrop or by Escape. The
       backdrop is a plain div with a click handler and no role: it is the
       outside of the dialog, and the keyboard's way out is Escape. -->
  <div class="scrim" onclick={hide} aria-hidden="true"></div>
  <div class="panel" role="dialog" aria-modal="true" aria-label="Search the documentation">
    <input
      bind:this={input}
      bind:value={query}
      type="search"
      placeholder="Search"
      autocomplete="off"
      spellcheck="false"
      aria-controls="search-results"
    />

    <div id="search-results" role="listbox" aria-label="Results">
      {#if failed}
        <p class="note">{failed}</p>
      {:else if query.trim().length < 2}
        <p class="note">Type at least two letters.</p>
      {:else if hits.length === 0}
        <p class="note">Nothing matches that.</p>
      {:else}
        {#each hits as hit, index (hit.space + "/" + hit.slug)}
          <a
            href={href(hit)}
            role="option"
            aria-selected={index === at}
            class:on={index === at}
            onmouseenter={() => (at = index)}
            onclick={(event) => {
              if (event.metaKey || event.ctrlKey || event.shiftKey) return;
              event.preventDefault();
              pick(hit);
            }}
          >
            <span class="where">{hit.space}</span>
            <span class="title">{hit.title}</span>
            <!-- The server's excerpt, which carries <b> around the words that
                 matched. It comes from ts_headline over text this server
                 rendered and stripped itself, so there is no markup in it that
                 a page's author could have put there. -->
            <span class="excerpt">{@html hit.excerpt}</span>
          </a>
        {/each}
      {/if}
    </div>
  </div>
{/if}

<style>
  .opener {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 5px 10px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }

  .opener:hover {
    border-color: var(--accent);
    color: var(--text);
  }

  kbd {
    font-family: inherit;
    font-size: 11px;
    color: var(--muted);
  }

  .scrim {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--ink) 45%, transparent);
    z-index: 10;
  }

  .panel {
    position: fixed;
    top: 12vh;
    left: 50%;
    z-index: 11;
    width: min(620px, calc(100vw - 32px));
    transform: translateX(-50%);
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--panel);
    box-shadow: 0 24px 60px color-mix(in srgb, var(--ink) 35%, transparent);
    overflow: hidden;
  }

  input {
    width: 100%;
    padding: 14px 16px;
    border: 0;
    border-bottom: 1px solid var(--line);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 14px;
  }

  input:focus {
    outline: none;
  }

  #search-results {
    max-height: min(60vh, 460px);
    overflow-y: auto;
  }

  .note {
    margin: 0;
    padding: 16px;
    color: var(--muted);
    font-size: 12px;
  }

  a {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 2px 8px;
    padding: 10px 16px;
    border-bottom: 1px solid var(--line);
    color: var(--text);
    text-decoration: none;
  }

  a:last-child {
    border-bottom: 0;
  }

  a.on {
    background: var(--accent-soft);
  }

  .where {
    color: var(--muted);
    font-size: 11px;
    line-height: 1.6;
  }

  .title {
    font-size: 13px;
    font-weight: 500;
  }

  .excerpt {
    grid-column: 2;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .excerpt :global(b) {
    color: var(--text);
    font-weight: 600;
  }
</style>
