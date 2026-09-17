/**
 * Reading a workspace, and nothing else.
 *
 * The viewer speaks two endpoints and both are GETs: `/v1/workspace` for the
 * name, and `/v1/document` for the document. It never posts. The one place
 * that would be tempting -- folding a branch, which is a field on a node -- is
 * deliberately kept as local view state instead, so there is no code path in
 * this directory that can write to somebody's workspace.
 *
 * It used to fetch `/v1/ops` and merge the log itself. It does not any more:
 * the merge is `internal/ops`, which is Go, and a second implementation of it
 * in TypeScript is two documents for one log. The server runs the one merge
 * and hands back the result.
 *
 * Live updates are polling, because the server has no websocket and no SSE.
 * Polling is cheap because the document carries a strong ETag: an unchanged
 * document answers 304 with no body, and nothing here re-renders.
 */
import {
  contentsOf,
  endsOf,
  isEdge,
  isRegion,
  outlineNodes,
  planTasks,
  readDocument,
  regionIdOf,
  sourceIdOf,
  tabsOf,
  textOf,
  type DocNode,
  type TabEntry,
} from "./doc";

/** What the viewer is doing, in the words the status line uses. */
export type Status = "starting" | "loading" | "live" | "offline" | "rejected" | "no-key";

/** How often to ask whether the document has moved. */
const POLL_MS = 5000;

const RETRY_MS = 3000;
const RETRY_CAP_MS = 60_000;

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
   * `$state.raw` and replaced whole: a document arrives as one JSON body and
   * is swapped in as one value. Making it deeply reactive would turn one
   * response into a signal write per node for one paint.
   */
  tree = $state.raw<DocNode[]>([]);

  /**
   * Lines the tree cannot reach, because the line they were under was deleted
   * while somebody was still writing under it -- or because the op that
   * creates their parent has not arrived yet, or because concurrent moves put
   * them in a cycle.
   *
   * Shown rather than dropped: a viewer that hides them looks complete and is
   * not, and cannot tell that it is not. server/README.md is explicit about
   * this being a state to render rather than an error.
   */
  detached = $state.raw<DocNode[]>([]);

  /** The sequence the document on screen was merged through. */
  head = $state(0);

  /** The workspace's tabs, in the order the document has them. */
  tabs = $derived<TabEntry[]>(tabsOf(this.tree));

  /**
   * Which tab is being read.
   *
   * The first one until somebody picks another, and the first one again if the
   * one they picked is retired while they are looking at it -- a tab that is
   * gone cannot be shown, and an empty page with a name at the top of it says
   * less than the tab next to it does. `contentsOf` does that correction, so
   * this is left as whatever was asked for.
   */
  tab = $state("");

  /**
   * What is on screen: one tab's document, which is what the desktop app has.
   *
   * The whole document is still fetched, still merged and still indexed -- the
   * links, the regions and the detached lines are the workspace's rather than
   * a tab's -- and this is the slice the two views draw.
   */
  contents = $derived<DocNode[]>(contentsOf(this.tree, this.tab));

  /** The outline: the tab without the tasks, which are the plan. */
  outline = $derived<DocNode[]>(outlineNodes(this.contents));

  /**
   * Links and regions, indexed once for the whole page.
   *
   * A mindmap is not a tree, and these are the parts of it that are not. The
   * viewer shows them because somebody sent this link to somebody else to read,
   * and a relationship nobody outside the desktop can see may as well not be in
   * the document.
   */
  graph = $derived.by(() => {
    const text = new Map<string, string>();
    const alive = new Set<string>();
    const regions = new Map<string, string>();
    const regionOf = new Map<string, string>();
    const links = new Map<string, { other: string; text: string; dangling: boolean }[]>();
    const edges: { from: string; to: string }[] = [];

    const walk = (nodes: readonly DocNode[]) => {
      for (const node of nodes) {
        alive.add(node.id);
        text.set(node.id, textOf(node));
        if (isEdge(node)) {
          edges.push(endsOf(node));
        } else if (isRegion(node)) {
          regions.set(node.id, textOf(node));
        } else {
          const region = regionIdOf(node);
          if (region) regionOf.set(node.id, region);
        }
        walk(node.children ?? []);
      }
    };
    walk(this.tree);

    // Both ends, because one relationship should not look like two different
    // things depending on which row is being read.
    const add = (from: string, to: string) => {
      if (!from || !to || from === to) return;
      const list = links.get(from) ?? [];
      list.push({ other: to, text: text.get(to) ?? "a line that is gone", dangling: !alive.has(to) });
      links.set(from, list);
    };
    for (const edge of edges) {
      add(edge.from, edge.to);
      add(edge.to, edge.from);
    }

    return { links, regions, regionOf };
  });

  /** The links touching a node. */
  linksOf(id: string): { other: string; text: string; dangling: boolean }[] {
    return this.graph.links.get(id) ?? [];
  }

  /** The region a node is in, or null. A deleted region is no region at all. */
  regionOf(id: string): string | null {
    const region = this.graph.regionOf.get(id);
    if (!region) return null;
    return this.graph.regions.get(region) ?? null;
  }

  /**
   * The same answer with the region's id on it, which the map needs.
   *
   * The map draws one box around every line of a region, so it has to know
   * which lines belong together -- and two regions can perfectly well share a
   * name. `regionOf` answers the question the outline asks, "what is this line
   * part of, in words", and that one cannot tell them apart.
   */
  regionEntryOf(id: string): { id: string; name: string } | null {
    const region = this.graph.regionOf.get(id);
    if (!region) return null;
    const name = this.graph.regions.get(region);
    if (name === undefined) return null;
    return { id: region, name };
  }

  /**
   * How many tasks were extracted from a line.
   *
   * Counted over the plan rather than read off the line: the link is written on
   * the task, and one line can be broken up more than once.
   */
  tasksOf(id: string): number {
    if (id === "") return 0;
    let found = 0;
    for (const task of this.tasks) {
      if (sourceIdOf(task) === id) found++;
    }
    return found;
  }

  /** The plan, already in position order -- the merge sorted it. */
  tasks = $derived<DocNode[]>(planTasks(this.contents));

  #key: string | null = null;
  #etag: string | null = null;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #backoff = RETRY_MS;
  #stopped = true;
  #busy = false;

  /** Points the viewer at a key, from nothing or from another one. */
  use(key: string | null): void {
    this.#key = key;
    this.#etag = null;
    this.#backoff = RETRY_MS;
    this.error = null;
    this.name = "";
    this.updatedAt = null;
    this.tree = [];
    this.detached = [];
    this.head = 0;
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
        const info = await this.#json<{ workspace?: { name?: string } }>(
          await this.#get("/v1/workspace"),
        );
        this.name = info.workspace?.name ?? "Workspace";
      }

      // The conditional request. The ETag is opaque -- echoed, never parsed,
      // and never derived from `head`.
      const headers: Record<string, string> = {};
      if (this.#etag !== null) headers["if-none-match"] = this.#etag;
      const response = await this.#get("/v1/document", headers);

      if (response.status !== 304) {
        const etag = response.headers.get("etag");
        const document = readDocument(await this.#json<unknown>(response));
        // Assigned after the body parsed, so a truncated response does not
        // leave an ETag behind that would suppress the retry of it.
        this.#etag = etag;
        this.tree = document.tree;
        this.detached = document.detached;
        this.head = document.head;
      }

      this.status = "live";
      this.error = null;
      this.updatedAt = Date.now();
      this.#backoff = RETRY_MS;
      this.#schedule(POLL_MS);
    } catch (err) {
      this.#fail(err);
    } finally {
      this.#busy = false;
    }
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
   *
   * `cache: "no-store"` is what makes the conditional request ours. Left to
   * its own devices the browser revalidates on its own schedule and turns the
   * 304 back into a 200 out of its cache before this code sees it -- so the
   * ETag would still save bandwidth and this page could never tell whether it
   * had.
   */
  async #get(path: string, headers: Record<string, string> = {}): Promise<Response> {
    const abort = new AbortController();
    const timer = setTimeout(() => abort.abort(), 10_000);
    let response: Response;
    try {
      response = await fetch(path, {
        headers: { ...headers, authorization: `Bearer ${this.#key}` },
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
    // 304 is the one answer on this server without a JSON body, and it is not
    // an error -- so it passes `ok`, which is false for it.
    if (!response.ok && response.status !== 304) {
      throw new ReadError(`The server answered ${response.status}.`, false);
    }
    return response;
  }

  async #json<T>(response: Response): Promise<T> {
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
