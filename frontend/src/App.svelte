<script lang="ts">
  import ClaudeConsole from "./components/ClaudeConsole.svelte";
  import ConfigSetup from "./components/ConfigSetup.svelte";
  import KanbanBoard from "./components/KanbanBoard.svelte";
  import OfficeCanvas from "./components/OfficeCanvas.svelte";
  import PermissionMenu from "./components/PermissionMenu.svelte";
  import SettingsMenu from "./components/SettingsMenu.svelte";
  import { Roster } from "./lib/agents/roster.svelte";
  import { TaskBoard } from "./lib/board/board.svelte";
  import { ChangeReview } from "./lib/changes/changes.svelte";
  import { ClaudeSession } from "./lib/claude/session.svelte";
  import { Config } from "./lib/config/config.svelte";
  import { Permissions } from "./lib/permissions/permissions.svelte";

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

  let showPerf = $state(false);

  // Backend events are wired once for the lifetime of the app.
  $effect(() => session.listen());
  $effect(() => board.listen());
  $effect(() => review.listen());

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

  function onKeydown(event: KeyboardEvent) {
    // Ctrl/Cmd+P toggles the performance overlay. Deliberately cheap and
    // deliberately optional.
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "p") {
      event.preventDefault();
      showPerf = !showPerf;
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<!-- Nothing is drawn while the folder is being looked up. It is one call, and
     the alternative is a flash of either the setup screen or an empty office,
     each of which says something untrue about how this app is configured. -->
{#if config.status === "missing"}
  <ConfigSetup {config} />
{:else if config.open}
  <main>
    <div class="work">
      <KanbanBoard {board} agents={roster.identities} />
      <div class="office">
        <!-- Why the office is empty, over the office it is empty in. It stayed
             here when the controls moved out: an agent folder that would not
             load is the explanation for the desks that are missing, and it
             belongs with them rather than in the corner of the conversation. -->
        {#if roster.loadError}
          <div class="load-error">{roster.loadError}</div>
        {/if}
        <OfficeCanvas {roster} {session} {board} {config} {showPerf} />
      </div>
    </div>
    <aside>
      <!-- The app's own controls live in the console's header row, which is the
           only strip in the window that is neither the work nor the watching.

           They sat over the office and had to be read against a moving canvas.
           Handed to the console as a snippet rather than placed above it: the
           row also carries what the run is doing, and that state belongs to the
           console -- two rows, one holding the controls and one holding the
           status, would be a box drawn around nothing.

           The permission mode comes before the wrench: what a run is allowed
           to do is looked at far more often than the folder agents load from. -->
      <ClaudeConsole {session} {review}>
        {#snippet controls()}
          <PermissionMenu {permissions} />
          <SettingsMenu {config} />
        {/snippet}
      </ClaudeConsole>
    </aside>
  </main>
{/if}

<style>
  main {
    display: grid;
    /* Office takes the room; the console keeps a usable, fixed-ish width. */
    grid-template-columns: minmax(0, 1fr) clamp(340px, 27%, 620px);
    /* An auto-sized row grows to its tallest child, which lets a long
       transcript push the whole window -- canvas included -- off screen.
       Pinning the row to the viewport makes the console scroll instead. */
    grid-template-rows: minmax(0, 100%);
    height: 100%;
    overflow: hidden;
  }

  .work {
    display: grid;
    /* The board takes a fixed slice off the top; the office gets the rest. */
    grid-template-rows: clamp(150px, 24%, 240px) minmax(0, 1fr);
    min-width: 0;
    min-height: 0;
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
</style>
