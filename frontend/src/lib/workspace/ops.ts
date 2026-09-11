/**
 * The merge, in TypeScript. A port of dev.jevido/work/internal/ops.
 *
 * That Go package is the normative one -- the server runs it, the desktop's Go
 * side runs it, and server/README.md documents it as the shared
 * implementation. This is a second implementation of the same three rules, and
 * a second implementation is a liability, so it is worth being explicit about
 * why there is one.
 *
 * The read-only viewer in web/ is a page in a browser. It is handed a log of
 * ops over HTTP and has to turn that into an outline, and there is no Go on
 * the other end of that -- no Wails runtime, no bindings, nothing to call.
 * Something has to replay the log in the browser. Given that, the desktop app
 * uses the same one rather than a third: two implementations that disagree
 * would show two different documents for the same log, and the one thing worse
 * than porting this is porting it twice.
 *
 * The rules, from ops.go, in the order they matter:
 *
 *  1. Fields are last-write-wins, one field at a time, decided by Stamp.
 *  2. Delete beats a concurrent edit, in either order, whatever the clocks say.
 *  3. Ops are idempotent by id.
 *
 * Anything here that looks arbitrary is load-bearing and is explained in
 * ops.go or state.go. Changes go there first.
 */

export type Kind = "create-node" | "set-fields" | "delete-node" | "move-node" | "extract-to-task";

const KINDS: readonly Kind[] = [
  "create-node",
  "set-fields",
  "delete-node",
  "move-node",
  "extract-to-task",
];

/**
 * Fields that extract-to-task writes. Ordinary last-write-wins fields with a
 * conventional name -- see ops.go, which says the same.
 */
export const FIELD_TASK_ID = "taskId";
export const FIELD_EXTRACTED_FROM = "extractedFrom";

/** The same limits ops.go enforces, so a replica refuses what it could not send. */
export const MAX_ID_LEN = 128;
export const MAX_POSITION_LEN = 256;
export const MAX_FIELD_NAME_LEN = 128;
export const MAX_FIELDS = 256;

/** One edit, self-contained enough to merge with edits it never saw. */
export interface Op {
  id: string;
  kind: Kind;
  actor: string;
  clock: number;
  node: string;
  fields?: Record<string, unknown>;
  parent?: string;
  position?: string;
  task?: string;
}

/** Which of two writes to the same slot wins. */
export interface Stamp {
  clock: number;
  actor: string;
}

const ZERO_STAMP: Stamp = { clock: 0, actor: "" };

/** -1, 0 or +1. Clock first, then actor, exactly as ops.go compares them. */
export function compareStamps(a: Stamp, b: Stamp): number {
  if (a.clock !== b.clock) return a.clock < b.clock ? -1 : 1;
  if (a.actor === b.actor) return 0;
  return a.actor < b.actor ? -1 : 1;
}

/** A stamp does not win against itself, so re-applying an op is never a write. */
function after(a: Stamp, b: Stamp): boolean {
  return compareStamps(a, b) > 0;
}

export class OpError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "OpError";
  }
}

/**
 * Checks an op is one this can apply.
 *
 * Every op is checked, including ones read back from the server's log: the log
 * outlives any version of this code, so an op written by a newer release can
 * arrive here. Refusing it is right; half-applying it is not.
 */
export function validate(op: Op): void {
  checkId("id", op.id);
  if (!KINDS.includes(op.kind)) throw new OpError(`op ${op.id}: unknown kind ${op.kind}`);
  checkId("actor", op.actor);
  // A zero clock ties with an untouched slot and makes the winner depend on
  // the actor alone, so it is not a valid clock.
  if (!Number.isSafeInteger(op.clock) || op.clock <= 0) {
    throw new OpError(`op ${op.id}: clock is not a positive integer`);
  }
  checkId("node", op.node);
  if ((op.position?.length ?? 0) > MAX_POSITION_LEN) {
    throw new OpError(`op ${op.id}: position is over the ${MAX_POSITION_LEN} limit`);
  }

  const names = op.fields ? Object.keys(op.fields) : [];
  if (names.length > MAX_FIELDS) {
    throw new OpError(`op ${op.id}: ${names.length} fields, over the ${MAX_FIELDS} limit`);
  }
  for (const name of names) {
    if (name === "") throw new OpError(`op ${op.id}: field name is empty`);
    if (name.length > MAX_FIELD_NAME_LEN) {
      throw new OpError(`op ${op.id}: field name is over the ${MAX_FIELD_NAME_LEN} limit`);
    }
  }

  switch (op.kind) {
    case "set-fields":
      // Nothing to set is a client bug that would take a sequence number and
      // do nothing with it.
      if (names.length === 0) throw new OpError(`op ${op.id}: set-fields with no fields`);
      break;
    case "create-node":
    case "move-node":
      if (op.parent === op.node) throw new OpError(`op ${op.id}: node is its own parent`);
      break;
    case "extract-to-task":
      checkId("task", op.task ?? "");
      if (op.task === op.node) throw new OpError(`op ${op.id}: node is extracted from itself`);
      if (op.parent === op.task) throw new OpError(`op ${op.id}: task is its own parent`);
      break;
  }

  if ((op.parent?.length ?? 0) > MAX_ID_LEN) {
    throw new OpError(`op ${op.id}: parent is over the ${MAX_ID_LEN} limit`);
  }
}

function checkId(what: string, id: string): void {
  if (typeof id !== "string" || id === "") throw new OpError(`${what} is empty`);
  if (id.length > MAX_ID_LEN) throw new OpError(`${what} is over the ${MAX_ID_LEN} limit`);
}

/* -------------------------------------------------------------------------- */

/** One node as it stands after a merge. A snapshot: changing it changes nothing. */
export interface Node {
  id: string;
  /** Empty for a root. */
  parent: string;
  position: string;
  fields: Record<string, unknown>;
  /** A tombstone keeps its fields, so a viewer can say what was deleted. */
  deleted: boolean;
}

export interface TreeNode extends Node {
  children: TreeNode[];
}

interface Slot {
  value: unknown;
  stamp: Stamp;
}

/**
 * Where a node sits.
 *
 * Parent and position share one stamp rather than having one each, because
 * moving a node is a single act -- with separate stamps two concurrent moves
 * could merge into one op's parent and the other's position, putting the node
 * somewhere nobody moved it to. state.go says the same, at more length.
 */
interface Placement {
  parent: string;
  position: string;
  stamp: Stamp;
}

interface Entry {
  at: Placement;
  fields: Map<string, Slot>;
  deleted: boolean;
}

/** State as it goes to and comes back from local storage. */
export interface StateJSON {
  clock: number;
  seen: string[];
  nodes: Record<
    string,
    {
      parent: string;
      position: string;
      stamp: Stamp;
      deleted: boolean;
      fields: Record<string, Slot>;
    }
  >;
}

/**
 * The result of merging a set of ops.
 *
 * Replaying the same ops in any order produces an equal State, so it does not
 * matter whether they arrive from the server's log, from a local edit, or from
 * both at once. Not reactive on purpose -- Svelte state wraps this, rather than
 * this being made of it, because a merge touches many slots per op and every
 * one of them would be a signal write.
 */
export class State {
  #nodes = new Map<string, Entry>();
  /**
   * Every op id applied, which is what makes apply() idempotent.
   *
   * Grows with the log and is never pruned. An id that fell out would let a
   * replay through, and a replay getting through is the thing rule three
   * exists to prevent.
   */
  #seen = new Set<string>();
  #clock = 0;

  /**
   * The highest clock seen.
   *
   * A replica issuing an op takes a clock strictly greater than this and
   * strictly greater than any it has issued in the same batch: two ops sharing
   * a clock and an actor are indistinguishable to the merge.
   */
  get clock(): number {
    return this.#clock;
  }

  /** How many nodes are known, tombstones included. */
  get size(): number {
    return this.#nodes.size;
  }

  applied(opId: string): boolean {
    return this.#seen.has(opId);
  }

  /**
   * Merges one op. Returns true if it was new.
   *
   * An op already applied returns false and touches nothing. A new op returns
   * true even if it changed no value -- a set-fields that lost every field to a
   * higher stamp is still accounted for, and must not be applied again.
   *
   * Throws on an invalid op, having changed nothing: validation runs before the
   * first write, so there is no half-applied op to undo.
   */
  apply(op: Op): boolean {
    validate(op);
    if (this.#seen.has(op.id)) return false;
    this.#seen.add(op.id);
    this.#clock = Math.max(this.#clock, op.clock);

    const stamp: Stamp = { clock: op.clock, actor: op.actor };
    const parent = op.parent ?? "";
    const position = op.position ?? "";

    switch (op.kind) {
      case "create-node": {
        const target = this.#touch(op.node);
        moveTo(target, parent, position, stamp);
        setFields(target, op.fields, stamp);
        break;
      }
      case "set-fields":
        setFields(this.#touch(op.node), op.fields, stamp);
        break;
      case "delete-node":
        // Tombstoning a node nobody has mentioned yet is deliberate: it is how
        // a delete that overtakes its create still wins when the create lands.
        this.#touch(op.node).deleted = true;
        break;
      case "move-node":
        moveTo(this.#touch(op.node), parent, position, stamp);
        break;
      case "extract-to-task": {
        const task = this.#touch(op.task as string);
        moveTo(task, parent, position, stamp);
        setFields(task, op.fields, stamp);
        setField(task, FIELD_EXTRACTED_FROM, op.node, stamp);
        // Extracting from a node that has since been deleted still produces
        // the task and still links back, so the tombstone records where its
        // content went.
        setField(this.#touch(op.node), FIELD_TASK_ID, op.task as string, stamp);
        break;
      }
    }
    return true;
  }

  /**
   * Merges ops in the order given and returns how many were new.
   *
   * Stops at the first invalid op and rethrows. Nothing needs unwinding: ops
   * are order-independent and idempotent, so replaying the whole batch after
   * fixing the bad one reaches the same State as never having seen it.
   */
  applyAll(ops: readonly Op[]): number {
    let applied = 0;
    for (const op of ops) if (this.apply(op)) applied++;
    return applied;
  }

  node(id: string): Node | null {
    const entry = this.#nodes.get(id);
    return entry ? snapshot(id, entry) : null;
  }

  /**
   * The live nodes reachable from the roots, in position order.
   *
   * Tombstones are not in it, and neither is anything underneath one: a child
   * created under a concurrently deleted node does not drag its parent back
   * into the tree. Those nodes are still here -- see detached().
   */
  tree(): TreeNode[] {
    const children = this.#liveChildren();
    const build = (ids: readonly string[]): TreeNode[] =>
      ids.map((id) => {
        const kids = children.get(id);
        return {
          ...snapshot(id, this.#nodes.get(id) as Entry),
          children: kids ? build(kids) : [],
        };
      });
    return build(children.get("") ?? []);
  }

  /**
   * Live nodes the tree cannot reach, ordered by id.
   *
   * Their parent is a tombstone, or is a node no op has ever mentioned, or
   * concurrent moves put them in a cycle. All three are what an eventually
   * consistent tree looks like mid-convergence and the last can outlive it, so
   * these are surfaced rather than dropped -- a viewer that ignores them is
   * showing an incomplete workspace.
   */
  detached(): Node[] {
    const children = this.#liveChildren();
    const reachable = new Set<string>();
    const queue = [...(children.get("") ?? [])];
    while (queue.length > 0) {
      const id = queue.pop() as string;
      if (reachable.has(id)) continue;
      reachable.add(id);
      const kids = children.get(id);
      if (kids) queue.push(...kids);
    }

    const out: Node[] = [];
    for (const id of [...this.#nodes.keys()].sort()) {
      const entry = this.#nodes.get(id) as Entry;
      if (!entry.deleted && !reachable.has(id)) out.push(snapshot(id, entry));
    }
    return out;
  }

  toJSON(): StateJSON {
    const nodes: StateJSON["nodes"] = {};
    for (const [id, entry] of this.#nodes) {
      nodes[id] = {
        parent: entry.at.parent,
        position: entry.at.position,
        stamp: entry.at.stamp,
        deleted: entry.deleted,
        fields: Object.fromEntries(entry.fields),
      };
    }
    return { clock: this.#clock, seen: [...this.#seen], nodes };
  }

  /**
   * Rebuilds a State that was written down.
   *
   * The stamps have to come back with it. A State restored from the rendered
   * tree alone would have every slot at the zero stamp, and the first op to
   * arrive from anybody else would win every field it touched regardless of
   * when it was written -- the merge would be right about the shape and wrong
   * about the content, which is the hardest kind of wrong to notice.
   */
  static fromJSON(value: unknown): State {
    const state = new State();
    const raw = value as Partial<StateJSON> | null;
    if (!raw || typeof raw !== "object") return state;

    state.#clock = typeof raw.clock === "number" && raw.clock >= 0 ? raw.clock : 0;
    for (const id of Array.isArray(raw.seen) ? raw.seen : []) {
      if (typeof id === "string") state.#seen.add(id);
    }
    const nodes = raw.nodes && typeof raw.nodes === "object" ? raw.nodes : {};
    for (const [id, stored] of Object.entries(nodes)) {
      if (!stored || typeof stored !== "object") continue;
      const fields = new Map<string, Slot>();
      for (const [name, slot] of Object.entries(stored.fields ?? {})) {
        if (slot && typeof slot === "object" && "stamp" in slot) {
          fields.set(name, { value: slot.value, stamp: asStamp(slot.stamp) });
        }
      }
      state.#nodes.set(id, {
        at: {
          parent: typeof stored.parent === "string" ? stored.parent : "",
          position: typeof stored.position === "string" ? stored.position : "",
          stamp: asStamp(stored.stamp),
        },
        fields,
        deleted: stored.deleted === true,
      });
    }
    return state;
  }

  #touch(id: string): Entry {
    const existing = this.#nodes.get(id);
    if (existing) return existing;
    // Every kind creates on demand, including the ones that only edit: ops
    // arrive out of causal order, and dropping an edit that overtook its
    // create loses data permanently for a message that was merely early.
    const created: Entry = {
      at: { parent: "", position: "", stamp: ZERO_STAMP },
      fields: new Map(),
      deleted: false,
    };
    this.#nodes.set(id, created);
    return created;
  }

  /**
   * Non-tombstoned nodes indexed by parent, each list in render order.
   *
   * A node whose parent is a tombstone still appears under it here; the walk
   * simply never reaches that parent.
   */
  #liveChildren(): Map<string, string[]> {
    const children = new Map<string, string[]>();
    for (const [id, entry] of this.#nodes) {
      if (entry.deleted) continue;
      const list = children.get(entry.at.parent);
      if (list) list.push(id);
      else children.set(entry.at.parent, [id]);
    }
    for (const siblings of children.values()) {
      // The id tiebreak is not cosmetic: two replicas can pick the same
      // position, and without a deterministic second key those two lines would
      // render in insertion order -- differently on every machine.
      siblings.sort((a, b) => {
        const pa = this.#nodes.get(a)?.at.position ?? "";
        const pb = this.#nodes.get(b)?.at.position ?? "";
        if (pa !== pb) return pa < pb ? -1 : 1;
        return a < b ? -1 : a > b ? 1 : 0;
      });
    }
    return children;
  }
}

function moveTo(entry: Entry, parent: string, position: string, stamp: Stamp): void {
  if (!after(stamp, entry.at.stamp)) return;
  entry.at = { parent, position, stamp };
}

function setFields(entry: Entry, fields: Record<string, unknown> | undefined, stamp: Stamp): void {
  if (!fields) return;
  for (const [name, value] of Object.entries(fields)) setField(entry, name, value, stamp);
}

/**
 * Writes one field, if this stamp beats the one already there.
 *
 * Deletion is deliberately not consulted. Having a tombstone swallow later
 * writes reads like what "delete beats an edit" means, and it does not
 * converge -- the tombstone would keep whichever fields happened to arrive
 * before the delete, which differs per replica. Rule two is about the node's
 * existence, and the tombstone already decides that.
 */
function setField(entry: Entry, name: string, value: unknown, stamp: Stamp): void {
  const current = entry.fields.get(name);
  if (current && !after(stamp, current.stamp)) return;
  entry.fields.set(name, { value, stamp });
}

function snapshot(id: string, entry: Entry): Node {
  return {
    id,
    parent: entry.at.parent,
    position: entry.at.position,
    // Sorted by name. A Map iterates in insertion order, which is the order
    // the ops happened to arrive in -- so two replicas holding identical
    // documents would produce objects whose keys are in different orders, and
    // anything comparing or serialising them would call them different. The
    // Go side gets this for free from encoding/json, which sorts map keys.
    fields: Object.fromEntries(
      [...entry.fields]
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([name, slot]) => [name, slot.value]),
    ),
    deleted: entry.deleted,
  };
}

function asStamp(value: unknown): Stamp {
  const r = value as Partial<Stamp> | null;
  if (!r || typeof r !== "object") return ZERO_STAMP;
  return {
    clock: typeof r.clock === "number" && r.clock > 0 ? r.clock : 0,
    actor: typeof r.actor === "string" ? r.actor : "",
  };
}
