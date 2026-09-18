<script lang="ts">
  import type { Workspaces } from "../lib/workspace/workspaces.svelte";

  let {
    workspaces,
    /** The tab the folder belongs to. */
    id,
    /** The folder bound to it on this machine, empty when there is none. */
    dir,
  }: { workspaces: Workspaces; id: string; dir: string } = $props();

  /**
   * What is in the field, which is not the same as what is bound.
   *
   * A field bound straight to `dir` would rewrite itself under the caret every
   * time Go republished the workspace -- which is every sync tick. So the
   * typing lives here, and `dir` is copied in whenever the tab changes or the
   * bound folder does, unless somebody is in the middle of editing it.
   */
  // svelte-ignore state_referenced_locally
  // The initial value on purpose: the effect below is what keeps it in step
  // afterwards, and it has a condition this initialiser does not need.
  let typed = $state(dir);
  let editing = $state(false);
  let field = $state<HTMLInputElement | null>(null);

  $effect(() => {
    void id;
    const bound = dir;
    if (!editing) typed = bound;
  });

  /** What went wrong with the last path typed here, if anything. */
  let refused = $state("");

  async function commit() {
    const want = typed.trim();
    editing = false;
    refused = "";
    // Unchanged, or emptied. Neither is a change to make: a tab with no folder
    // is a state Go has, but there is no call that takes one back, and an
    // empty submit reads as a cancel far more often than as a request.
    if (want === dir || want === "") {
      typed = dir;
      return;
    }
    workspaces.error = null;
    if (await workspaces.setFolder(id, want)) return;
    // Go refused it -- no such folder, or a file. The field goes back to what
    // is actually bound rather than sitting there showing a path that is not.
    refused = workspaces.error ?? "That folder could not be used.";
    typed = dir;
  }

  function onKey(event: KeyboardEvent) {
    if (event.key === "Enter") {
      event.preventDefault();
      field?.blur();
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      editing = false;
      refused = "";
      typed = dir;
      field?.blur();
    }
  }

  async function browse() {
    refused = "";
    await workspaces.bind(id);
  }
</script>

<!--
  Which folder all of this is about, and the way to change it.

  It used to be the tail of the path with the whole of it on a title, which
  answered "which project" and nothing else: somebody who wanted to point this
  tab somewhere else had to leave the workspace or find the picker on the
  notice over the office. The path is the control now -- type or paste one and
  press Enter, or press the button and walk a picker.
-->
<div class="folder" class:bad={refused !== ""}>
  <input
    type="text"
    class="path"
    bind:this={field}
    bind:value={typed}
    spellcheck="false"
    autocapitalize="off"
    autocorrect="off"
    aria-label="Project folder for this tab"
    aria-invalid={refused !== "" ? "true" : undefined}
    placeholder="No folder on this machine"
    title={refused || dir || "No folder on this machine"}
    oninput={() => (editing = true)}
    onfocus={(event) => event.currentTarget.select()}
    onblur={commit}
    onkeydown={onKey}
  />
  <button class="browse" onclick={browse} title="Choose a folder">Browse…</button>
</div>

<style>
  .folder {
    display: flex;
    flex: 1;
    align-items: center;
    min-width: 0;
    margin-right: auto;
    gap: 4px;
  }

  /* Monospace, because it is a path: the eye reads a path by its separators,
     and a proportional font moves them around under the same folder names.

     Borderless until it is touched. It sits in a strip of labels, and a box
     drawn around it at rest would read as the loudest thing in a bar whose
     job is to be read past. */
  .path {
    flex: 1;
    min-width: 0;
    padding: 2px 4px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: none;
    color: var(--muted);
    font-family: ui-monospace, monospace;
    font-size: 11px;
    text-overflow: ellipsis;
  }

  .path:hover {
    border-color: var(--line);
  }

  .path:focus {
    border-color: var(--accent);
    background: var(--panel);
    color: var(--text);
    outline: none;
    text-overflow: clip;
  }

  .bad .path {
    border-color: var(--err);
    color: var(--text);
  }

  .browse {
    flex: none;
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel);
    color: var(--muted);
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }

  .browse:hover {
    color: var(--text);
  }
</style>
