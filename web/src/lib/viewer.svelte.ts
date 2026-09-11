/**
 * Reading a workspace, and nothing else.
 *
 * The viewer speaks the read half of the server's API: metadata, and the op
 * log from a sequence number. It never posts. The one place that would be
 * tempting -- folding a branch, which is a field on a node -- is deliberately
 * kept as local view state instead, so there is no code path in this
 * directory that can write to somebody's workspace.
 *
 * Live updates are polling, because the server has no websocket and no SSE.
 * See server/README.md.
 */
import { State, type Node, type Op, type TreeNode } from "@doc/ops";
import { outlineRows, planTasks, type Row } from "@doc/model";

/** What the viewer is doing, in the words the status line uses. */
export type Status = "starting" | "loading" | "live" | "offline" | "rejected" | "no-key";

/** How often to ask for more of the log once we have caught up. */
const POLL_MS = 5000;

const RETRY_MS = 3000;
const RETRY_CAP_MS = 60_000;

/** Pages read per pass, so a long history fills in visibly rather than at once. */
const PAGES_PER_PASS = 10;

export class Viewer {
  status = $state<Status>("starting");

  /** What the workspace is called, once the server has said. */
  name = $state("");

  /** The server's own words when something went wrong. */
  error = $state<string | null>(null);

  /** When the last successful read finished, for "updated 20 seconds ago". */
  updatedAt = $state<number | null>(null);

  /**
   * The document.
   *
   * `$state.raw` and replaced whole: the merge is not reactive, so the tree is
   * rebuilt after each batch of ops and swapped in. Making it deeply reactive
   * would turn reading a thousand-op history into thousands of signal writes
   * for one paint.
   */
  tree = $state.raw<TreeNode[]>([]);

  /**
   * Lines the tree cannot reach, because the line they were under was deleted
   * while somebody was still writing under it. Shown rather than dropped: a
   * viewer that hides them looks complete and is not.
   */
  detached = $state.raw<Node[]>([]);

  rows = $derived<Row[]>(outlineRows(this.tree));
  tasks = $derived<TreeNode[]>(planTasks(this.tree));

  #key: string | null = null;
  #state = new State();
  #seq = 0;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #backoff = RETRY_MS;
  #stopped = true;
  #busy = false;

  /** Points the viewer at a key, from nothing or from another one. */
  use(key: string | null): void {
    this.#key = key;
    this.#state = new State();
    this.#seq = 0;
    this.#backoff = RETRY_MS;
    this.error = null;
    this.name = "";
    this.updatedAt = null;
    this.tree = [];
    this.detached = [];
    this.status = key ? "loading" : "no-key";
    if (!this.#stopped) this.#schedule(0);
  }

  start(): () => void {
    this.#stopped = false;
    this.#schedule(0);

    // Two things are worth cutting a wait short for: the network coming back,
    // and the page being looked at again. A phone that was in a pocket for an
    // hour should show the current outline when it comes out of it, not the
    // one from an hour ago plus however long the backoff had grown to.
    const wake = () => {
      if (document.hidden) return;
      this.#backoff = RETRY_MS;
      this.#schedule(0);
    };
    window.addEventListener("online", wake);
    document.addEventListener("visibilitychange", wake);

    return () => {
      this.#stopped = true;
      window.removeEventListener("online", wake);
      document.removeEventListener("visibilitychange", wake);
      if (this.#timer !== null) clearTimeout(this.#timer);
      this.#timer = null;
    };
  }

  retry(): void {
    this.#backoff = RETRY_MS;
    this.error = null;
    if (this.status === "rejected") this.status = "loading";
    this.#schedule(0);
  }

  #schedule(delay: number): void {
    if (this.#stopped || !this.#key) return;
    if (this.#timer !== null) clearTimeout(this.#timer);
    this.#timer = setTimeout(() => {
      this.#timer = null;
      void this.#tick();
    }, delay);
  }

  async #tick(): Promise<void> {
    if (!this.#key || this.#busy || this.status === "rejected") return;
    // A hidden tab is a tab nobody is reading. Polling it is a request every
    // five seconds for a page that is not on screen, on somebody's data plan.
    if (document.hidden) {
      this.#schedule(POLL_MS);
      return;
    }

    this.#busy = true;
    try {
      if (this.name === "") {
        const info = await this.#get<{ workspace?: { name?: string } }>("/v1/workspace");
        this.name = info.workspace?.name ?? "Workspace";
      }

      let caughtUp = false;
      for (let page = 0; page < PAGES_PER_PASS; page++) {
        const body = await this.#get<{
          head?: number;
          ops?: { seq?: number; op?: Op }[];
          more?: boolean;
        }>(`/v1/ops?since=${this.#seq}`);

        const entries = body.ops ?? [];
        let applied = 0;
        for (const entry of entries) {
          if (!entry.op) continue;
          try {
            if (this.#state.apply(entry.op)) applied++;
          } catch {
            // An op this version cannot apply -- written by a newer release of
            // the app. Skipped, so the rest of the log still renders: a
            // viewer that refused the whole workspace over one unknown op
            // would go blank on the day the desktop grows a feature.
          }
          if (typeof entry.seq === "number") this.#seq = Math.max(this.#seq, entry.seq);
        }
        if (applied > 0) this.#refresh();

        if (!(body.more ?? entries.length > 0) || entries.length === 0) {
          caughtUp = true;
          break;
        }
      }

      this.status = "live";
      this.error = null;
      this.updatedAt = Date.now();
      this.#backoff = RETRY_MS;
      this.#schedule(caughtUp ? POLL_MS : 0);
    } catch (err) {
      this.#fail(err);
    } finally {
      this.#busy = false;
    }
  }

  #refresh(): void {
    this.tree = this.#state.tree();
    this.detached = this.#state.detached();
  }

  #fail(err: unknown): void {
    if (err instanceof ReadError && err.rejected) {
      this.status = "rejected";
      this.error = err.message;
      // Not rescheduled. A key the server will not accept is not going to
      // start working, and retrying it every few seconds forever is a request
      // loop nobody asked for.
      return;
    }
    this.status = "offline";
    this.error = err instanceof Error ? err.message : String(err);
    this.#schedule(this.#backoff);
    this.#backoff = Math.min(this.#backoff * 2, RETRY_CAP_MS);
  }

  /**
   * A GET against the server that served this page.
   *
   * Relative, because that is what it is: the viewer is a static file handed
   * out by the same Go binary that answers /v1. An absolute URL would need
   * CORS on a server that has no reason to allow it.
   */
  async #get<T>(path: string): Promise<T> {
    const abort = new AbortController();
    const timer = setTimeout(() => abort.abort(), 10_000);
    let response: Response;
    try {
      response = await fetch(path, {
        headers: { authorization: `Bearer ${this.#key}` },
        signal: abort.signal,
        cache: "no-store",
      });
    } catch {
      throw new ReadError("This page could not reach the server.", false);
    } finally {
      clearTimeout(timer);
    }

    if (response.status === 401 || response.status === 403) {
      throw new ReadError(
        "The server would not accept this link. It may have been rotated, or it may never have been valid.",
        true,
      );
    }
    if (!response.ok) {
      throw new ReadError(`The server answered ${response.status}.`, false);
    }
    try {
      return (await response.json()) as T;
    } catch {
      throw new ReadError("The server's answer was not readable.", false);
    }
  }
}

class ReadError extends Error {
  readonly rejected: boolean;

  constructor(message: string, rejected: boolean) {
    super(message);
    this.name = "ReadError";
    this.rejected = rejected;
  }
}
