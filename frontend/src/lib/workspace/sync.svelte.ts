/**
 * The connection between a workspace and the server that holds it, and the
 * states a person needs told apart.
 *
 * Every edit lands in the merged state first and in this outbox second. That
 * ordering is the whole design: an outline that waited for a server before
 * showing the line you just typed would be unusable on a train, and a
 * workspace that has never been shared has no server to wait for.
 *
 * Live updates are polling -- server/README.md is explicit that there is no
 * websocket and no SSE -- so this is a loop, and the interval is the honest
 * cost of that.
 */
import type { Op } from "./ops";
import {
  MAX_OPS_PER_PUSH,
  SyncError,
  type Accepted,
  type Transport,
} from "./transport";

/**
 * What the badge says.
 *
 * `local` and `synced` are both "nothing to worry about" and are still kept
 * apart, because they answer different questions: one workspace is not shared
 * with anybody, the other is shared and up to date. A badge that showed the
 * same for both would tell you your work was safe somewhere else when it was
 * only on this machine.
 *
 * `rejected` is the one that is not a phase. Everything else resolves by
 * waiting; that one waits forever unless somebody supplies a key that works.
 */
export type SyncState = "local" | "synced" | "syncing" | "offline" | "rejected";

/** An edit made here that the server has not taken yet. */
export interface Outgoing {
  op: Op;
  /** Only ever shown to a person: "waiting 4 minutes" is different from "waiting". */
  at: number;
}

const RETRY_MS = 2000;
const RETRY_CAP_MS = 60_000;

/** How often to ask for other people's edits when everything is fine. */
const POLL_MS = 4000;

/**
 * How many pages of history to take in one go on first connect.
 *
 * A workspace joined for the first time reads its whole log, which is a lot of
 * requests for a big one. Bounded per tick so a long catch-up does not hold
 * the loop -- and so the outline fills in visibly rather than after a stall.
 */
const PAGES_PER_TICK = 8;

export class Sync {
  /**
   * Edits waiting to be sent, oldest first.
   *
   * A flush sends the front of this queue and drops exactly what came back
   * accounted for, so an edit made while a flush is in flight is neither lost
   * nor sent twice.
   */
  outbox = $state<Outgoing[]>([]);

  state = $state<SyncState>("local");

  /** The server's or the network's own words, for the badge's detail. */
  error = $state<string | null>(null);

  /** How far through the server's log we have read. */
  seq = $state(0);

  /** The server's own head, so "3 behind" is a number and not a feeling. */
  head = $state(0);

  /** What this key may do, once the server has said. Null until it has. */
  access = $state<"read" | "write" | null>(null);

  #transport: Transport | null = null;
  #apply: (ops: readonly Op[]) => void;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #backoff = RETRY_MS;
  #busy = false;
  #stopped = true;

  /**
   * @param apply How to merge the server's ops. Passed in because the merged
   *   state belongs to the workspace: this class knows when edits arrive and
   *   nothing about what they mean.
   */
  constructor(apply: (ops: readonly Op[]) => void) {
    this.#apply = apply;
  }

  get pending(): number {
    return this.outbox.length;
  }

  /** When the oldest unsent edit was made, or null when there are none. */
  get oldestAt(): number | null {
    return this.outbox[0]?.at ?? null;
  }

  /** How far behind the server we are. Zero when caught up. */
  get behind(): number {
    return Math.max(0, this.head - this.seq);
  }

  get shared(): boolean {
    return this.#transport !== null;
  }

  get base(): string | null {
    return this.#transport?.base ?? null;
  }

  /**
   * Points this at a server, or at none.
   *
   * Also the only way out of a refused key: supplying a working one is the one
   * thing that can clear a rejection. The outbox deliberately survives -- edits
   * made while a key was being refused are still edits, and dropping them to
   * tidy up a state machine is losing work to simplify a badge.
   */
  configure(transport: Transport | null, seq = 0): void {
    // Cut loose from a server: the queue goes with it. Those ops were
    // addressed to that workspace, and holding them for whichever server
    // comes next would push one workspace's edits into another's log.
    if (!transport) this.outbox = [];
    this.#transport = transport;
    this.seq = seq;
    this.head = Math.max(this.head, seq);
    this.access = null;
    this.error = null;
    this.#backoff = RETRY_MS;
    this.state = transport ? (this.outbox.length > 0 ? "syncing" : "synced") : "local";
    if (!this.#stopped) this.#schedule(0);
  }

  /**
   * Queues an edit. The caller has already merged it locally.
   *
   * Not coalesced here even though two `set-fields` on the same node plainly
   * could be. The caller does that, where it knows a keystroke from a paste;
   * doing it here would mean this file guessing which of two writes was a
   * correction and which was a second thought -- and each op is idempotent by
   * id, so dropping one is dropping an edit, not a duplicate.
   */
  record(op: Op): void {
    // A workspace with no server does not queue. There is nothing for the ops
    // to be waiting for, and a count of them would be a tab reading "8 unsaved
    // changes" next to a badge reading "on this machine" -- one of which has
    // to be wrong, and it is the count. Sharing later sends the document
    // rather than the history; see Workspace.seedOps.
    if (!this.#transport) return;

    this.outbox.push({ op, at: Date.now() });
    if (this.state === "rejected") return;
    if (this.state === "synced") this.state = "syncing";
    this.#schedule(0);
  }

  /** Starts the loop. Returns the stop. */
  start(): () => void {
    this.#stopped = false;
    this.#schedule(0);

    // The browser saying the network is back beats the next timer: a laptop
    // just opened should not sit out a 60-second backoff it earned while shut.
    const wake = () => {
      if (this.state !== "offline") return;
      this.#backoff = RETRY_MS;
      this.#schedule(0);
    };
    window.addEventListener("online", wake);

    return () => {
      this.#stopped = true;
      window.removeEventListener("online", wake);
      if (this.#timer !== null) clearTimeout(this.#timer);
      this.#timer = null;
    };
  }

  /** Tries now, whatever the backoff said. What the badge's retry does. */
  retry(): void {
    this.#backoff = RETRY_MS;
    this.error = null;
    if (this.state === "rejected") return;
    this.#schedule(0);
  }

  #schedule(delay: number): void {
    if (this.#stopped || !this.#transport) return;
    if (this.#timer !== null) clearTimeout(this.#timer);
    this.#timer = setTimeout(() => {
      this.#timer = null;
      void this.#tick();
    }, delay);
  }

  async #tick(): Promise<void> {
    const transport = this.#transport;
    if (!transport || this.#busy || this.state === "rejected") return;
    this.#busy = true;
    try {
      // What this key may do, asked once per connection. The desktop needs it
      // so that pasting a read key into "join" says so, rather than looking
      // like a workspace where every edit silently fails to leave.
      if (this.access === null) {
        const info = await transport.info();
        this.access = info.access;
        this.head = Math.max(this.head, info.head);
      }

      // A read key in a desktop tab. The server would say so on the first
      // push, as a 403 that reads as "your key was refused" -- true, and
      // needlessly cryptic when the workspace has already told us which kind
      // of key this is. Said before anything is attempted, and said as the
      // thing it is.
      if (this.access === "read") {
        this.state = "rejected";
        this.error =
          "This is a read key. It can open the workspace and cannot change it — a write key starts with wk_.";
        return;
      }

      // Push before pull. Our own edits are the ones somebody is watching for,
      // and pulling first makes the round trip that shows them one poll longer
      // than it needs to be.
      if (this.outbox.length > 0) await this.#push(transport);
      const caughtUp = await this.#pull(transport);

      this.error = null;
      this.#backoff = RETRY_MS;
      this.state = this.outbox.length > 0 ? "syncing" : "synced";
      // Still paging through history: come straight back rather than waiting
      // out a poll interval per page.
      this.#schedule(caughtUp ? POLL_MS : 0);
    } catch (err) {
      this.#fail(err);
    } finally {
      this.#busy = false;
    }
  }

  async #push(transport: Transport): Promise<void> {
    // The contract caps a request at 500 ops and a body at 1 MiB. The count is
    // knowable here; the size is not, really, so `too_large` is handled below
    // rather than predicted. Whatever is left over goes next tick, which is
    // immediately: a queue over the cap means we are behind, not idle.
    let size = Math.min(this.outbox.length, MAX_OPS_PER_PUSH);

    for (;;) {
      const batch = this.outbox.slice(0, size);
      let result: Accepted;
      try {
        result = await transport.push(batch.map((o) => o.op));
      } catch (err) {
        if (!(err instanceof SyncError) || err.failure !== "refused") throw err;

        // Too big is not too broken. Half of it will fit, and the half after
        // that goes on the next pass -- dropping a batch because it was long
        // would lose edits over a size limit.
        if (err.code === "too_large" && size > 1) {
          size = Math.ceil(size / 2);
          continue;
        }

        // A batch the server will never take -- a malformed op, or one whose
        // id collides with different content -- would otherwise block every
        // edit behind it forever. It is dropped, and it is the only case in
        // here that loses an edit, so it says so rather than failing quietly.
        const ids = new Set(batch.map((o) => o.op.id));
        this.outbox = this.outbox.filter((o) => !ids.has(o.op.id));
        throw new SyncError(
          "refused",
          `${err.message} ${batch.length} ${batch.length === 1 ? "change was" : "changes were"} dropped.`,
          err.code,
        );
      }

      // Duplicates are not an error: the contract says a retry of a request
      // whose response was lost reports every op as one, and a queue that
      // resends after a lost response is exactly what this is.
      const sent = new Set(batch.map((o) => o.op.id));
      this.outbox = this.outbox.filter((o) => !sent.has(o.op.id));
      this.head = Math.max(this.head, result.head);
      return;
    }
  }

  /** Reads forward from `seq`. Returns true once there is nothing more waiting. */
  async #pull(transport: Transport): Promise<boolean> {
    for (let page = 0; page < PAGES_PER_TICK; page++) {
      const { head, ops, lastSeq, more } = await transport.since(this.seq);
      this.head = Math.max(this.head, head);
      if (ops.length > 0) {
        // Ops we sent ourselves come back in here too, and applying one again
        // is a no-op -- that is rule three, and every path through State.apply
        // is written to keep it.
        this.#apply(ops);
      }
      // Sequence numbers are gapless, so the last seq on the page is exactly
      // how far we have read. The cursor only moves once the ops it covers
      // have been merged, so a failure mid-catch-up resumes rather than skips.
      this.seq = Math.max(this.seq, lastSeq);
      if (!more || ops.length === 0) return true;
    }
    return false;
  }

  #fail(err: unknown): void {
    if (err instanceof SyncError && err.failure === "rejected") {
      this.state = "rejected";
      this.error = err.message;
      // Nothing is scheduled. A refused key is not worth asking about again:
      // the retries cost a request every few seconds forever, and there is no
      // amount of waiting that turns a wrong key into a right one.
      return;
    }
    this.state = "offline";
    this.error = err instanceof Error ? err.message : String(err);

    if (err instanceof SyncError && err.retryAfter !== null) {
      // The server said how long. Honour it rather than our own backoff.
      this.#schedule(err.retryAfter * 1000);
      return;
    }
    this.#schedule(this.#backoff);
    this.#backoff = Math.min(this.#backoff * 2, RETRY_CAP_MS);
  }
}
