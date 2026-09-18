<script lang="ts">
  import type { NavSpace } from "../lib/api";
  import { href, type Route } from "../lib/router";

  let {
    spaces,
    route,
    navigate,
  }: {
    spaces: NavSpace[];
    route: Route | null;
    navigate: (to: Route) => void;
  } = $props();

  function open(event: MouseEvent, to: Route) {
    // Modified clicks are the browser's: a middle click opens a tab, a
    // ctrl-click opens a background one, and intercepting either takes away
    // something the address bar cannot give back.
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    event.preventDefault();
    navigate(to);
  }
</script>

<nav aria-label="Documentation">
  {#each spaces as space (space.slug)}
    <section>
      <h2>{space.title}</h2>
      {#if space.summary}
        <p class="summary">{space.summary}</p>
      {/if}
      {#if space.pages.length === 0}
        <p class="empty">Nothing written here yet.</p>
      {:else}
        <ul>
          {#each space.pages as page (page.slug)}
            {@const to = { space: space.slug, slug: page.slug }}
            <li>
              <a
                href={href(to)}
                aria-current={route?.space === space.slug && route?.slug === page.slug
                  ? "page"
                  : undefined}
                onclick={(event) => open(event, to)}
              >
                {page.title}
              </a>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/each}
</nav>

<style>
  nav {
    display: flex;
    flex-direction: column;
    gap: 22px;
    padding: 24px 18px 48px;
  }

  h2 {
    margin: 0 0 6px;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .summary,
  .empty {
    margin: 0 0 8px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--muted);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  a {
    display: block;
    padding: 4px 8px;
    margin-left: -8px;
    border-radius: 5px;
    color: var(--text);
    font-size: 13px;
    line-height: 1.5;
    text-decoration: none;
  }

  a:hover {
    background: var(--hover);
  }

  a[aria-current="page"] {
    background: var(--accent-soft);
    color: var(--accent);
    font-weight: 500;
  }
</style>
