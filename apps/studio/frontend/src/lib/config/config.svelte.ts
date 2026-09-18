import * as Service from "./service";

/**
 * Where the app stands on knowing its config folder.
 *
 * `probing` is the state the window opens in, and nothing is drawn during it:
 * an app that already has a folder must not flash a setup screen on its way to
 * the office, and one that does not must not flash the office on its way to
 * the setup screen. Both are a half-second of lying about how this is set up.
 */
export type ConfigStatus = "probing" | "missing" | "ready";

/**
 * The config folder, as the app currently understands it.
 *
 * Agents are folders on disk, edited in an editor rather than in here, so the
 * two things this holds are a path and a way to re-read it. Every call it makes
 * goes through ./service.
 */
export class Config {
  /** The folder, or null if none is saved. */
  path = $state<string | null>(null);

  status = $state<ConfigStatus>("probing");

  /** True while a picker is open or a reload is in flight. */
  busy = $state(false);

  /** Why the last thing tried did not work. */
  error = $state<string | null>(null);

  /**
   * What the last reload found, for the menu to show.
   *
   * Reloading an unchanged folder changes nothing on screen, so without this
   * the menu item reads as broken. Cleared when the menu is next opened.
   */
  note = $state<string | null>(null);

  /**
   * Re-reads whatever the config folder feeds, and describes what it found.
   *
   * The roster is the caller's, not this class's: config knows about a path,
   * and the office knows about agents. The description comes back so the menu
   * can say "3 agents" without this file knowing what an agent is.
   */
  #refresh: () => Promise<string>;

  constructor(refresh: () => Promise<string>) {
    this.#refresh = refresh;
  }

  /** True once the rest of the app is allowed to render. */
  get open(): boolean {
    return this.status === "ready";
  }

  /** Reads the saved folder. Called once, before anything is drawn. */
  async probe(): Promise<void> {
    try {
      this.path = await Service.getConfigPath();
    } catch (err) {
      this.error = messageOf(err);
    }
    this.status = this.path ? "ready" : "missing";
  }

  /**
   * Asks for a folder. Returns true if one was chosen.
   *
   * Cancelling leaves everything alone, including a folder already in use --
   * "Change folder location" that loses the current one when you back out of
   * the picker is the worst thing this menu could do.
   */
  async choose(): Promise<boolean> {
    if (this.busy) return false;
    this.busy = true;
    this.error = null;
    try {
      const picked = await Service.selectConfigFolder();
      if (!picked) return false;
      this.path = picked;
      return true;
    } catch (err) {
      this.error = messageOf(err);
      return false;
    } finally {
      this.busy = false;
    }
  }

  /** Lets the app through, once a folder has been chosen and looked at. */
  confirm(): void {
    if (this.path) this.status = "ready";
  }

  /** Re-reads the folder and refreshes what it feeds. */
  async reload(): Promise<void> {
    await this.#run(async () => {
      await Service.reloadAgents();
      return `Reloaded — ${await this.#refresh()}`;
    });
  }

  /**
   * Reads the team from a folder that was just chosen.
   *
   * Picking a folder loads it on the backend already, so re-reading it here
   * would be a second scan of the same folder for nothing. What is left is
   * showing what it found.
   */
  async adopt(): Promise<void> {
    await this.#run(async () => `Loaded — ${await this.#refresh()}`);
  }

  async #run(work: () => Promise<string>): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    this.error = null;
    this.note = null;
    try {
      this.note = await work();
    } catch (err) {
      this.error = messageOf(err);
    } finally {
      this.busy = false;
    }
  }
}

function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
