<script lang="ts">
  import type { AgentStatus } from "../../bindings/dev.jevido/work/internal/workbench/models.js";
  import { readFolder, type AgentFolder } from "../lib/agents/profile";
  import type { Config } from "../lib/config/config.svelte";

  let {
    agent,
    config,
    avatar,
  }: {
    agent: AgentStatus;
    config: Config;
    /** Where to fetch their picture, or null when the folder holds none. */
    avatar: string | null;
  } = $props();

  /**
   * The folder as it reads now, or null while it is being read.
   *
   * Read here rather than taken from the roster the panel already has. The
   * roster is only as new as the last reload, and the person opening this may
   * have edited the file a moment ago -- in the one view whose whole job is to
   * show that file, a stale copy of it would read as a bug. It is two small
   * local reads on a click, so nothing is saved by keeping the old answer.
   */
  let folder = $state<AgentFolder | null>(null);

  /** Why the folder could not be read, if it could not be. */
  let error = $state<string | null>(null);

  $effect(() => {
    const id = agent.id;
    let live = true;
    folder = null;
    error = null;
    readFolder(id)
      .then((read) => {
        if (live) folder = read;
      })
      .catch((err) => {
        if (live) error = messageOf(err);
      });
    // Switching desks with the panel open starts a second read. Without this
    // whichever finished last would win, and that need not be this agent's.
    return () => {
      live = false;
    };
  });

  function messageOf(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  /**
   * What the coordinator routes this agent on, and where it comes from.
   *
   * Two sources, decided in Go so this does not re-derive the rule: a built-in
   * agent carries a skillset Work wrote, and an agent that arrived as a folder
   * has only the first prose line of their PERSONALITY.md.
   */
  const skillset = $derived(folder?.skillset ?? []);
  const skills = $derived(folder?.skills ?? []);

  /**
   * A URL that would not load, so it is not asked for a second time.
   *
   * Tracked as the URL rather than a flag: the next agent, or the next reload
   * of this one, is a different URL and deserves its own attempt.
   */
  let failed = $state<string | null>(null);

  /**
   * The portrait to show, if there is one to show.
   *
   * No avatar is the ordinary case -- most folders have none -- so this shows
   * nothing at all rather than a placeholder for a picture that was never
   * missing. A file that has gone since the folder was read is the same thing
   * to look at.
   */
  const portrait = $derived(avatar && avatar !== failed ? avatar : null);
</script>

<!-- Read-only, on purpose.

     An agent is a folder on disk now: PERSONALITY.md, skills/, an avatar. A
     form in here that wrote back would be a second source of truth for the
     same person -- edit them at their desk, press Reload config, and the files
     would quietly win. So this shows what the folder currently says and points
     at where to change it, which is the editor.

     It replaces the thread rather than sitting over it, the way the form did:
     the panel is narrow, and the header, the dot and "working" stay where they
     were so this agent's run carries on being watched behind it. -->
<div class="profile" role="document">
  <div class="body">
    <!-- The picture sits with the line that says what they are, not above the
         panel as a banner: this view answers "who is this", and their face and
         their role are the same answer. -->
    <div class="ident">
      {#if portrait}
        <!-- alt is empty on purpose: the name is in the panel header two rows
             up, and "Anton" read out twice is worse than not at all. -->
        <img
          class="portrait"
          src={portrait}
          alt=""
          style:border-color={agent.colour}
          onerror={() => (failed = avatar)}
        />
      {/if}
      <p class="role">
        {agent.title}
        <span class="sep">·</span>
        {agent.role}
        {#if agent.model}
          <span class="sep">·</span>
          <span class="model">{agent.model}</span>
        {/if}
      </p>
    </div>

    {#if error}
      <!-- A folder that exists but will not be read is the one thing here
           worth interrupting for: everything under this label would otherwise
           read as "the file is empty", which is a different problem. -->
      <p class="error" role="alert">Could not read this agent's folder: {error}</p>
    {:else if !folder}
      <!-- Two small local reads, so this is normally a single frame. It says
           what it is waiting for rather than showing empty sections that are
           about to fill in. -->
      <p class="empty">Reading their folder…</p>
    {:else}
      <div class="field">
        <span class="label">Routing</span>
        {#if skillset.length > 0}
          <div class="chips">
            {#each skillset as skill (skill)}
              <span class="chip">{skill}</span>
            {/each}
          </div>
          <!-- Worth saying once: this is the part that moves work around, which
               is not something anybody would guess from a row of words. -->
          <p class="hint">Anton routes work by these.</p>
        {:else if folder.blurb}
          <p class="prose">{folder.blurb}</p>
          <p class="hint">
            The first prose line of <code>PERSONALITY.md</code>, and all Anton
            knows about them when he decides who gets a task.
          </p>
        {:else}
          <p class="empty">
            Nothing for Anton to route on. Give them a first line in
            <code>PERSONALITY.md</code>.
          </p>
        {/if}
      </div>

      <div class="field">
        <span class="label">Skills</span>
        {#if folder.skills.length > 0}
          <div class="chips">
            {#each folder.skills as skill (skill)}
              <span class="chip">{skill}</span>
            {/each}
          </div>
        {:else}
          <p class="empty">Nothing in <code>skills/</code> yet.</p>
        {/if}
      </div>

      <div class="field" class:last={!agent.experience}>
        <span class="label">Personality</span>
        {#if folder.personality}
          <!-- The heading is gone: it is their name, and the panel header two
               rows up already says it. -->
          <p class="prose">{folder.personality}</p>
        {:else}
          <p class="empty">Nothing in <code>PERSONALITY.md</code> yet.</p>
        {/if}
      </div>

      <!-- Only when there is one. Nothing on disk sets this, so on a
           folder-defined agent an "Experience: not set" row would be a label
           for a field they cannot fill in. -->
      {#if agent.experience}
        <div class="field last">
          <span class="label">Experience</span>
          <p class="prose">{agent.experience}</p>
        </div>
      {/if}
    {/if}
  </div>

  <!-- The one actionable thing on a read-only panel: where the files are, and
       the two words that make an edit show up in the office. -->
  <div class="footer">
    {#if folder?.dir}
      <!-- Their own folder rather than the config root: it is the path that
           can be opened to make the edit this sentence is asking for, and it
           is what the panel above was just read from.

           On its own line rather than inside the sentence: a long one has to
           break mid-token to fit this panel, and doing that in the middle of a
           clause makes the sentence hard to read as well. -->
      <p>Edit these files, then <strong>Reload config</strong>.</p>
      <p class="path">{folder.dir}</p>
    {:else if config.path}
      <p>Edit this agent's folder, then <strong>Reload config</strong>.</p>
      <p class="path">{config.path}</p>
    {:else}
      <p>Agents are read from files on disk. Edit them there, then reload.</p>
    {/if}
  </div>
</div>

<style>
  .profile {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 12px;
  }

  /* One row, so a portrait and the role line read as one identity rather than
     as a picture with a caption under it. Without a picture it collapses to
     exactly the line that was here before. */
  .ident {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 14px;
  }

  /* Circular and ringed in the agent's colour, the same way the office draws
     it on their shoulders: the panel and the desk have to be the same person.
     object-fit keeps a picture at any aspect ratio a face rather than a
     stretched one. */
  .portrait {
    width: 42px;
    height: 42px;
    flex: none;
    border-radius: 50%;
    border: 1.5px solid var(--line);
    object-fit: cover;
    background: var(--panel-2);
  }

  .role {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }

  .sep {
    opacity: 0.5;
    padding: 0 2px;
  }

  .model {
    font-family: ui-monospace, monospace;
    font-size: 11px;
  }

  .field {
    display: grid;
    gap: 5px;
    margin-bottom: 14px;
  }

  .field.last {
    margin-bottom: 0;
  }

  .label {
    color: var(--muted);
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }

  /* The same pill the board and the task panel use, carrying a word instead of
     a state. */
  .chip {
    padding: 1px 9px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--panel-2);
    font-size: 11.5px;
    user-select: text;
  }

  .prose {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }

  .hint,
  .empty {
    margin: 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  /* Louder than .empty because it is a different claim: the file was not read,
     rather than read and found to say nothing. */
  .error {
    margin: 0;
    color: var(--err);
    font-size: 11.5px;
    line-height: 1.45;
    user-select: text;
  }

  /* Unpadded, like the setup screen's: box padding leaves a gap in front of
     the punctuation that follows a filename. */
  code {
    color: var(--text);
    font-family: ui-monospace, monospace;
    font-size: 0.92em;
  }

  .footer {
    padding: 9px 12px 11px;
    border-top: 1px solid var(--line);
  }

  .footer p {
    margin: 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .footer strong {
    color: var(--text);
    font-weight: 600;
  }

  .path {
    margin-top: 4px;
    font-family: ui-monospace, monospace;
    word-break: break-all;
    user-select: text;
  }
</style>
