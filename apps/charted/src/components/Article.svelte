<script lang="ts">
  import type { Page } from "../lib/api";
  import { parse, type Route } from "../lib/router";

  let {
    page,
    navigate,
  }: {
    page: Page;
    navigate: (to: Route) => void;
  } = $props();

  /**
   * Internal links inside the rendered HTML, caught on the way up.
   *
   * One listener on the article rather than a rewrite of the HTML: the markup
   * came out of the server already rendered, and picking through it in the
   * browser to swap anchors for components would mean parsing it twice and
   * owning a second renderer. A click is the only moment the difference
   * matters, so it is handled at the click.
   *
   * Attached rather than declared as onclick, which is not a style choice: an
   * onclick on a <div> is a non-interactive element with a mouse handler, and
   * the accessibility warning about that is right. What is actually
   * interactive here are the anchors inside, which already answer the keyboard
   * -- Enter on a focused link fires a click, and this handler sees it.
   */
  function links(node: HTMLElement) {
    node.addEventListener("click", clicks);
    return () => node.removeEventListener("click", clicks);
  }

  function clicks(event: MouseEvent) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    const anchor = (event.target as HTMLElement | null)?.closest("a");
    if (!anchor) return;

    const href = anchor.getAttribute("href") ?? "";
    // Fragments stay the browser's: it already knows how to scroll to an id,
    // and doing it here would be reimplementing that badly.
    if (!href || href.startsWith("#")) return;
    if (anchor.target === "_blank" || /^[a-z]+:/i.test(href) || href.startsWith("//")) return;

    const to = parse(new URL(href, location.origin).pathname);
    if (!to) return;
    event.preventDefault();
    navigate(to);
  }

  const updated = $derived(
    new Date(page.updatedAt).toLocaleDateString(undefined, {
      year: "numeric",
      month: "long",
      day: "numeric",
    }),
  );
</script>

<article {@attach links}>
  <header>
    <p class="where">{page.space}</p>
    <h1>{page.title}</h1>
    {#if page.description}
      <p class="description">{page.description}</p>
    {/if}
  </header>

  <!-- Rendered by the Go server with raw HTML disabled, which is what makes
       this safe: what arrives is goldmark's output over somebody's markdown,
       not somebody's markup. -->
  <div class="body">{@html page.html}</div>

  <footer>Last changed {updated}</footer>
</article>

<style>
  article {
    max-width: 46rem;
    padding: 32px 28px 96px;
  }

  .where {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  h1 {
    margin: 6px 0 0;
    font-size: 30px;
    line-height: 1.2;
    letter-spacing: -0.02em;
  }

  .description {
    margin: 10px 0 0;
    color: var(--muted);
    font-size: 15px;
    line-height: 1.6;
  }

  header {
    padding-bottom: 20px;
    margin-bottom: 8px;
    border-bottom: 1px solid var(--line);
  }

  footer {
    margin-top: 48px;
    padding-top: 16px;
    border-top: 1px solid var(--line);
    color: var(--muted);
    font-size: 12px;
  }

  /* The rendered markdown. Global, because this markup is not Svelte's and so
     carries none of its scoping attributes. */
  .body :global(h2),
  .body :global(h3),
  .body :global(h4) {
    margin: 32px 0 8px;
    line-height: 1.3;
    scroll-margin-top: 24px;
  }

  .body :global(h2) {
    font-size: 20px;
  }

  .body :global(h3) {
    font-size: 16px;
  }

  .body :global(p),
  .body :global(li) {
    font-size: 15px;
    line-height: 1.7;
  }

  .body :global(a) {
    color: var(--accent);
    text-decoration-color: color-mix(in srgb, var(--accent) 40%, transparent);
  }

  .body :global(code) {
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--code-bg);
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 0.88em;
  }

  .body :global(pre) {
    padding: 14px 16px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--code-bg);
    overflow-x: auto;
  }

  .body :global(pre code) {
    padding: 0;
    background: none;
  }

  .body :global(blockquote) {
    margin: 20px 0;
    padding: 2px 16px;
    border-left: 3px solid var(--accent);
    color: var(--muted);
  }

  .body :global(table) {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
  }

  .body :global(th),
  .body :global(td) {
    padding: 8px 10px;
    border: 1px solid var(--line);
    text-align: left;
  }

  .body :global(img) {
    max-width: 100%;
  }
</style>
