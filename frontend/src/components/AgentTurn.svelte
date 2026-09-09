<script lang="ts">
  import type { AgentEntry, Phase } from "../lib/claude/session.svelte";
  import ToolRow from "./ToolRow.svelte";

  let { entry, showName = true }: { entry: AgentEntry; showName?: boolean } = $props();

  // Only the synthesis turn is worth labelling: a plain work turn is obvious
  // from the agent's name, and routing has an entry of its own. A side-channel
  // answer is labelled because it is the one turn that is not the work: read
  // unlabelled next to a run, it would look like part of it.
  const phaseLabel: Record<Phase, string> = {
    plan: "",
    work: "",
    synthesis: "bringing it together",
    chat: "answering while the work runs",
  };

  const phase = $derived(phaseLabel[entry.phase]);
  const streaming = $derived(entry.status === "streaming");
</script>

<article class="turn" data-status={entry.status}>
  <!-- Skipped entirely when there is nothing to put in it: an empty header
       still costs a line, which reads as a gap between turns. -->
  {#if showName || phase || streaming}
    <div class="speaker">
      {#if showName}
        <span class="dot" style:background={entry.colour}></span>
        <span class="name">{entry.agentName}</span>
      {/if}
      {#if phase}
        <span class="aside">{phase}</span>
      {/if}
      {#if streaming}
        <span class="pulse" style:background={entry.colour}></span>
      {/if}
    </div>
  {/if}

  {#each entry.parts as part (part.id)}
    {#if part.kind === "text"}
      {#if part.text.trim()}<pre>{part.text}</pre>{/if}
    {:else}
      <div class="tools"><ToolRow call={part.call} /></div>
    {/if}
  {/each}

  {#if entry.error}
    <div class="error" role="alert">{entry.error}</div>
  {/if}

  {#if !streaming && entry.durationMs > 0}
    <div class="footnote">
      {(entry.durationMs / 1000).toFixed(1)}s
      {#if entry.costUsd > 0}· ${entry.costUsd.toFixed(4)}{/if}
    </div>
  {/if}
</article>

<style>
  .turn {
    margin-bottom: 16px;
  }

  .speaker {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 5px;
  }

  .name {
    font-weight: 600;
    font-size: 12px;
  }

  .aside {
    color: var(--muted);
    font-size: 11px;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex: none;
  }

  .pulse {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    animation: blink 1s steps(2, end) infinite;
  }

  @keyframes blink {
    50% {
      opacity: 0.15;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .pulse {
      animation: none;
      opacity: 0.6;
    }
  }

  /* Tool rows sit in the same left-hand channel as the prose, so a turn reads
     as one column of activity. */
  .tools {
    padding-left: 14px;
    border-left: 1px solid var(--line);
  }

  pre {
    margin: 0;
    padding-left: 14px;
    border-left: 1px solid var(--line);
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .error {
    margin: 6px 0 0 14px;
    padding: 7px 9px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
    user-select: text;
  }

  .footnote {
    padding: 5px 0 0 14px;
    color: var(--muted);
    font-size: 11px;
  }
</style>
