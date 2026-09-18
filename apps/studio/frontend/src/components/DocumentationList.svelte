<script lang="ts">
  import * as Workbench from "../../bindings/dev.jevido/work/apps/studio/internal/bridge/workbenchservice.js";
  import type { DocumentationSite, PublishResult } from "../../bindings/dev.jevido/work/apps/studio/internal/bridge/models.js";
  import { detailOf, labelOf, statusOf, textOf } from "../lib/workspace/model";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let { workspace }: { workspace: Workspace } = $props();

  let site = $state<DocumentationSite | null>(null);
  let published = $state<Record<string, PublishResult>>({});
  let failed = $state<Record<string, string>>({});
  let busy = $state<string | null>(null);

  $effect(() => {
    // Read on mount rather than held in the workspace: the site is a property
    // of this machine's config, not of the document, and it changes when
    // somebody edits a file rather than when anything here happens.
    Workbench.DocumentationSite()
      .then((answer) => (site = answer))
      .catch(() => (site = null));
  });

  /**
   * What is finished, in plan order, with the page body the agent wrote on it.
   *
   * Finished is the whole filter. Documentation is written about a running
   * product, so a task still in progress has nothing true to say about it yet
   * -- and a page written off a plan rather than off the thing it describes is
   * the failure this mode exists to avoid.
   */
  const shipped = $derived.by(() =>
    workspace.tasks
      .map((task, at) => ({ task, at }))
      .filter(({ task }) => statusOf(task) === "done")
      .map(({ task, at }) => {
        const from = workspace.sourceOf(task);
        return {
          id: task.id,
          at,
          title: labelOf(task),
          // The card's detail is the page. The agent writes it there in this
          // mode, through the same proposal path it writes anything else, so
          // what gets published is what somebody read and approved on the map.
          markdown: detailOf(task),
          from: from ? textOf(from) : "",
        };
      }),
  );

  /** A slug from a title: lowercase words, hyphens, nothing else. */
  function slugify(title: string): string {
    return title
      .toLowerCase()
      .normalize("NFKD")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 80);
  }

  async function publish(item: { id: string; title: string; markdown: string }) {
    const slug = slugify(item.title);
    if (!slug) {
      failed[item.id] = "that title makes no address; give the card a few words";
      return;
    }
    busy = item.id;
    delete failed[item.id];
    try {
      published[item.id] = await Workbench.PublishPage(slug, item.title, "", item.markdown);
    } catch (err) {
      failed[item.id] = err instanceof Error ? err.message : String(err);
    } finally {
      busy = null;
    }
  }
</script>

<section class="documentation" tabindex="-1" aria-label="Documentation">
  <header>
    <h2>Ready to publish</h2>
    {#if site?.baseUrl}
      <p>
        Finished work in this workspace. Publishing writes a page to
        <strong>{site.baseUrl}</strong>, in the <strong>{site.space || "unset"}</strong> space.
        {#if !site.writable}
          This machine has no write token, so nothing can be published from it yet.
        {/if}
      </p>
    {:else}
      <p>
        Finished work in this workspace. No documentation site is configured on this
        machine, so there is nowhere to publish to yet — add a <code>charted</code> block
        to the config file with a <code>baseUrl</code>, a <code>token</code> and a
        <code>space</code>.
      </p>
    {/if}
  </header>

  {#if shipped.length === 0}
    <p class="empty">
      Nothing is finished in this workspace yet. A task closed in implementation shows up
      here.
    </p>
  {:else}
    <ol>
      {#each shipped as item (item.id)}
        <li>
          <span class="n">{item.at + 1}</span>
          <div class="what">
            <p class="title">{item.title}</p>
            {#if item.from}
              <p class="from">from <span>{item.from}</span></p>
            {/if}

            {#if item.markdown}
              <p class="body">{item.markdown.slice(0, 240)}{item.markdown.length > 240 ? "…" : ""}</p>
            {:else}
              <p class="body none">
                No page written on this card yet. Ask in the console for the page, read it
                on the map, then publish it.
              </p>
            {/if}

            {#if published[item.id]}
              {@const result = published[item.id]}
              <!-- `?? []` because the generated type says a Go slice can arrive
                   as null. The service never sends one -- it replaces an empty
                   result with an empty array -- but the binding is what the
                   typechecker reads, and agreeing with it costs two characters. -->
              {@const broken = result.broken ?? []}
              <p class="done">
                Published to <a href={result.url}>{result.url}</a>
              </p>
              {#if broken.length > 0}
                <!-- Said, not treated as a failure: a page that links ahead to
                     one nobody has written yet is normal, and the list is what
                     to write next. -->
                <p class="broken">
                  Links to pages that do not exist yet:
                  {#each broken as link, at (link.space + "/" + link.slug)}
                    <code>{link.space}/{link.slug}</code>{at < broken.length - 1 ? ", " : ""}
                  {/each}
                </p>
              {/if}
            {/if}

            {#if failed[item.id]}
              <p class="failed" role="alert">{failed[item.id]}</p>
            {/if}
          </div>

          <button
            onclick={() => publish(item)}
            disabled={busy !== null || !item.markdown || !site?.writable}
          >
            {busy === item.id ? "Publishing…" : published[item.id] ? "Publish again" : "Publish"}
          </button>
        </li>
      {/each}
    </ol>
  {/if}
</section>

<style>
  .documentation {
    display: flex;
    flex-direction: column;
    gap: 14px;
    height: 100%;
    padding: 16px 18px;
    overflow-y: auto;
  }

  header h2 {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
  }

  header p,
  .empty {
    margin: 6px 0 0;
    max-width: 68ch;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  ol {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 10px;
    align-items: start;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
  }

  .n {
    color: var(--muted);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    line-height: 1.6;
  }

  .title {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
  }

  .from,
  .body,
  .done,
  .broken,
  .failed {
    margin: 4px 0 0;
    font-size: 11px;
    line-height: 1.5;
    color: var(--muted);
  }

  .from span {
    font-style: italic;
  }

  .body {
    white-space: pre-wrap;
  }

  .body.none {
    font-style: italic;
  }

  .failed {
    color: var(--danger, #c2452f);
  }

  button {
    padding: 4px 10px;
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel);
    color: var(--text);
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  button:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
