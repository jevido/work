<script lang="ts">
  import { untrack } from "svelte";
  import ClaudeConsole from "./components/ClaudeConsole.svelte";
  import ConfigSetup from "./components/ConfigSetup.svelte";
  import IdeaOutline from "./components/IdeaOutline.svelte";
  import KanbanBoard from "./components/KanbanBoard.svelte";
  import ModeToggle from "./components/ModeToggle.svelte";
  import OfficeCanvas from "./components/OfficeCanvas.svelte";
  import PermissionMenu from "./components/PermissionMenu.svelte";
  import PlanningList from "./components/PlanningList.svelte";
  import SettingsMenu from "./components/SettingsMenu.svelte";
  import SyncBadge from "./components/SyncBadge.svelte";
  import UpdateDialog from "./components/UpdateDialog.svelte";
  import WorkspaceDialog, { type Purpose } from "./components/WorkspaceDialog.svelte";
  import WorkspaceTabs from "./components/WorkspaceTabs.svelte";
  import { Roster } from "./lib/agents/roster.svelte";
  import { TaskBoard } from "./lib/board/board.svelte";
  import { ChangeReview } from "./lib/changes/changes.svelte";
  import { ClaudeSession } from "./lib/claude/session.svelte";
  import { Config } from "./lib/config/config.svelte";
  import { Permissions } from "./lib/permissions/permissions.svelte";
  import { AppUpdate } from "./lib/update/update.svelte";
  import { MODES, MODE_HINTS } from "./lib/workspace/model";
  import { Workspaces } from "./lib/workspace/workspaces.svelte";

  const session = new ClaudeSession();
  const board = new TaskBoard();
  const review = new ChangeReview();
  /**
   * The team. Read from the config folder at startup and again on every
   * reload, so it is held whole here rather than mapped into a snapshot per
   * consumer: a folder edited on disk has to change every label that names
   * whoever is in it.
   */
  const roster = new Roster();

  /**
   * What the agents are allowed to do.
   *
   * Work chooses a permission mode rather than inheriting whatever the local
   * Claude installation allows: nothing here can answer a permission prompt,
   * so a mode that asks is a mode that refuses. This is the user's say over
   * which one, and it is read before the office is drawn.
   */
  const permissions = new Permissions();

  /**
   * Whether a newer release of Work exists.
   *
   * Held for the session and not written down anywhere: the popup comes back
   * on the next launch if it was closed rather than acted on. See AppUpdate.
   */
  const update = new AppUpdate();

  /**
   * Where the agent folders are.
   *
   * Agents are read off disk now, so this has to be answered before there is
   * an office to draw -- and re-reading the folder is re-reading the team,
   * which is why the roster is what a reload refreshes.
   */
  const config = new Config(async () => {
    await roster.load();
    if (roster.loadError) throw new Error(roster.loadError);
    const n = roster.list.length;
    return `${n} ${n === 1 ? "agent" : "agents"}`;
  });

  /**
   * The open documents.
   *
   * Every one of them is live: its sync loop runs whether or not its tab is in
   * front, so switching tabs shows a workspace that has been keeping up rather
   * than one that starts catching up when you arrive. That is the whole reason
   * a tab bar is worth having over a document picker.
   */
  const workspaces = new Workspaces();

  let showPerf = $state(false);

  /** The workspace dialog, and what it is for. Null when it is closed. */
  let dialog = $state<{ purpose: Purpose; targetId: string | null } | null>(null);
  /** What opened it, so closing hands focus back rather than to the document. */
  let dialogOpener: HTMLElement | null = null;

  const active = $derived(workspaces.active);
  const dialogTarget = $derived.by(() => {
    const id = dialog?.targetId;
    return id ? (workspaces.list.find((w) => w.id === id) ?? null) : null;
  });

  /** The tab strip's aria-controls target, and the panel's own id. */
  const PANEL_ID = "workspace-panel";

  // Backend events are wired once for the lifetime of the app.
  $effect(() => session.listen());
  $effect(() => board.listen());
  $effect(() => review.listen());
  $effect(() => update.listen());

  /**
   * Opens whatever was open last time, and stops every sync loop on the way
   * out.
   *
   * In an effect rather than at the top of this script because it starts
   * timers and window listeners, and those need somewhere to be cleaned up.
   *
   * Untracked, and it has to be: restoring writes the workspace list and then
   * reads it back -- to pick which tab is in front, and to start a loop for
   * each. Reading state this effect has just written makes the effect depend
   * on it, so it re-runs, restores again, and does not stop. Without the
   * untrack this is Svelte's effect_update_depth_exceeded on launch, and an
   * app that draws nothing at all.
   */
  $effect(() => {
    untrack(() => {
      workspaces.restore();
      if (workspaces.list.length === 0) {
        // A first run has no tabs, and an app whose main surface is an empty
        // strip with a "New" button is an app that asks a question before it
        // has shown what it is. One local workspace costs nothing and needs
        // no server; sharing it later loses nothing. It opens on the office,
        // for the reason in Workspaces.bootstrap.
        workspaces.bootstrap();
      }
    });
    return () => workspaces.dispose();
  });

  /**
   * Writes everything down, 800ms after the last change.
   *
   * The cleanup cancels the pending write, so a burst of typing rearms rather
   * than saving per keystroke -- and the stamp it watches is a string of
   * counters, so noticing a change does not mean walking every outline.
   */
  $effect(() => {
    void workspaces.stamp;
    return workspaces.scheduleSave();
  });

  // Reads nothing reactive, so this runs once.
  $effect(() => {
    void config.probe();
  });

  // The mode does not depend on the config folder -- it is about what a run
  // may do, not about who is on the team -- so it is asked for straight away.
  $effect(() => {
    void permissions.load();
  });

  // The team is only worth asking for once the folder it lives in is settled.
  // `config.open` goes true once and stays true, so this reads as "load the
  // roster when we are allowed to" and runs a single time.
  $effect(() => {
    if (!config.open) return;
    void roster.load();
  });

  // Changes already on disk when the window opened.
  //
  // The review lives behind a button now, and a button is the only thing that
  // says there is anything to review -- so it has to be right on a window that
  // opened into the middle of a run, not just on one that watched the run
  // happen. `run:changes` only fires when something changes, so without this a
  // dev reload mid-run leaves the header claiming the run touched nothing.
  $effect(() => {
    if (!config.open) return;
    void review.refresh();
  });

  // The console labels turns and plan steps by agent, so it follows the roster
  // rather than being handed a copy of it at startup.
  $effect(() => session.setAgents(roster.identities));

  function openDialog(purpose: Purpose, targetId: string | null, from?: EventTarget | null) {
    dialogOpener = from instanceof HTMLElement ? from : (document.activeElement as HTMLElement);
    workspaces.error = null;
    dialog = { purpose, targetId };
  }

  function closeDialog() {
    dialog = null;
    // The opener can have been unmounted by what the dialog did -- joining a
    // workspace replaces the tab strip. Falling back to nothing would drop the
    // caret at the top of the document.
    const back = dialogOpener?.isConnected ? dialogOpener : null;
    dialogOpener = null;
    back?.focus();
  }

  function onKeydown(event: KeyboardEvent) {
    // Ctrl/Cmd+P toggles the performance overlay. Deliberately cheap and
    // deliberately optional.
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "p") {
      event.preventDefault();
      showPerf = !showPerf;
      return;
    }

    // Ctrl/Cmd+1..3 switches mode without leaving the outline.
    //
    // The toggle is a radio group and already has its own arrow keys, but
    // reaching it means leaving whatever you are typing in -- and the whole
    // point of the three modes is that they are the same work seen three ways,
    // so moving between them should not cost the caret.
    if ((event.ctrlKey || event.metaKey) && !event.shiftKey && !event.altKey) {
      const at = Number(event.key) - 1;
      const mode = MODES[at];
      if (mode && active) {
        event.preventDefault();
        active.mode = mode;
      }
    }
  }
</script>

<svelte:window
  onkeydown={onKeydown}
  onpagehide={() => workspaces.save()}
/>

<!-- Nothing is drawn while the folder is being looked up. It is one call, and
     the alternative is a flash of either the setup screen or an empty office,
     each of which says something untrue about how this app is configured. -->
{#if config.status === "missing"}
  <ConfigSetup {config} />
{:else if config.open}
  <main>
    <WorkspaceTabs
      {workspaces}
      panelId={PANEL_ID}
      onnew={() => openDialog("new", null)}
      onjoin={() => openDialog("join", null)}
    />

    <div class="body">
      <!-- A div rather than a section: `tabpanel` is the role, and a section
           already carries one of its own that would have to be overridden. -->
      <div
        class="workspace"
        id={PANEL_ID}
        role="tabpanel"
        aria-labelledby={active ? `workspace-tab-${active.id}` : undefined}
        tabindex="-1"
      >
        {#if active}
          <div class="modebar">
            <!--
              Keyed on the workspace, like the panes below.

              A radio's checked state lives in the DOM, and renaming a live
              radio takes it out of its group -- WebKit clears it. Svelte then
              will not put it back, because from its side the expression
              (`mode === option`) did not change: coming back to a tab left in
              Planning found Planning still true and the DOM already
              unchecked. The result was a segmented control with nothing
              selected sitting above the mode it was supposed to be showing.
              A fresh group per workspace cannot drift.
            -->
            {#key active.id}
              <ModeToggle bind:mode={active.mode} group={active.id} />
            {/key}
            <!-- What the mode is for, in the strip that switches it. The
                 toggle already says which one you are in, so repeating the
                 name here would be the same word twice; this says what you
                 came here to do. -->
            <p class="what">{MODE_HINTS[active.mode]}</p>
            <SyncBadge
              workspace={active}
              onfix={(reason) =>
                openDialog(reason === "rekey" ? "rekey" : "share", active.id)}
            />
          </div>

          <!--
            The three modes, stacked in one grid cell.

            Work is always in the DOM and the other two are not, and that
            asymmetry is the point of the whole layout. The office is a picture
            of a run that carries on while you plan: unmounting it would
            restart the picture rather than the run -- everybody back at their
            opening position, every monitor blank, the walk you were watching
            gone -- and the desk buttons are positioned in pixels measured off
            a laid-out canvas, so a collapsed box comes back with the office
            drawn for a window nothing is. So work keeps its box and stops
            drawing; see OfficeCanvas's `shown`.

            Idea and planning have nothing running behind them. Their state is
            the workspace's, which outlives them, and the one thing they do own
            -- where the caret is -- is better forgotten than restored to a
            line somebody else has since moved.
          -->
          <div class="panes">
            <!-- Keyed on the workspace, so switching tabs builds a new outline
                 rather than handing the old one a different document. What
                 these two own is the caret and, in the outline's case, a map
                 of which input belongs to which line -- both of which are
                 about the workspace you were in, not the one you moved to. -->
            {#key active.id}
              {#if active.mode === "idea"}
                <div class="pane">
                  <IdeaOutline workspace={active} />
                </div>
              {:else if active.mode === "planning"}
                <div class="pane">
                  <PlanningList workspace={active} />
                </div>
              {/if}
            {/key}

            <div class="pane work" class:hidden={active.mode !== "work"}>
              <KanbanBoard {board} agents={roster.identities} />
              <div class="office">
                <!-- Why the office is empty, over the office it is empty in.
                     An agent folder that would not load is the explanation for
                     the desks that are missing, and it belongs with them. -->
                {#if roster.loadError}
                  <div class="load-error">{roster.loadError}</div>
                {/if}
                <OfficeCanvas
                  {roster}
                  {session}
                  {board}
                  {config}
                  {showPerf}
                  shown={active.mode === "work"}
                />
              </div>
            </div>
          </div>
        {:else}
          <div class="no-workspace">
            <p>No workspace open.</p>
            <div class="row">
              <button
                class="primary"
                onclick={(event) => openDialog("new", null, event.currentTarget)}
              >
                New workspace
              </button>
              <button
                class="ghost"
                onclick={(event) => openDialog("join", null, event.currentTarget)}
              >
                Join one by key
              </button>
            </div>
          </div>
        {/if}
      </div>

      <aside>
        <!-- The console is outside the mode stack, and stays mounted for the
             life of the window.

             It is the conversation, not a view of the workspace: a run started
             in work mode is still going while you are writing an outline, and
             unmounting the console would drop the transcript, the composer's
             half-typed prompt and the scroll position every time somebody
             looked at the plan. Its state lives in ClaudeSession either way --
             this is about the DOM, and about the reply that is streaming into
             it right now.

             The app's own controls live in its header row, which is the only
             strip in the window that is neither the work nor the watching. -->
        <ClaudeConsole {session} {review}>
          {#snippet controls()}
            <PermissionMenu {permissions} />
            <SettingsMenu {config} />
          {/snippet}
        </ClaudeConsole>
      </aside>
    </div>
  </main>
{/if}

{#if dialog}
  <WorkspaceDialog
    purpose={dialog.purpose}
    {workspaces}
    target={dialogTarget}
    onclose={closeDialog}
  />
{/if}

<!-- Outside the config branches on purpose. A new release is news about the
     app, not about the folder the agents live in, so it reaches someone still
     on the setup screen as well as someone with an office open. It is fixed
     and draws over whichever of the two is behind it. -->
{#if update.showing}
  <UpdateDialog {update} />
{/if}

<style>
  main {
    display: grid;
    /* The tab strip takes its own height off the top; everything else shares
       what is left. */
    grid-template-rows: auto minmax(0, 1fr);
    height: 100%;
    overflow: hidden;
  }

  .body {
    display: grid;
    /* The workspace takes the room; the console keeps a usable, fixed-ish
       width. */
    grid-template-columns: minmax(0, 1fr) clamp(340px, 27%, 620px);
    /* An auto-sized row grows to its tallest child, which lets a long
       transcript push the whole window -- canvas included -- off screen.
       Pinning the row to the viewport makes the console scroll instead. */
    grid-template-rows: minmax(0, 100%);
    min-height: 0;
    overflow: hidden;
  }

  .workspace {
    display: grid;
    grid-template-rows: auto minmax(0, 1fr);
    min-width: 0;
    min-height: 0;
  }

  .workspace:focus {
    outline: none;
  }

  .modebar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 5px 10px;
    border-bottom: 1px solid var(--line);
    background: var(--panel);
  }

  .what {
    margin: 0;
    margin-right: auto;
    color: var(--muted);
    font-size: 11px;
  }

  /*
   * One cell, every mode in it.
   *
   * Stacking rather than swapping so that whichever mode is on screen gets
   * exactly the box the office has -- the office has to stay laid out at its
   * real size while it is hidden, because its desk buttons are pixel
   * positions measured off that layout.
   */
  .panes {
    display: grid;
    grid-template-areas: "pane";
    min-width: 0;
    min-height: 0;
  }

  .pane {
    grid-area: pane;
    min-width: 0;
    min-height: 0;
  }

  /*
   * `visibility: hidden`, not `display: none`.
   *
   * Both stop the paint. Only this one keeps the box, and the box is the
   * point: the office measures itself from it. It also takes everything
   * inside out of the tab order and out of the accessibility tree, which is
   * what stops focus landing on a desk in a room that is not drawn.
   */
  .pane.hidden {
    visibility: hidden;
  }

  .work {
    display: grid;
    /* The board takes a fixed slice off the top; the office gets the rest. */
    grid-template-rows: clamp(150px, 24%, 240px) minmax(0, 1fr);
  }

  .office {
    position: relative;
    min-width: 0;
    min-height: 0;
  }

  aside {
    min-width: 0;
    min-height: 0;
  }

  .load-error {
    position: absolute;
    /* Above the desk panel, which is bottom-left and cannot reach this corner
       -- but can be dragged there. */
    z-index: 3;
    top: 10px;
    right: 10px;
    max-width: min(340px, 50vw);
    padding: 6px 10px;
    border: 1px solid #4a2b2b;
    border-radius: 6px;
    background: #241a1a;
    color: var(--err);
    font-size: 12px;
  }

  .no-workspace {
    display: grid;
    align-content: center;
    justify-items: center;
    gap: 12px;
    grid-row: span 2;
    color: var(--muted);
  }

  .no-workspace p {
    margin: 0;
  }

  .no-workspace .row {
    display: flex;
    gap: 8px;
  }

  .primary,
  .ghost {
    padding: 6px 14px;
    border-radius: 5px;
    font: inherit;
    cursor: pointer;
  }

  .primary {
    border: 1px solid var(--accent);
    background: var(--accent);
    color: #1a1408;
    font-weight: 600;
  }

  .ghost {
    border: 1px solid var(--line);
    background: var(--panel-2);
    color: var(--text);
  }

  :focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
</style>
