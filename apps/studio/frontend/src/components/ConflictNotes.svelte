<script lang="ts">
  import { describeNote, type Conflicts, type Note } from "../lib/workspace/conflicts.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";

  let { conflicts, workspace }: { conflicts: Conflicts; workspace: Workspace } = $props();

  const notes = $derived(conflicts.forWorkspace(workspace));
</script>

<!--
  What changed, never who changed it.

  There is no name to show and no colour that stands for a person, because
  nothing in the data model knows one. What a note has is the line, the text
  that is gone and the text that replaced it — which is everything somebody
  needs to decide whether they mind.
-->
{#if notes.length > 0}
  <section class="notes" aria-label="Changes from elsewhere">
    <ul>
      {#each notes as note (note.node + note.field)}
        <li class:gone={note.deleted}>
          <p>{describeNote(note)}</p>
          <div class="acts">
            {#if !note.deleted}
              <!--
                An ordinary edit, not a rollback. It beats what beat it, the
                other side receives it, and they can undo in turn.
              -->
              <button onclick={() => conflicts.undo(workspace, note)}>Undo</button>
            {/if}
            <button class="quiet" onclick={() => conflicts.dismiss(note)}>Dismiss</button>
          </div>
        </li>
      {/each}
    </ul>

    {#if notes.length > 1}
      <button class="quiet all" onclick={() => conflicts.dismissAll()}>
        Dismiss all {notes.length}
      </button>
    {/if}
  </section>
{/if}

<style>
  .notes {
    border: 1px solid var(--line);
    border-radius: 5px;
    background: var(--panel);
    padding: 6px 8px;
    margin: 0 0 8px;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  li {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
    font-size: 12px;
    line-height: 1.5;
  }

  /* A deleted line is a different situation from a changed one: there is
     nothing to undo, only text to keep. Said in words above; marked here so
     the difference is visible before the sentence is read. */
  li.gone {
    opacity: 0.85;
  }

  p {
    margin: 0;
    min-width: 0;
  }

  .acts {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }

  button {
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  button.quiet {
    color: var(--muted);
  }

  button:hover {
    color: inherit;
  }

  .all {
    margin-top: 6px;
  }
</style>
