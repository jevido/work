<script lang="ts">
  import Article from "./components/Article.svelte";
  import Contents from "./components/Contents.svelte";
  import Search from "./components/Search.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import { nav, page as fetchPage, type NavSpace, type Page } from "./lib/api";
  import { current, go, href, type Route } from "./lib/router";
  import { currentTheme, setTheme, type Theme } from "./lib/theme";

  let spaces = $state<NavSpace[]>([]);
  let route = $state<Route | null>(current());
  let page = $state<Page | null>(null);
  let failed = $state("");
  let loading = $state(true);
  let theme = $state<Theme>(currentTheme());

  /** The first page of the first space, which is where "/" goes. */
  const firstPage = $derived.by(() => {
    for (const space of spaces) {
      if (space.pages.length > 0) return { space: space.slug, slug: space.pages[0].slug };
    }
    return null;
  });

  $effect(() => {
    const controller = new AbortController();
    nav(controller.signal)
      .then((answer) => (spaces = answer.spaces))
      .catch((err) => {
        if (!controller.signal.aborted) {
          failed = err instanceof Error ? err.message : "the site did not answer";
        }
      });
    return () => controller.abort();
  });

  // The address bar is the source of truth for which page is open, so Back and
  // a link click come through the same path.
  $effect(() => {
    const onpop = () => (route = current());
    addEventListener("popstate", onpop);
    return () => removeEventListener("popstate", onpop);
  });

  $effect(() => {
    const where = route;
    if (!where) {
      page = null;
      loading = false;
      return;
    }
    loading = true;
    const controller = new AbortController();
    fetchPage(where.space, where.slug, controller.signal)
      .then((answer) => {
        page = answer;
        failed = "";
        loading = false;
        // Only when the address has no fragment of its own: a link to
        // #a-heading is a request to land at that heading, and scrolling to the
        // top would undo it.
        if (!location.hash) scrollTo({ top: 0 });
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        page = null;
        loading = false;
        failed = err instanceof Error ? err.message : "that page did not load";
      });
    return () => controller.abort();
  });

  function navigate(to: Route) {
    go(to);
    route = to;
  }

  $effect(() => {
    document.title = page ? `${page.title} — Charted` : "Charted";
  });
</script>

<header class="bar">
  <a
    class="brand"
    href="/"
    onclick={(event) => {
      if (event.metaKey || event.ctrlKey || event.shiftKey) return;
      event.preventDefault();
      history.pushState(null, "", "/");
      route = null;
    }}
  >
    Charted
  </a>

  <div class="tools">
    <Search {navigate} />
    <button
      class="theme"
      onclick={() => {
        theme = theme === "dark" ? "light" : "dark";
        setTheme(theme);
      }}
      aria-label={theme === "dark" ? "Use the light theme" : "Use the dark theme"}
    >
      {theme === "dark" ? "☾" : "☀"}
    </button>
  </div>
</header>

<div class="shell">
  <aside class="nav"><Sidebar {spaces} {route} {navigate} /></aside>

  <main>
    {#if failed && !page}
      <div class="message" role="alert">
        <h1>This did not load</h1>
        <p>{failed}</p>
      </div>
    {:else if loading && !page}
      <div class="message"><p>Loading…</p></div>
    {:else if page}
      <Article {page} {navigate} />
    {:else if firstPage}
      <div class="message">
        <h1>Charted</h1>
        <p>Everything published here, in the order somebody chose to put it in.</p>
        <p><a href={href(firstPage)} onclick={(e) => (e.preventDefault(), navigate(firstPage))}>Start reading</a></p>
      </div>
    {:else}
      <div class="message">
        <h1>Nothing published yet</h1>
        <p>Documentation mode writes here. When it has, the first page appears in the sidebar.</p>
      </div>
    {/if}
  </main>

  <div class="toc">
    {#if page}
      <Contents headings={page.toc} />
    {/if}
  </div>
</div>

<style>
  .bar {
    position: sticky;
    top: 0;
    z-index: 5;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 10px 18px;
    border-bottom: 1px solid var(--line);
    background: color-mix(in srgb, var(--bg) 88%, transparent);
    backdrop-filter: blur(8px);
  }

  .brand {
    color: var(--text);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: -0.01em;
    text-decoration: none;
  }

  .tools {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .theme {
    width: 30px;
    height: 28px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    color: var(--muted);
    font-size: 13px;
    cursor: pointer;
  }

  .theme:hover {
    color: var(--text);
  }

  .shell {
    display: grid;
    grid-template-columns: 250px minmax(0, 1fr) 220px;
    align-items: start;
    max-width: 1400px;
    margin: 0 auto;
  }

  .nav {
    position: sticky;
    top: 49px;
    max-height: calc(100vh - 49px);
    border-right: 1px solid var(--line);
    overflow-y: auto;
  }

  .message {
    max-width: 46rem;
    padding: 48px 28px;
  }

  .message h1 {
    margin: 0 0 8px;
    font-size: 24px;
  }

  .message p {
    margin: 0 0 8px;
    color: var(--muted);
    font-size: 14px;
    line-height: 1.6;
  }

  .message a {
    color: var(--accent);
  }

  /* One breakpoint, not three: the contents list goes first because it is the
     one thing the page repeats, then the sidebar, which the search box can
     stand in for. */
  @media (max-width: 1100px) {
    .shell {
      grid-template-columns: 250px minmax(0, 1fr);
    }

    .toc {
      display: none;
    }
  }

  @media (max-width: 760px) {
    .shell {
      grid-template-columns: minmax(0, 1fr);
    }

    .nav {
      position: static;
      max-height: none;
      border-right: 0;
      border-bottom: 1px solid var(--line);
    }
  }
</style>
