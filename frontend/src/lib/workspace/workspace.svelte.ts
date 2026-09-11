/**
 * One workspace: its document, which mode it is showing, and its connection to
 * the server that holds it.
 *
 * Every edit goes through a method here, and every method does the same three
 * things in the same order -- mint the op, merge it locally, hand it to the
 * outbox. Components read `tree`, `rows` and `tasks` and never write. That is
 * what makes the pending count trustworthy: there is no way to change a
 * workspace that does not also count as a change.
 */
import {
  FIELD_COLLAPSED,
  FIELD_STATUS,
  FIELD_TEXT,
  FIELD_TYPE,
  MODES,
  findInTree,
  isCollapsed,
  isTask,
  newId,
  outlineRows,
  planTasks,
  sourceIdOf,
  statusOf,
  taskIdOf,
  textOf,
  type Mode,
  type Row,
  type TaskState,
} from "./model";
import { afterIndex, atEnd, atStart, keyFor, stepped, type Bounds } from "./bounds";
import { State, type Node, type Op, type TreeNode } from "./ops";
import { Replica, actorId } from "./replica";
import { Sync, type Outgoing } from "./sync.svelte";
import { httpTransport, type Transport } from "./transport";

/**
 * How long typing settles before it counts as an edit worth sending.
 *
 * A `set-fields` carries the whole line, so one per keystroke would be correct
 * and would also make the pending badge count keystrokes -- a number nobody
 * can act on, attached to a word that promises they might. It would also burn
 * a sequence number and an op id per character, forever, in an append-only log.
 */
const TYPING_SETTLE_MS = 400;

/** Everything about a workspace that survives a restart. */
export interface WorkspaceSnapshot {
  id: string;
  name: string;
  /** The sync server, or null for a workspace that has never been shared. */
  base: string | null;
  key: string | null;
  mode: Mode;
  seq: number;
  /** The merged state, stamps and all. See State.fromJSON for why stamps. */
  state: unknown;
  outbox: Outgoing[];
  /**
   * Lines that were mid-typing when this was written.
   *
   * Kept because closing the window is not a reason to lose the sentence
   * somebody was halfway through. They come back as drafts and settle into
   * ops the moment anything touches them again.
   */
  drafts: Record<string, string>;
}

export class Workspace {
  readonly id: string;

  name = $state("");

  /**
   * Which mode this tab is showing.
   *
   * Per workspace, not per window, and written down with the rest of it. A tab
   * you left in planning is a tab you were planning in; coming back to it in
   * idea mode means finding your place again on every switch, which is the one
   * thing tabs are supposed to save you.
   */
  mode = $state<Mode>("idea");

  base = $state<string | null>(null);
  key = $state<string | null>(null);

  readonly sync: Sync;

  /**
   * The merged document.
   *
   * `$state.raw` and rebuilt rather than mutated: the merge is not reactive --
   * see ops.ts, where one op touches many slots -- so the tree is derived from
   * it after each batch and swapped in whole. A deeply reactive tree would
   * turn a hundred-op catch-up into thousands of signal writes for one repaint.
   */
  tree = $state.raw<TreeNode[]>([]);

  /**
   * Live nodes the tree cannot reach: their parent is a tombstone, or is a
   * node no op has mentioned yet, or two concurrent moves made a cycle.
   *
   * Shown rather than dropped, because state.go is explicit that a viewer
   * ignoring these is showing an incomplete workspace -- and "my line
   * disappeared" is the worst bug a shared outliner can have.
   */
  detached = $state.raw<Node[]>([]);

  /**
   * Lines being typed right now, by node id.
   *
   * Typing does not mint an op per keystroke. It could -- the merge would
   * cope -- but every op id is remembered forever so that a replay cannot get
   * through, so a keystroke each would grow that set by a few hundred entries
   * per paragraph and never let one go. So the characters live here until the
   * typing settles, and everything that renders a line reads text() rather
   * than the merged node.
   */
  drafts = $state<Record<string, string>>({});

  /** Bumped by every change. Cheap for a save-on-change to watch. */
  revision = $state(0);

  /**
   * A line the outline should open on, set by whoever sent you there.
   *
   * The plan links back to the idea a task came from, and following that link
   * is a mode switch -- so the outline is not on screen at the moment the link
   * is clicked and cannot be told to focus anything. This is the note it finds
   * when it mounts. Cleared by whoever acts on it, and not written down: a
   * request to look at something is about this minute, not about the document.
   */
  revealRequest = $state<string | null>(null);

  /** Sends the outline to a line, from wherever you are. */
  requestReveal(id: string): void {
    this.reveal(id);
    this.revealRequest = id;
    this.mode = "idea";
  }

  #state: State;
  #replica: Replica;
  /** Field writes waiting to settle, by node id. See TYPING_SETTLE_MS. */
  #typing = new Map<string, ReturnType<typeof setTimeout>>();

  constructor(snapshot: WorkspaceSnapshot) {
    this.id = snapshot.id;
    this.name = snapshot.name;
    this.mode = MODES.includes(snapshot.mode) ? snapshot.mode : "idea";
    this.base = snapshot.base;
    this.key = snapshot.key;

    this.#state = State.fromJSON(snapshot.state);
    this.drafts = { ...snapshot.drafts };
    this.#replica = new Replica(actorId(), () => this.#state.clock);

    this.sync = new Sync((ops) => this.#merge(ops));
    this.sync.outbox = snapshot.outbox;
    // The outbox was merged before it was written down, so it is already in
    // the state -- replaying it here would be a no-op by rule three anyway.
    this.#refresh();
    this.sync.configure(this.#transport(), snapshot.seq);
  }

  /** A brand new, unshared workspace. */
  static create(name: string): Workspace {
    return new Workspace({
      id: newId(),
      name,
      base: null,
      key: null,
      mode: "idea",
      seq: 0,
      state: null,
      outbox: [],
      drafts: {},
    });
  }

  /** A workspace joined by key. Its content arrives from the log. */
  static join(name: string, base: string, key: string): Workspace {
    return new Workspace({
      id: newId(),
      name,
      base,
      key,
      mode: "idea",
      seq: 0,
      state: null,
      outbox: [],
      drafts: {},
    });
  }

  snapshot(): WorkspaceSnapshot {
    return {
      id: this.id,
      name: this.name,
      base: this.base,
      key: this.key,
      mode: this.mode,
      seq: this.sync.seq,
      state: this.#state.toJSON(),
      outbox: this.sync.outbox,
      drafts: this.drafts,
    };
  }

  /** The outline, flattened to what is on screen, in keyboard order. */
  rows = $derived<Row[]>(outlineRows(this.tree));

  /** The plan, in order. */
  tasks = $derived<TreeNode[]>(planTasks(this.tree));

  /** How much of the outline is still folded away, for the "expand all" button. */
  foldedCount = $derived(countFolded(this.tree));

  /**
   * Points this workspace at a server. Also the way out of a refused key.
   *
   * The sequence number resets to zero, so the whole log is read back. That is
   * the right default for a key change -- the new key may see a different
   * workspace entirely -- and it is cheap for the merge, which is idempotent
   * by op id and will recognise everything it already has.
   */
  connect(base: string, key: string): void {
    this.base = base;
    this.key = key;
    this.sync.configure(this.#transport(), 0);
    this.revision++;
  }

  /**
   * The ops that would rebuild this workspace from nothing.
   *
   * Needed because sharing a workspace that already has something in it is a
   * real thing to want, and the merge does not keep the ops it merged -- only
   * their ids, and only so a replay cannot get through twice. So the current
   * document is turned back into a set of creates: one per live node, in
   * parent-first order, carrying the fields it has now.
   *
   * These are new ops with new ids and fresh clocks, which is correct rather
   * than a shortcut. They are being sent to a workspace that has never seen
   * this content; reusing the original ids would claim a history the server's
   * log does not have.
   */
  seedOps(): Op[] {
    const ops: Op[] = [];
    const walk = (nodes: readonly TreeNode[], parent: string) => {
      for (const node of nodes) {
        // Parent-first, so no create ever refers to a node that is not there
        // yet. The merge would cope with the other order -- it creates on
        // demand -- but a log that reads in causal order is one a person can
        // debug.
        ops.push(this.#replica.create(node.id, parent, node.position, { ...node.fields }));
        walk(node.children, node.id);
      }
    };
    walk(this.tree, "");
    return ops;
  }

  /**
   * Puts an existing local workspace onto a server.
   *
   * The document goes with it, and has to: an unshared workspace queues
   * nothing -- see Sync.record -- so there is no history to replay, and
   * without seedOps this would connect an outline full of work to an empty
   * log and then sync the emptiness in both directions. That is the one
   * outcome nobody would forgive.
   */
  share(base: string, key: string): void {
    const seed = this.seedOps();
    this.connect(base, key);
    for (const op of seed) this.#emit(op);
  }

  #transport(): Transport | null {
    if (!this.base || !this.key) return null;
    return httpTransport(this.base, this.key);
  }

  /* ---------------------------------------------------------------------- */
  /* The outline                                                            */
  /* ---------------------------------------------------------------------- */

  /**
   * Adds a line after `afterId`, at the same depth, and returns its id.
   *
   * A null `afterId` means the very top. A line with expanded children gets
   * the new one as its first child rather than as its next sibling, which is
   * what pressing Enter on a heading is asking for -- the alternative drops it
   * below the whole branch, a screenful from the caret.
   */
  insertAfter(afterId: string | null, text = ""): string {
    this.#settleAll();
    const id = newId();

    if (afterId === null) {
      this.#emit(
        this.#replica.create(id, "", keyFor(atStart(this.#siblingsOf(""))), {
          [FIELD_TEXT]: text,
        }),
      );
      return id;
    }

    const node = findInTree(this.tree, afterId);
    if (!node) return "";

    // A line with children open gets the new one as its first child. Enter on
    // a heading is asking for that; the alternative drops the new line below
    // the whole branch, a screenful from the caret.
    const [parent, bounds]: [string, Bounds] =
      node.children.length > 0 && !isCollapsed(node)
        ? [node.id, atStart(node.children)]
        : this.#slotAfter(node);

    this.#emit(this.#replica.create(id, parent, keyFor(bounds), { [FIELD_TEXT]: text }));
    return id;
  }

  /**
   * The text of a line, as it should be shown.
   *
   * The draft first, so a line being typed reads the same in the plan's "from"
   * line as it does under the caret, and only then the merged node.
   */
  text(id: string): string {
    const draft = this.drafts[id];
    if (draft !== undefined) return draft;
    return textOf(findInTree(this.tree, id) ?? this.#detachedNode(id));
  }

  /**
   * Takes what has been typed into a line.
   *
   * Held as a draft and sent as one op once the typing stops -- see `drafts`.
   * The op carries the whole line, so the one that eventually goes is the line
   * as it ends up; no intermediate state is anybody else's business.
   */
  setText(id: string, text: string): void {
    if (this.text(id) === text) return;
    this.drafts[id] = text;
    this.revision++;
    this.#settleLater(id);
  }

  /**
   * Makes a line a child of the line above it.
   *
   * Refused for the first among its siblings: there is nothing at its own
   * depth above it to become a child of, and reaching further up to a
   * shallower row would jump the line across a branch boundary on a keypress
   * meant to nudge it one step.
   */
  indent(id: string): boolean {
    this.#settleAll();
    const row = this.rows.find((r) => r.node.id === id);
    if (!row || row.index === 0) return false;

    const siblings = this.#siblingsOf(row.parentId);
    const parent = siblings[row.index - 1];
    // A folded new parent would swallow the line whole -- the caret would be
    // inside something that is not drawn. Unfolded first, and as a real op, so
    // everybody else's outline opens with it too.
    if (isCollapsed(parent)) this.setCollapsed(parent.id, false);
    this.#emit(this.#replica.move(id, parent.id, keyFor(atEnd(parent.children))));
    return true;
  }

  /** Makes a line the next sibling of its parent. Refused at the top level. */
  outdent(id: string): boolean {
    this.#settleAll();
    const row = this.rows.find((r) => r.node.id === id);
    if (!row || row.parentId === "") return false;

    const parentRow = this.rows.find((r) => r.node.id === row.parentId);
    if (!parentRow) return false;
    // Deliberately not Workflowy's rule, which also adopts the parent's
    // following siblings as children of the outdented line. That is right when
    // restructuring a long document and startling on a single keypress: two
    // lines you were not looking at move as well.
    const [grandparent, bounds] = this.#slotAfter(parentRow.node);
    this.#emit(this.#replica.move(id, grandparent, keyFor(bounds)));
    return true;
  }

  /** Swaps a line with the sibling above or below. Stays within its parent. */
  moveBy(id: string, delta: -1 | 1): boolean {
    this.#settleAll();
    const row = this.rows.find((r) => r.node.id === id);
    if (!row) return false;
    const target = row.index + delta;
    if (target < 0 || target >= row.siblings) return false;

    const bounds = stepped(this.#siblingsOf(row.parentId), row.index, delta);
    if (!bounds) return false;
    this.#emit(this.#replica.move(id, row.parentId, keyFor(bounds)));
    return true;
  }

  setCollapsed(id: string, collapsed: boolean): boolean {
    const node = findInTree(this.tree, id);
    if (!node || node.children.length === 0 || isCollapsed(node) === collapsed) return false;
    return this.#emit(this.#replica.setFields(id, { [FIELD_COLLAPSED]: collapsed }));
  }

  /**
   * Unfolds everything, at every depth.
   *
   * Walks the tree rather than the visible rows, because the folded branches
   * inside a folded branch are exactly the ones `rows` does not contain --
   * unfolding what is on screen and looking again would take as many passes as
   * the outline is deep.
   */
  unfoldAll(): number {
    let opened = 0;
    const walk = (nodes: readonly TreeNode[]) => {
      for (const node of nodes) {
        if (isCollapsed(node) && node.children.length > 0) {
          this.#emit(this.#replica.setFields(node.id, { [FIELD_COLLAPSED]: false }));
          opened++;
        }
        walk(node.children);
      }
    };
    // Snapshotted first: #emit rebuilds `tree`, so walking it live would be
    // walking a tree that is replaced underneath the recursion.
    walk(this.tree);
    return opened;
  }

  /** Unfolds every ancestor of a line, so it can be scrolled to and focused. */
  reveal(id: string): void {
    // The chain is walked rather than read off `rows`, because a folded
    // ancestor is exactly what keeps this line out of `rows` in the first
    // place -- the thing that needs opening is not in the list to look up.
    const chain: TreeNode[] = [];
    const dig = (nodes: readonly TreeNode[]): boolean => {
      for (const candidate of nodes) {
        if (candidate.id === id) return true;
        chain.push(candidate);
        if (dig(candidate.children)) return true;
        chain.pop();
      }
      return false;
    };
    if (!dig(this.tree)) return;
    for (const ancestor of chain) this.setCollapsed(ancestor.id, false);
  }

  /**
   * Removes a line and everything under it.
   *
   * One tombstone on the line itself, not one per descendant: the merge does
   * not reach past a tombstone, so the branch goes with it. That also means a
   * line somebody else adds under it concurrently is not resurrected -- it
   * becomes detached, and is shown as such rather than vanishing.
   */
  remove(id: string): boolean {
    this.#settle(id);
    if (!findInTree(this.tree, id) && !this.#detachedNode(id)) return false;
    return this.#emit(this.#replica.remove(id));
  }

  /* ---------------------------------------------------------------------- */
  /* The plan                                                               */
  /* ---------------------------------------------------------------------- */

  /**
   * Turns a line of the outline into a task at the end of the plan.
   *
   * `extract-to-task` rather than a create plus two field writes, because the
   * protocol has a kind for exactly this and it stamps both ends of the link
   * together -- the task's `extractedFrom` and the idea's `taskId` cannot
   * disagree if they were written by the same op.
   *
   * The link is the whole point. A plan is a list of things to do, and six
   * weeks later "why is this on here" is answered by the paragraph it came out
   * of, not by the eight words that fitted on the task.
   *
   * A line already on the plan is not extracted twice: the second press is
   * almost always somebody checking whether the first one worked.
   */
  promote(nodeId: string): string | null {
    this.#settleAll();
    const node = findInTree(this.tree, nodeId);
    if (!node) return null;

    const existing = taskIdOf(node);
    if (existing && this.tasks.some((t) => t.id === existing)) return existing;

    const task = newId();
    this.#emit(
      this.#replica.extract(nodeId, task, "", keyFor(atEnd(this.tasks)), {
        [FIELD_TYPE]: "task",
        [FIELD_TEXT]: this.text(nodeId).trim(),
        [FIELD_STATUS]: "todo",
      }),
    );
    return task;
  }

  /** A task with no idea behind it. Typed straight into the plan. */
  addTask(title = ""): string {
    this.#settleAll();
    const id = newId();
    this.#emit(
      this.#replica.create(id, "", keyFor(atEnd(this.tasks)), {
        [FIELD_TYPE]: "task",
        [FIELD_TEXT]: title,
        [FIELD_STATUS]: "todo",
      }),
    );
    return id;
  }

  setTaskTitle(id: string, title: string): void {
    this.setText(id, title);
  }

  setTaskState(id: string, status: TaskState): boolean {
    const task = this.tasks.find((t) => t.id === id);
    if (!task || statusOf(task) === status) return false;
    return this.#emit(this.#replica.setFields(id, { [FIELD_STATUS]: status }));
  }

  moveTaskBy(id: string, delta: -1 | 1): boolean {
    this.#settleAll();
    const tasks = this.tasks;
    const from = tasks.findIndex((t) => t.id === id);
    if (from < 0) return false;
    const to = from + delta;
    if (to < 0 || to >= tasks.length) return false;

    const bounds = stepped(tasks, from, delta);
    if (!bounds) return false;
    return this.#emit(this.#replica.move(id, "", keyFor(bounds)));
  }

  removeTask(id: string): boolean {
    return this.remove(id);
  }

  /** The idea a task came out of, or null if there was none or it is gone. */
  sourceOf(task: Node): TreeNode | null {
    const id = sourceIdOf(task);
    return id ? findInTree(this.tree, id) : null;
  }

  /** True if this line is already on the plan, so its button can say so. */
  taskFor(nodeId: string): TreeNode | null {
    const node = findInTree(this.tree, nodeId);
    const id = node ? taskIdOf(node) : null;
    return id ? (this.tasks.find((t) => t.id === id) ?? null) : null;
  }

  /** Lets go of any timers, flushing what they were holding. */
  dispose(): void {
    this.#settleAll();
  }

  /* ---------------------------------------------------------------------- */

  /** Merge, then queue. The only path a change may take. */
  #emit(op: Op): boolean {
    // apply() validates, and validation can fail on an op this app built: the
    // protocol caps a sort key at 256 bytes, and inserting between the same
    // two lines about fifteen hundred times running -- never appending, never
    // moving anything -- eventually produces one longer than that. Nothing
    // else here can breach a limit. It is not worth engineering around, and
    // it is worth not throwing out of a keystroke handler for: the edit is
    // refused and the caller, which already handles a refusal, carries on.
    let applied = false;
    try {
      applied = this.#state.apply(op);
    } catch {
      return false;
    }
    if (!applied) return false;
    this.#refresh();
    this.sync.record(op);
    return true;
  }

  #merge(ops: readonly Op[]): void {
    let changed = 0;
    for (const op of ops) {
      try {
        if (this.#state.apply(op)) changed++;
      } catch {
        // An op this version cannot apply -- written by a newer release, or
        // malformed. Skipped rather than allowed to stop the catch-up: the
        // rest of the log is still readable, and refusing all of it over one
        // op would leave the workspace blank instead of nearly right.
      }
    }
    if (changed > 0) this.#refresh();
  }

  #refresh(): void {
    this.tree = this.#state.tree();
    this.detached = this.#state.detached();
    this.revision++;
  }

  #detachedNode(id: string): Node | null {
    return this.detached.find((n) => n.id === id) ?? null;
  }

  /** The live children of a parent, in render order. "" is the top level. */
  #siblingsOf(parentId: string): TreeNode[] {
    if (parentId === "") return this.tree.filter((n) => !isTask(n));
    return findInTree(this.tree, parentId)?.children ?? [];
  }

  /**
   * Where a new sibling immediately after `node` goes: whose child it is, and
   * which two keys it falls between.
   *
   * Returned as a pair rather than as three loose strings, because the parent
   * and the bounds are used at opposite ends of the same call and a flat
   * triple is one destructuring slip away from passing a node id where a sort
   * key belongs -- which produces a position that is merely strange rather
   * than an error anything catches.
   */
  #slotAfter(node: TreeNode): [parent: string, bounds: Bounds] {
    const row = this.rows.find((r) => r.node.id === node.id);
    const parent = row?.parentId ?? "";
    const siblings = this.#siblingsOf(parent);
    return [parent, afterIndex(siblings, siblings.findIndex((s) => s.id === node.id))];
  }

  #settleLater(id: string): void {
    const running = this.#typing.get(id);
    if (running !== undefined) clearTimeout(running);
    this.#typing.set(
      id,
      setTimeout(() => {
        this.#typing.delete(id);
        this.#flushDraft(id);
      }, TYPING_SETTLE_MS),
    );
  }

  /**
   * Sends a settling edit now.
   *
   * Called before any structural op, so the outbox keeps the order the person
   * made them in. Without it, typing a line and immediately indenting it sends
   * the move first and the text after -- harmless for those two, and not
   * harmless before a delete, where the text op would arrive with a higher
   * clock than the tombstone it was meant to precede.
   */
  #settle(id: string): void {
    const timer = this.#typing.get(id);
    if (timer === undefined) return;
    clearTimeout(timer);
    this.#typing.delete(id);
    this.#flushDraft(id);
  }

  /**
   * Turns a draft into the one op that carries it.
   *
   * The draft is only dropped after the op has merged, so nothing renders the
   * old text for a frame in between -- and a line whose draft matches what the
   * merge already has produces no op at all, which is the case where somebody
   * typed a character and took it back again.
   */
  #flushDraft(id: string): void {
    const draft = this.drafts[id];
    if (draft === undefined) return;
    const node = findInTree(this.tree, id) ?? this.#detachedNode(id);
    if (node && textOf(node) !== draft) {
      this.#emit(this.#replica.setFields(id, { [FIELD_TEXT]: draft }));
    }
    delete this.drafts[id];
    this.revision++;
  }

  #settleAll(): void {
    for (const id of [...this.#typing.keys()]) this.#settle(id);
  }
}

function countFolded(tree: readonly TreeNode[]): number {
  let n = 0;
  for (const node of tree) {
    if (isCollapsed(node) && node.children.length > 0) n++;
    n += countFolded(node.children);
  }
  return n;
}
