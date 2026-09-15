<script lang="ts">
  import type { Restructuring } from "../lib/workspace/restructure.svelte";
  import type { Workspace } from "../lib/workspace/workspace.svelte";
  import type { Mode } from "../lib/workspace/model";

  let {
    workspace,
    restructuring,
    mode,
    /** What the button says. The wording is the point -- see below. */
    label,
    /** What the empty field says, which is where the useful examples go. */
    placeholder,
    /** The row the caret is on, if any. */
    focus = null,
  }: {
    workspace: Workspace;
    restructuring: Restructuring;
    mode: Mode;
    label: string;
    placeholder: string;
    focus?: { id: string; text: string } | null;
  } = $props();

  let request = $state("");
  let open = $state(false);
  let field = $state<HTMLInputElement | null>(null);

  const busy = $derived(restructuring.busy(workspace));

  /*
    One mechanism, two wordings. The component is shared because opening a
    composer and sending a line is the same act in both modes; the label and
    the placeholder are props because what you are asking for is not. "Break
    this into tasks" and "reorganise this branch" are different questions of
    the same document, and a single generic "Ask Claude" would hide that from
    the one person who has to decide which they meant.
  */

  async function send(event: SubmitEvent) {
    event.preventDefault();
    const said = request;
    request = "";
    open = false;
    await restructuring.ask(workspace, mode, said, focus);
  }

  function start() {
    open = true;
    // After the field exists. Opening a composer and leaving the caret
    // somewhere else is a composer nobody types into.
    queueMicrotask(() => field?.focus());
  }
</script>

<div class="ask">
  {#if busy}
    <!--
      Said rather than spun. "Claude is thinking" with no sense of what it was
      asked is a progress bar; naming the mode at least says which panel the
      answer is going to appear in.
    -->
    <p class="thinking">Claude is working on it. The suggestion will appear here to review.</p>
  {:else if open}
    <form onsubmit={send}>
      <label>
        <span class="sr">{label}</span>
        <input
          bind:this={field}
          bind:value={request}
          {placeholder}
          onkeydown={(e) => {
            if (e.key === "Escape") {
              open = false;
              request = "";
            }
          }}
        />
      </label>
      <button type="submit" disabled={request.trim() === ""}>Ask</button>
      <button type="button" onclick={() => ((open = false), (request = ""))}>Cancel</button>
    </form>
  {:else}
    <!--
      A composer rather than firing on the click. "Reorganise this branch" with
      no further instruction is a coin toss; the field is where "group these by
      which part of the app they touch" gets said, and that sentence is the
      difference between a proposal worth reading and one worth discarding.
    -->
    <button type="button" onclick={start}>{label}</button>
  {/if}

  {#if restructuring.refused}
    <p class="refused">{restructuring.refused}</p>
  {/if}
</div>

<style>
  .ask {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    padding: 6px 0;
  }

  form {
    display: flex;
    flex: 1;
    gap: 6px;
    min-width: 0;
  }

  input {
    flex: 1;
    min-width: 0;
    padding: 4px 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel);
    color: inherit;
    font: inherit;
  }

  button {
    padding: 4px 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--panel);
    color: var(--muted);
    font-size: 11px;
    cursor: pointer;
  }

  button:hover:not(:disabled) {
    color: inherit;
  }

  button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .thinking,
  .refused {
    margin: 0;
    font-size: 11px;
    color: var(--muted);
  }

  .refused {
    color: var(--warn, #c66);
  }

  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
