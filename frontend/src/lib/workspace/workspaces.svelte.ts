/**
 * The open tabs, and which one is in front.
 *
 * This used to be N independent workspaces, each with its own HTTP transport,
 * its own outbox and its own push loop. It is not any more, and that is the
 * whole point of this file: **there is one sync loop and it is in Go.** The
 * workbench owns the queue, the merge and the keys; it fsyncs, it survives the
 * webview's data being cleared, and it keeps pushing while this window is
 * doing something else. A second loop up here pushing the same ops to the same
 * server was not a redundancy, it was two writers with two clocks.
 *
 * So everything below is a *view* of what `WorkbenchService` reports, plus the
 * calls that ask it to change. Nothing here talks to a server.
 *
 * One thing is still this side's, and is marked as such wherever it appears:
 * the Idea and Planning outline. It is a local document per tab and does not
 * leave this machine. That is no longer for want of somewhere to send it --
 * `ApplyWorkspaceEdits` writes nodes through Go's queue now -- it is work not
 * yet done. See workspace.svelte.ts, which says what moving onto it involves.
 */
import { Events } from "@wailsio/runtime";
import type {
  Status,
  WorkspaceView,
} from "../../../bindings/dev.jevido/work/internal/workbench/models.js";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import { WORKSPACE_CHANGED, WORKSPACE_SYNC } from "../bridge/events";
import { DEFAULT_SERVER, looksLikeReadKey, parseInvite } from "./invite";
import { Workspace } from "./workspace.svelte";
import * as Storage from "./storage";

/** How long after the last change to write the local outlines down. */
const SAVE_AFTER_MS = 800;

/**
 * The tab that exists when no workspace has been joined.
 *
 * Go has no tabs at all until a workspace exists -- `NewTab` answers "no
 * workspace joined" -- and Work has always run perfectly well without one. So
 * an unjoined machine gets exactly one tab here, with a fixed id, and it is
 * the app as it has always been: local outline, local board, nothing on a
 * wire. Joining replaces it with the workspace's own tabs.
 */
export const LOCAL_TAB = "local";

/** The badge's five states, derived from what Go reports. */
export type SyncState = "local" | "synced" | "syncing" | "offline" | "rejected";

export class Workspaces {
  /**
   * The tabs, in Go's order, each carrying its local outline.
   *
   * Instances are reused across updates by id -- see `#adopt`. A workspace
   * that was replaced on every `workspace:changed` would throw away the
   * outline, the drafts and the mode of every tab each time the sync status
   * moved.
   */
  list = $state<Workspace[]>([]);

  /** The tab being looked at. Always settable; see `select`. */
  activeId = $state<string | null>(null);

  active = $derived<Workspace | null>(
    this.list.find((w) => w.id === this.activeId) ?? this.list[0] ?? null,
  );

  /** Whether this build can talk to a workspace server at all. */
  available = $state(false);

  /** The joined workspace, or null. Null is the ordinary case. */
  view = $state<WorkspaceView | null>(null);

  joined = $derived(this.view !== null);

  /** Where sync stands, workspace-wide. Go's, not ours. */
  status = $state<Status | null>(null);

  /**
   * Which tab agents actually run in.
   *
   * Not necessarily the tab on screen. `ActivateTab` is refused while a run is
   * in flight and refused for a tab with no folder on this machine, and
   * neither of those is a reason to stop somebody looking at another tab. When
   * the two differ, the difference is shown rather than hidden -- a tab strip
   * that silently runs work somewhere other than where you are looking is the
   * worst version of this.
   */
  runsIn = $derived<string | null>(this.view?.activeTab || null);

  /** Why the last create, join or tab change did not work. */
  error = $state<string | null>(null);

  /** True while a call is in flight. */
  busy = $state(false);

  /** The server to prefill: the one in use, else the last default. */
  lastServer = $state(DEFAULT_SERVER);

  /**
   * The badge's state.
   *
   * "local" is a machine that has joined nothing, which is a normal way to run
   * Work and not a degraded one. The other four are Go's `Status.state`.
   */
  state = $derived<SyncState>(
    !this.status?.joined
      ? "local"
      : this.status.state === "rejected"
        ? "rejected"
        : this.status.state === "offline"
          ? "offline"
          : this.status.state === "syncing" || this.status.pending > 0
            ? "syncing"
            : "synced",
  );

  /** Ops that have not reached the server. Workspace-wide, because Go's is. */
  pending = $derived(this.status?.pending ?? 0);

  /**
   * How many ops behind the server this machine is.
   *
   * Exact, not an estimate: sequence numbers are gapless, so head minus cursor
   * is the count. See server/README.md.
   */
  behind = $derived(Math.max(0, (this.status?.head ?? 0) - (this.status?.cursor ?? 0)));

  /** Ops dropped because the outbox hit its cap. Non-zero means a hole. */
  dropped = $derived(this.status?.dropped ?? 0);

  /** Go's last sync failure, or null. */
  syncError = $derived(this.status?.error?.trim() || null);

  /**
   * A cheap summary of what a save would write.
   *
   * Revisions and counts rather than documents: the save is debounced on this
   * changing, and reading the outlines to notice one changed would walk every
   * tree on every keystroke.
   */
  stamp = $derived(
    [this.activeId, ...this.list.map((w) => `${w.id}:${w.mode}:${w.revision}`)].join("|"),
  );

  /**
   * Subscribes to the backend and reads the first paint.
   *
   * Returns the unsubscribe, so the caller's effect has something to clean up.
   * The event carries the same values these three calls return -- they are
   * here because an event only fires when something changes, and a window that
   * opened into an already-joined workspace has to draw it.
   */
  start(): () => void {
    // The local tab, now, before anything is awaited. Every call below is a
    // round trip, and a frame with no tab in it is a frame of tab strip over
    // blank panel -- which is what an app with nothing in it looks like, on
    // every launch, for as long as the IPC takes.
    this.#adopt(null);

    const offChanged = Events.On(WORKSPACE_CHANGED, (e) => {
      const payload = e.data as { workspace?: WorkspaceView | null; status?: Status };
      this.#adopt(payload?.workspace ?? null);
      if (payload?.status) this.status = payload.status;
    });
    const offSync = Events.On(WORKSPACE_SYNC, (e) => {
      const payload = e.data as { status?: Status };
      if (payload?.status) this.status = payload.status;
    });

    void this.#firstPaint();

    return () => {
      offChanged();
      offSync();
    };
  }

  async #firstPaint(): Promise<void> {
    try {
      this.available = await Workbench.Workspaces();
    } catch {
      // An older backend, or one built without a transport. The workspace
      // controls stay hidden rather than offering buttons whose only outcome
      // is an error.
      this.available = false;
    }
    try {
      this.#adopt(await Workbench.Workspace());
    } catch {
      this.#adopt(null);
    }
    try {
      this.status = await Workbench.SyncStatus();
    } catch {
      this.status = null;
    }
  }

  /**
   * Rebuilds the tab list from what Go says, keeping the workspaces that are
   * still there.
   *
   * Reuse by id is load-bearing twice over. It keeps each tab's local outline,
   * drafts and mode across every status update -- and because the id is what
   * the `{#each}` and the `{#key}` in App.svelte are keyed on, it is also what
   * keeps the same textarea, the same canvas and a checked mode toggle through
   * a change that only moved a pending count.
   */
  #adopt(view: WorkspaceView | null): void {
    this.view = view;
    if (view?.serverUrl) this.lastServer = view.serverUrl;

    const existing = new Map(this.list.map((w) => [w.id, w]));
    const wanted: { id: string; name: string; bound: boolean }[] = view
      ? (view.tabs ?? []).map((tab) => ({
          id: tab.id,
          name: tab.name?.trim() || "Untitled tab",
          bound: tab.bound,
        }))
      : // Unjoined: the one local tab, which is the app as it has always been.
        [{ id: LOCAL_TAB, name: "Workspace", bound: true }];

    const next: Workspace[] = [];
    for (const want of wanted) {
      const found = existing.get(want.id);
      const workspace = found ?? Storage.open(want.id);
      workspace.name = want.name;
      workspace.bound = want.bound;
      next.push(workspace);
      existing.delete(want.id);
    }

    // Tabs that went away -- closed here, or retired by a colleague. Their
    // timers are stopped; what they had written down is left on disk, because
    // a tab a colleague closed is not a reason to destroy the notes somebody
    // took in it.
    for (const gone of existing.values()) gone.dispose();

    this.list = next;
    if (!next.some((w) => w.id === this.activeId)) {
      // The tab that was in front last time, then the one agents run in, then
      // the first. Never null while there is a tab: a panel with nothing
      // behind it is a blank screen under a tab strip.
      const remembered = Storage.lastActive();
      this.activeId =
        (remembered && next.some((w) => w.id === remembered) ? remembered : null) ??
        (view?.activeTab && next.some((w) => w.id === view.activeTab) ? view.activeTab : null) ??
        next[0]?.id ??
        null;
    }
  }

  /**
   * Looks at a tab, and asks the backend to run in it.
   *
   * The two are separate on purpose and in that order. Looking is instant and
   * cannot fail. Running there can: `ActivateTab` is refused mid-run, and
   * refused for a tab with no folder bound on this machine. A refusal leaves
   * the tab you clicked on screen -- taking it away again would be the app
   * undoing a click it had already drawn -- and says why, next to the tab.
   */
  select(id: string): void {
    if (!this.list.some((w) => w.id === id)) return;
    this.activeId = id;
    this.error = null;
    if (!this.joined || id === this.runsIn) return;
    void this.#run(() => Workbench.ActivateTab(id));
  }

  /** Points a tab at a project folder on this machine, with the platform picker. */
  async bind(id: string): Promise<boolean> {
    const path = await this.#run(() => Workbench.BindTabFolder(id));
    // An empty path with no error is a cancelled dialog, which is not a
    // failure and must not read as one.
    return typeof path === "string" && path !== "";
  }

  /** Opens a new tab in the joined workspace. */
  async newTab(name: string): Promise<boolean> {
    const made = await this.#run(() => Workbench.NewTab(name.trim() || "Untitled"));
    if (!made) return false;
    this.activeId = made.id;
    return true;
  }

  /**
   * Retires a tab.
   *
   * For everyone, not just here: `CloseTab` tombstones the tab node, so it
   * goes from every machine in the workspace. Whatever asks before calling
   * this has to say that, because nothing about a × on a tab suggests it.
   */
  async close(id: string): Promise<boolean> {
    if (!this.joined) return false;
    const done = await this.#run(() => Workbench.CloseTab(id));
    return done !== null;
  }

  rename(id: string, name: string): void {
    // Local, and only local. A tab's name is a field on its node in Go's
    // document; `ApplyWorkspaceEdits` could now write it, and this does not
    // call it, so a rename here shows a name the next `workspace:changed`
    // silently takes away. Nothing in the UI offers this yet -- see
    // WorkspaceTabs, where the rename key is deliberately absent -- and it
    // should either be wired to that call or removed.
    const workspace = this.list.find((w) => w.id === id);
    if (workspace) workspace.name = name;
  }

  /** Makes a workspace on a server and joins it. Returns the read key. */
  async createShared(
    base: string,
    signupToken: string,
    name: string,
  ): Promise<{ readKey: string } | null> {
    const view = await this.#run(
      () => Workbench.CreateWorkspace(base.trim(), signupToken.trim(), name.trim() || "Untitled"),
      explainCreate,
    );
    if (!view) return null;
    this.#adopt(view);
    // The read key is not on the view -- it never rides along on a payload the
    // frontend fetches as a matter of course -- so it is asked for by name.
    const keys = await this.#run(() => Workbench.WorkspaceKeys());
    return { readKey: keys?.readKey ?? "" };
  }

  /**
   * Joins an existing workspace by key.
   *
   * A read key is refused here, before the round trip and before anything
   * changes on screen. The backend refuses it too -- `JoinWorkspace` checks
   * `access` against the server -- and this is the same answer said sooner: a
   * tab that opened, loaded somebody's board and then quietly refused every
   * edit looks exactly like one that is still loading, which is a state this
   * app has for real.
   */
  async join(input: string, fallbackBase: string): Promise<boolean> {
    const invite = parseInvite(input);
    if (!invite) {
      this.error = "That does not look like a key or a share link.";
      return false;
    }
    if (looksLikeReadKey(invite.key)) {
      this.error =
        "That is a read key. It can read the workspace and not change it — open it in the viewer, or ask for a write key.";
      return false;
    }
    const base = invite.base ?? fallbackBase.trim();
    if (!base) {
      this.error = "Which server is that key for?";
      return false;
    }

    const view = await this.#run(() => Workbench.JoinWorkspace(base, invite.key));
    if (!view) return false;
    this.#adopt(view);
    return true;
  }

  /**
   * Points this machine at a different key. The way out of a rejection.
   *
   * Joining again, because that is what it is: `JoinWorkspace` swaps the sync
   * loop over and leaves the outbox on disk, so ops that never got out under
   * the old key go out under the new one.
   */
  async rekey(input: string, fallbackBase: string): Promise<boolean> {
    return this.join(input, fallbackBase);
  }

  /** Stops syncing and goes back to running purely locally. */
  async leave(): Promise<boolean> {
    const done = await this.#run(() => Workbench.LeaveWorkspace());
    if (done === null) return false;
    this.#adopt(null);
    return true;
  }

  /** Pushes and pulls now instead of waiting for the poll. The offline retry. */
  retry(): void {
    void this.#run(() => Workbench.SyncNow());
  }

  /** Writes the local outlines down now. */
  save(): void {
    Storage.save(this.list, this.activeId);
  }

  /**
   * Arms the debounced save.
   *
   * Called from an effect that reads `stamp`, so a change cancels the pending
   * timer and sets a new one -- a burst of typing writes once at the end
   * rather than once per character. Returns the cancel, which is the effect's
   * cleanup.
   */
  scheduleSave(): () => void {
    const timer = setTimeout(() => this.save(), SAVE_AFTER_MS);
    return () => clearTimeout(timer);
  }

  /** Stops every tab's timers. For the app going away. */
  dispose(): void {
    for (const workspace of this.list) workspace.dispose();
  }

  /**
   * Runs a backend call with the busy flag and one place that turns a failure
   * into a sentence.
   *
   * @param describe How to word a failure. Creating a workspace and joining
   *   one fail with the same 403 and it means entirely different things --
   *   one is the signup token, the other the workspace key -- so telling
   *   somebody to check their key when they mistyped a signup token sends them
   *   to look at the wrong field.
   */
  async #run<T>(
    call: () => Promise<T>,
    describe: (err: unknown) => string = explain,
  ): Promise<T | null> {
    if (this.busy) return null;
    this.busy = true;
    this.error = null;
    try {
      // void is what a Go method with no return comes back as. Normalised to
      // a non-null marker so callers can tell "it worked" from "it failed".
      const value = await call();
      return (value ?? (true as unknown)) as T;
    } catch (err) {
      this.error = describe(err);
      return null;
    } finally {
      this.busy = false;
    }
  }
}

function explainCreate(err: unknown): string {
  const text = explain(err);
  if (/forbidden|403/i.test(text)) {
    // The contract gives one 403 for both and the server cannot say which, so
    // the message names both rather than guessing.
    return "That server would not accept this signup token. It may be wrong, or the server may not be handing out new workspaces.";
  }
  return text;
}

/**
 * A backend error, as a sentence.
 *
 * Go's errors are wrapped for logs -- "workbench: join workspace: unauthorized:
 * unknown or expired key" -- and the prefixes are the package's, not the
 * user's. They are stripped and the rest is punctuated, so the live region
 * reads as a sentence rather than as a stack of colons.
 */
function explain(err: unknown): string {
  const raw = (err instanceof Error ? err.message : String(err)).trim();
  if (raw === "") return "That did not work, and the backend did not say why.";
  const text = raw.replace(/^(workbench|services|sync|ops):\s*/i, "").trim();
  const capitalised = text[0].toUpperCase() + text.slice(1);
  return /[.!?]$/.test(capitalised) ? capitalised : `${capitalised}.`;
}
