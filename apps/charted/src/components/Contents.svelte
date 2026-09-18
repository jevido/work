<script lang="ts">
  import type { Heading } from "../lib/api";

  let { headings }: { headings: Heading[] } = $props();

  /**
   * Which heading the reader is level with.
   *
   * An IntersectionObserver rather than a scroll handler: the question is "is
   * this heading on screen", which is the observer's entire job, and it answers
   * it without running code on every frame of a scroll.
   */
  let active = $state("");

  $effect(() => {
    const targets = headings
      .map((heading) => document.getElementById(heading.id))
      .filter((el): el is HTMLElement => el !== null);
    if (targets.length === 0) return;

    const seen = new Set<string>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) seen.add(entry.target.id);
          else seen.delete(entry.target.id);
        }
        // The first heading still on screen, in document order -- not the last
        // one crossed, which jumps backwards the moment a short section scrolls
        // past a long one.
        const first = headings.find((heading) => seen.has(heading.id));
        if (first) active = first.id;
      },
      // A band across the upper third: a heading counts as current once it is
      // near the top, not when its last pixel leaves the bottom.
      { rootMargin: "-80px 0px -66% 0px" },
    );

    for (const target of targets) observer.observe(target);
    return () => observer.disconnect();
  });
</script>

{#if headings.length > 1}
  <aside aria-label="On this page">
    <h2>On this page</h2>
    <ul>
      {#each headings as heading (heading.id)}
        <li style="--depth: {Math.max(0, heading.level - 2)}">
          <a href="#{heading.id}" aria-current={active === heading.id ? "true" : undefined}>
            {heading.text}
          </a>
        </li>
      {/each}
    </ul>
  </aside>
{/if}

<style>
  aside {
    position: sticky;
    top: 0;
    align-self: start;
    padding: 24px 18px;
  }

  h2 {
    margin: 0 0 8px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--muted);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
    border-left: 1px solid var(--line);
  }

  a {
    display: block;
    padding: 3px 0 3px calc(12px + var(--depth) * 12px);
    margin-left: -1px;
    border-left: 1px solid transparent;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
    text-decoration: none;
  }

  a:hover {
    color: var(--text);
  }

  a[aria-current="true"] {
    border-left-color: var(--accent);
    color: var(--accent);
  }
</style>
