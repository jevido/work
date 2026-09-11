/**
 * This machine, as a writer of ops.
 *
 * Every op carries the replica that wrote it and a Lamport clock, and those
 * two decide who wins a contested field. Getting either wrong is not a crash;
 * it is a workspace that silently keeps the wrong version of a line, which is
 * why minting an op happens in one place with one rule and nowhere else.
 */
import { newId } from "./model";
import type { Op } from "./ops";

const ACTOR_KEY = "work.actor.v1";

/**
 * The id this installation writes ops under.
 *
 * Stable across restarts, and it has to be: the actor is the tiebreak when two
 * replicas pick the same clock, so an id that changed every launch would make
 * the outcome of a concurrent edit depend on when the app was last opened.
 * It is also what tells our own ops apart from somebody else's when the log
 * comes back.
 */
export function actorId(): string {
  try {
    const saved = localStorage.getItem(ACTOR_KEY);
    if (saved) return saved;
    const fresh = `d-${newId().slice(0, 16)}`;
    localStorage.setItem(ACTOR_KEY, fresh);
    return fresh;
  } catch {
    // Storage refused. A per-session actor still converges; it just looks like
    // a new replica each launch, which is true enough of a session that cannot
    // remember anything.
    return `d-${newId().slice(0, 16)}`;
  }
}

export class Replica {
  readonly actor: string;

  /** The highest clock this replica has issued. */
  #issued = 0;

  /** What the merge has seen, which is at least as high as what we issued. */
  #observed: () => number;

  /**
   * @param observed The merged state's clock. Read at every mint rather than
   *   copied, because ops arrive from other replicas between our own edits and
   *   an op issued at a clock below one we have already merged would lose to
   *   an edit it plainly came after.
   */
  constructor(actor: string, observed: () => number) {
    this.actor = actor;
    this.#observed = observed;
  }

  /**
   * The next clock.
   *
   * Strictly greater than everything seen and everything issued -- including
   * ops issued moments ago in the same batch, which is what #issued is for:
   * the merged state has not caught up with them yet, so reading its clock
   * alone would hand out the same number twice and make two of our own edits
   * concurrent with each other.
   */
  #tick(): number {
    this.#issued = Math.max(this.#issued, this.#observed()) + 1;
    return this.#issued;
  }

  #base(node: string): Pick<Op, "id" | "actor" | "clock" | "node"> {
    return { id: newId(), actor: this.actor, clock: this.#tick(), node };
  }

  create(node: string, parent: string, position: string, fields: Record<string, unknown>): Op {
    return { ...this.#base(node), kind: "create-node", parent, position, fields };
  }

  setFields(node: string, fields: Record<string, unknown>): Op {
    return { ...this.#base(node), kind: "set-fields", fields };
  }

  /** Tombstones a node. Permanent: nothing in the protocol undeletes one. */
  remove(node: string): Op {
    return { ...this.#base(node), kind: "delete-node" };
  }

  move(node: string, parent: string, position: string): Op {
    return { ...this.#base(node), kind: "move-node", parent, position };
  }

  extract(
    node: string,
    task: string,
    parent: string,
    position: string,
    fields: Record<string, unknown>,
  ): Op {
    return { ...this.#base(node), kind: "extract-to-task", task, parent, position, fields };
  }
}
