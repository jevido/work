/**
 * The open workspaces, and which one is in front.
 *
 * A tab bar's whole job is that the thing behind each tab keeps running while
 * you are not looking at it, so every workspace here is live: its sync loop
 * is going, its outbox is draining, and switching tabs shows you a workspace
 * that has been keeping up rather than one that starts catching up when you
 * arrive.
 */
import * as Storage from "./storage";
import { Workspace } from "./workspace.svelte";
import {
  DEFAULT_SERVER,
  createWorkspace,
  httpTransport,
  parseInvite,
  SyncError,
} from "./transport";

/** How long after the last change to write everything down. */
const SAVE_AFTER_MS = 800;

export class Workspaces {
  list = $state<Workspace[]>([]);

  activeId = $state<string | null>(null);

  /** What the tabs and everything under them are showing. */
  active = $derived<Workspace | null>(
    this.list.find((w) => w.id === this.activeId) ?? this.list[0] ?? null,
  );

  /** Why the last create or join did not work, for the dialog to show. */
  error = $state<string | null>(null);

  /** True while a create or join is talking to a server. */
  busy = $state(false);

  /** The server to prefill. The last one that worked, so the second tab is a paste. */
  lastServer = $state(DEFAULT_SERVER);

  /** The sync loops, by workspace id, so closing a tab can stop its own. */
  #loops = new Map<string, () => void>();

  /**
   * A cheap summary of everything worth writing down.
   *
   * Revisions and counts rather than documents: the save is debounced on this
   * changing, and reading the outlines to notice that one of them changed
   * would walk every tree on every keystroke.
   */
  stamp = $derived(
    [
      this.activeId,
      ...this.list.map(
        (w) =>
          `${w.id}:${w.name}:${w.mode}:${w.revision}:${w.sync.seq}:${w.sync.outbox.length}:${w.base ?? ""}`,
      ),
    ].join("|"),
  );

  /** Reads whatever was open last time and starts all of it. */
  restore(): void {
    const stored = Storage.load();
    this.list = stored.workspaces.map((snapshot) => new Workspace(snapshot));
    this.activeId = stored.activeId ?? this.list[0]?.id ?? null;
    for (const workspace of this.list) this.#run(workspace);
    const shared = this.list.find((w) => w.base);
    if (shared?.base) this.lastServer = shared.base;
  }

  select(id: string): void {
    if (this.list.some((w) => w.id === id)) this.activeId = id;
  }

  /**
   * Opens a workspace that is only on this machine.
   *
   * Not every outline is worth a server. Making one local is instant, needs no
   * network and no signup token, and `share` turns it into a shared one later
   * without losing what is in it.
   *
   * It opens in Idea, because that is where a workspace somebody just decided
   * to make actually starts.
   */
  createLocal(name: string): Workspace {
    const workspace = Workspace.create(name.trim() || "Untitled");
    this.#add(workspace);
    return workspace;
  }

  /**
   * The one workspace a first run gets, so the app does not open on an empty
   * strip with a New button.
   *
   * It opens in Work, and the difference from createLocal is the whole point
   * of having a second method. Nobody asked for this workspace -- it exists
   * because Work used to be one screen and now has three, and the person
   * launching it after an update did not come here to write an outline. They
   * came to the app they already had, which is the office. Idea and planning
   * are there when they are wanted.
   */
  bootstrap(): Workspace {
    const workspace = Workspace.create("Workspace");
    workspace.mode = "work";
    this.#add(workspace);
    return workspace;
  }

  /**
   * Asks a server for a new workspace and opens it.
   *
   * Creation is gated by a signup token the server is configured with -- see
   * server/README.md -- so a server that is not handing out workspaces answers
   * 403, and that reads as "this server is not making workspaces" rather than
   * as a bad token, because from here they are the same 403 and only one of
   * them is something the person did.
   */
  async createShared(
    base: string,
    signupToken: string,
    name: string,
  ): Promise<{ workspace: Workspace; readKey: string } | null> {
    return this.#attempt(async () => {
      const created = await createWorkspace(base, signupToken, name.trim() || "Untitled");
      const workspace = Workspace.join(created.info.name, base, created.writeKey);
      this.#add(workspace);
      this.lastServer = base;
      return { workspace, readKey: created.readKey };
    }, explainCreate);
  }

  /**
   * Joins an existing workspace by key.
   *
   * The key is checked before a tab appears. A tab that opened empty and then
   * turned into an error badge is a workspace you have to close again, and it
   * looks exactly like a workspace that is simply still loading -- which is a
   * state this app has for real, so the two must not be confused.
   */
  async join(input: string, fallbackBase: string): Promise<Workspace | null> {
    const invite = parseInvite(input);
    if (!invite) {
      this.error = "That does not look like a key or a share link.";
      return null;
    }
    const base = invite.base ?? fallbackBase;
    if (!base) {
      this.error = "Which server is that key for?";
      return null;
    }

    return this.#attempt(async () => {
      const info = await httpTransport(base, invite.key).info();
      requireWrite(info.access);

      const existing = this.list.find((w) => w.base === base && w.key === invite.key);
      if (existing) {
        // Already open. Bringing it forward is what somebody pasting the same
        // link twice meant, and a second tab onto one workspace is two of
        // everything with no way to tell them apart.
        this.activeId = existing.id;
        return existing;
      }

      const workspace = Workspace.join(info.name || "Shared workspace", base, invite.key);
      this.#add(workspace);
      this.lastServer = base;
      return workspace;
    });
  }

  /**
   * Puts a local workspace onto a server.
   *
   * A new workspace is created there and this one's content is sent to it, so
   * nothing that was typed before sharing is lost. Returns the read key, which
   * is the thing worth handing to somebody -- it is the only moment the server
   * will ever show it.
   */
  async share(
    workspace: Workspace,
    base: string,
    signupToken: string,
  ): Promise<string | null> {
    return this.#attempt(async () => {
      const created = await createWorkspace(base, signupToken, workspace.name);
      workspace.share(base, created.writeKey);
      this.lastServer = base;
      this.save();
      return created.readKey;
    }, explainCreate);
  }

  /** Points an already-open workspace at a different key. The way out of a rejection. */
  async rekey(workspace: Workspace, input: string, fallbackBase: string): Promise<boolean> {
    const invite = parseInvite(input);
    if (!invite) {
      this.error = "That does not look like a key or a share link.";
      return false;
    }
    const base = invite.base ?? fallbackBase;
    const done = await this.#attempt(async () => {
      requireWrite((await httpTransport(base, invite.key).info()).access);
      workspace.connect(base, invite.key);
      this.lastServer = base;
      return true;
    });
    return done === true;
  }

  rename(id: string, name: string): void {
    const workspace = this.list.find((w) => w.id === id);
    if (workspace) workspace.name = name;
  }

  /**
   * Closes a tab.
   *
   * Local to this machine: it stops the loop and forgets the workspace here,
   * and does not delete anything on the server -- there is no endpoint for
   * that and no reason for a tab bar to be where you would look for one. A
   * workspace with unsent edits is the caller's to warn about; see
   * `unsentIn`.
   */
  close(id: string): void {
    const workspace = this.list.find((w) => w.id === id);
    if (!workspace) return;
    // Flush anything mid-typing into the outbox first, so what is written down
    // includes the sentence somebody was in the middle of.
    workspace.dispose();
    this.#loops.get(id)?.();
    this.#loops.delete(id);

    const at = this.list.findIndex((w) => w.id === id);
    this.list = this.list.filter((w) => w.id !== id);
    if (this.activeId === id) {
      // The neighbour, not the first tab: closing the fourth of five should
      // land on one of the two you were between, the way every other tab bar
      // behaves.
      this.activeId = (this.list[at] ?? this.list[at - 1] ?? null)?.id ?? null;
    }
    this.save();
  }

  /** Unsent edits in a workspace, for a close that would strand them. */
  unsentIn(id: string): number {
    const workspace = this.list.find((w) => w.id === id);
    if (!workspace || !workspace.sync.shared) return 0;
    return workspace.sync.pending;
  }

  /** Writes everything down now. */
  save(): void {
    Storage.save({
      workspaces: this.list.map((w) => w.snapshot()),
      activeId: this.active?.id ?? null,
    });
  }

  /**
   * Arms the debounced save.
   *
   * Called from an effect that reads `stamp`, so a change cancels the pending
   * timer and sets a new one -- and a burst of typing writes once at the end
   * of it rather than once per character. Returns the cancel, which is the
   * effect's cleanup.
   */
  scheduleSave(): () => void {
    const timer = setTimeout(() => this.save(), SAVE_AFTER_MS);
    return () => clearTimeout(timer);
  }

  /** Stops every loop. For the app going away. */
  dispose(): void {
    for (const workspace of this.list) workspace.dispose();
    for (const stop of this.#loops.values()) stop();
    this.#loops.clear();
  }

  #add(workspace: Workspace): void {
    this.list = [...this.list, workspace];
    this.activeId = workspace.id;
    this.#run(workspace);
    this.save();
  }

  #run(workspace: Workspace): void {
    this.#loops.get(workspace.id)?.();
    this.#loops.set(workspace.id, workspace.sync.start());
  }

  /**
   * Runs something that talks to a server, with the busy flag and one place
   * that turns a failure into a sentence.
   *
   * @param describe How to word a failure. Creating a workspace and joining
   *   one both fail with the same 403, and the two mean entirely different
   *   things -- one is the signup token, the other is the workspace key -- so
   *   telling somebody to check their key when they mistyped a signup token
   *   sends them to look at the wrong field.
   */
  async #attempt<T>(
    work: () => Promise<T>,
    describe: (err: unknown) => string = explain,
  ): Promise<T | null> {
    if (this.busy) return null;
    this.busy = true;
    this.error = null;
    try {
      return await work();
    } catch (err) {
      this.error = describe(err);
      return null;
    } finally {
      this.busy = false;
    }
  }
}

/**
 * Refuses a read key where a write key belongs.
 *
 * Checked before a tab appears rather than left to the first push, which is
 * where the server would catch it. A tab that opens, loads somebody's outline
 * and then silently refuses every edit is the worst version of this: it looks
 * exactly like a workspace that is working, right up until the changes you
 * made turn out never to have left. The read-only viewer in web/ is what a
 * read key is for.
 */
function requireWrite(access: "read" | "write"): void {
  if (access === "write") return;
  throw new Error(
    "That is a read key. It can open the workspace and not change it — open it in the viewer, or ask for a write key.",
  );
}

/**
 * A failure, as something a person can do something about.
 *
 * The server's own message is used where there is one, because it knows more
 * than this does -- but the three cases below are ones where what the server
 * says ("forbidden") is true and useless, and what the person needs to know
 * is which of their two inputs was wrong.
 */
function explainCreate(err: unknown): string {
  if (err instanceof SyncError && err.failure === "rejected") {
    // The contract gives one 403 for both, and the server cannot tell us
    // which -- so the message names both rather than guessing.
    return "That server would not accept this signup token. It may be wrong, or the server may not be handing out new workspaces.";
  }
  return explain(err);
}

function explain(err: unknown): string {
  if (err instanceof SyncError) {
    switch (err.failure) {
      case "rejected":
        return "That key was refused. Check it is the whole key, and that it has not been rotated.";
      case "rate-limited":
        return "That server is asking us to slow down. Try again in a moment.";
      case "unreachable":
        return "That server could not be reached.";
      default:
        return err.message;
    }
  }
  return err instanceof Error ? err.message : String(err);
}
