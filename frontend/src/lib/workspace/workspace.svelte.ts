/**
 * One tab's outline: its document, and which mode it is showing.
 *
 * **Local. Nothing here reaches a server.** This used to own a transport, an
 * outbox and a sequence number, and that was the second sync loop -- pointed
 * at the same workspace the Go side was already pushing to, with its own
 * actor and its own clock. The workspace, its keys, its queue and its merge
 * are Go's now; see workspaces.svelte.ts.
 *
 * What stayed is the Idea and Planning outline, which is **local to this
 * machine**: it persists in localStorage, it does not sync, and nothing in
 * the app shows it as though it did.
 *
 * That was once because there was nowhere to put it. It is not any more --
 * `WorkbenchService.ApplyWorkspaceEdits(tabID, edits)` landed, it takes the
 * same five op kinds this file mints, and it returns the merged document. So
 * the outline *can* now go through the one queue and the one merge in Go, and
 * it should. What is below has simply not been moved yet, and moving it is
 * more than swapping the call: the merge currently happens here and
 * synchronously, so every method returns a document that already includes the
 * edit, while ApplyWorkspaceEdits is a round trip. Typing has to stay
 * optimistic across it, ErrNoTransport has to keep an unjoined machine
 * working exactly as it does today, and storage.ts has to stop being the
 * document's home without losing the notes already in it.
 *
 * Until that is done this is a per-tab local document, and the comments below
 * describe what it does rather than what it ought to.
 *
 * The op machinery below stays because the document is a tree of the same
 * shape, and rebuilding it as a plain tree would be a rewrite that changes
 * nothing anyone can see -- and would have to be undone the day the outline
 * gets a home in Go. It is a local document model now rather than a replica:
 * ops are minted, applied and forgotten, and none of them is ever sent or
 * received.
 *
 * Every edit goes through a method here. Components read `tree`, `rows` and
 * `tasks` and never write.
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
import type { ProposedOp } from "./proposal";
import { editOf, opsOf } from "./outbound";
import type { Document, Edit } from "../../../bindings/dev.jevido/work/internal/workbench/models.js";
import { State, type Node, type Op, type TreeNode } from "./ops";
import { Replica, actorId } from "./replica";

/**
 * How long typing settles before it counts as an edit worth sending.
 *
 * A `set-fields` carries the whole line, so one per keystroke would be correct
 * and would also make the pending badge count keystrokes -- a number nobody
 * can act on, attached to a word that promises they might. It would also burn
 * a sequence number and an op id per character, forever, in an append-only log.
 */
const TYPING_SETTLE_MS = 400;

/**
 * Everything about a tab's outline that survives a restart.
 *
 * No id, no name, no server and no key. The id is the key this is filed
 * under, the name is the tab's and comes from Go, and the credentials are not
 * this side's to hold -- see storage.ts.
 */
export interface OutlineSnapshot {
  mode: Mode;
  /** The merged state, stamps and all. See State.fromJSON for why stamps. */
  state: unknown;
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

  /**
   * Whether this machine has a project folder for this tab.
   *
   * Per-machine and set from Go's TabView: a tab a colleague made arrives
   * unbound, and agents cannot run in it until someone points it at a project
   * here. Nothing about the outline needs it; the Work pane does.
   */
  bound = $state(true);

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

  /**
   * @param id The tab this outline belongs to. Go's tab id when a workspace is
   *   joined, and the fixed local one when none is -- see LOCAL_TAB.
   * @param snapshot What storage had for that id, or null for a fresh tab.
   */
  /**
   * Where an edit goes to become an op, or null on a machine that has joined
   * no workspace.
   *
   * Injected rather than imported so that "unjoined" is the absence of a
   * collaborator instead of an error code checked in eighteen places. Work is a
   * complete local tool with nothing joined and stays one: with no send, every
   * method below behaves exactly as it did before any of this existed.
   */
  send: ((edits: Edit[]) => Promise<Document | null>) | null = null;

  /**
   * Lines that changed elsewhere while somebody was in them.
   *
   * Node id to the text the document now holds. A marker rather than an
   * overwrite: what is under the caret belongs to the person typing, and a poll
   * arriving mid-word is not a reason to take a word off them. Cleared by
   * leaving the line, or by taking what arrived.
   */
  marks = $state<Record<string, string>>({});

  /**
   * What the document said when the caret arrived, per line being edited.
   *
   * Compared against rather than the draft: the draft is what is being typed
   * and changes constantly, so a comparison with it would mark a line every
   * time anybody's own keystroke settled.
   */
  #baseline: Record<string, string> = {};

  /** Edits waiting to cross, oldest first. */
  #outbound: Edit[] = [];
  /** True while a batch is in flight, which is what serialises them. */
  #sending = false;

  constructor(id: string, snapshot: OutlineSnapshot | null) {
    this.id = id;
    this.mode = snapshot && MODES.includes(snapshot.mode) ? snapshot.mode : "idea";
    this.#state = State.fromJSON(snapshot?.state ?? null);
    this.drafts = { ...(snapshot?.drafts ?? {}) };
    this.#replica = new Replica(actorId(), () => this.#state.clock);
    this.#refresh();
    // Restoring is not an edit. #refresh counts one, so it is taken back --
    // otherwise every tab looks dirty the moment it is opened and the
    // debounced save writes the whole store on startup for nothing.
    this.revision = 0;
  }

  snapshot(): OutlineSnapshot {
    return {
      mode: this.mode,
      state: this.#state.toJSON(),
      drafts: this.drafts,
    };
  }

  /** The outline, flattened to what is on screen, in keyboard order. */
  rows = $derived<Row[]>(outlineRows(this.tree));

  /** The plan, in order. */
  tasks = $derived<TreeNode[]>(planTasks(this.tree));

  /** How much of the outline is still folded away, for the "expand all" button. */
  foldedCount = $derived(countFolded(this.tree));

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
  /**
   * The caret arrived. The line stops taking the document's value until it
   * leaves.
   *
   * Holding a draft for the whole time the line is focused is what makes that
   * true: text() prefers a draft, so a document adopted underneath changes
   * nothing on screen. A draft equal to the node emits nothing when it settles,
   * so this costs no ops.
   */
  enter(id: string): void {
    const now = this.text(id);
    this.#baseline[id] = now;
    if (this.drafts[id] === undefined) {
      this.drafts[id] = now;
      this.revision++;
    }
  }

  /**
   * The caret left. What was typed becomes an ordinary edit, and the marker
   * goes with it.
   *
   * Deliberately not a second conflict system: a line that changed elsewhere
   * and was then typed over resolves the way every other edit does -- by clock,
   * through the merge, with a note if it loses. The marker is about the moment
   * of typing.
   */
  leave(id: string): void {
    this.#settle(id);
    delete this.#baseline[id];
    if (this.marks[id] !== undefined) {
      const { [id]: _gone, ...rest } = this.marks;
      this.marks = rest;
    }
  }

  /** Takes what arrived, discarding what was being typed. */
  takeArrived(id: string): void {
    const arrived = this.marks[id];
    if (arrived === undefined) return;
    delete this.drafts[id];
    this.#baseline[id] = arrived;
    const { [id]: _gone, ...rest } = this.marks;
    this.marks = rest;
    this.revision++;
  }

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

  /* ---------------------------------------------------------------------- */
  /* Applying what Claude proposed                                          */
  /* ---------------------------------------------------------------------- */

  /**
   * Turns one approved proposed op into a real one.
   *
   * This is the only door a proposal comes through, and it is a narrow one on
   * purpose. Everything a proposal says is *relative* -- this node, under that
   * node, after this sibling -- and everything absolute about the resulting op
   * is minted here, now, against the document as it stands: the op id, the
   * actor, the Lamport clock and above all the sort key. A proposal never
   * carries a position (./proposal.ts refuses one), so there is no path by
   * which a key computed against a stale document can reach the log.
   *
   * It also means an approved restructuring is indistinguishable, in the log,
   * from the same edits made by hand. There is no "written by Claude" stamp on
   * these ops and there is nowhere to put one: the op envelope carries a
   * replica, not an author, and the document records what changed rather than
   * who changed it.
   *
   * @param refs Ids the earlier ops of this proposal created, by the ref they
   *   named themselves with. Written to as well as read: an insert with a ref
   *   registers the id it got, so a later op in the same proposal can put
   *   something under a line that did not exist when the proposal was written.
   * @returns false when the edit was refused, which the review reports as a
   *   failed row rather than throwing out of a click handler.
   */
  applyProposed(op: ProposedOp, refs: Map<string, string>): boolean {
    // Anything half-typed goes in first, so the proposal's ops carry higher
    // clocks than the edits they were written against -- see #settle.
    this.#settleAll();

    /** A proposal id: a real node, or one an earlier op in this set made. */
    const real = (id: string): string => refs.get(id) ?? id;

    switch (op.kind) {
      case "set-text": {
        const node = real(op.node);
        if (!findInTree(this.tree, node)) return false;
        return this.#emit(this.#replica.setFields(node, { [FIELD_TEXT]: op.text }));
      }

      case "insert": {
        const parent = op.parent === "" ? "" : real(op.parent);
        const bounds = this.#slotIn(parent, op.after === null ? null : real(op.after));
        if (!bounds) return false;
        const id = newId();
        if (!this.#emit(this.#replica.create(id, parent, keyFor(bounds), { [FIELD_TEXT]: op.text }))) {
          return false;
        }
        if (op.ref) refs.set(op.ref, id);
        return true;
      }

      case "move": {
        const node = real(op.node);
        const parent = op.parent === "" ? "" : real(op.parent);
        const moving = findInTree(this.tree, node);
        if (!moving) return false;
        // Refused rather than merged. A move into a node's own subtree is a
        // cycle, and the merge resolves a cycle by detaching the branch --
        // which on screen is lines vanishing. Review catches this before the
        // row can be ticked; this is the same answer at the last door, for a
        // document that changed between the check and the press.
        if (parent === node) return false;
        if (parent !== "" && findInTree(moving.children, parent)) return false;
        const bounds = this.#slotIn(parent, op.after === null ? null : real(op.after));
        if (!bounds) return false;
        return this.#emit(this.#replica.move(node, parent, keyFor(bounds)));
      }

      case "delete":
        return this.remove(real(op.node));

      case "promote":
        return this.promote(real(op.node)) !== null;

      case "set-status":
        return this.setTaskState(real(op.node), op.status);
    }
  }

  /**
   * Where a line goes inside `parentId`, relative to a sibling.
   *
   * Null when the named sibling is not in fact a sibling -- a proposal that
   * says "after X, under Y" while X sits under something else is describing a
   * document this is not, and guessing which half it meant would put the line
   * somewhere nobody asked for.
   */
  #slotIn(parentId: string, afterId: string | null): Bounds | null {
    const siblings = this.#siblingsOf(parentId);
    if (afterId === null) return atStart(siblings);
    const at = siblings.findIndex((s) => s.id === afterId);
    if (at < 0) return null;
    return afterIndex(siblings, at);
  }

  /** Lets go of any timers, flushing what they were holding. */
  dispose(): void {
    this.#settleAll();
  }

  /* ---------------------------------------------------------------------- */

  /**
   * Merges one op into the local document. The only path a change may take.
   *
   * There is no queue behind this: an edit here changes this machine's
   * outline and stops. The other half is `ApplyWorkspaceEdits`, which now
   * exists and is not yet called from here -- see the note at the top of this
   * file for what moving onto it involves.
   */
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

    // A change this machine made on purpose is shown, even into a line with a
    // caret in it. The marker exists to stop a *remote* edit landing under
    // somebody's fingers; an approved proposal, or taking the version from
    // elsewhere, is this person's own decision and masking it behind their own
    // draft would look like the app ignoring them.
    const wrote = op.fields?.[FIELD_TEXT];
    if (typeof wrote === "string" && this.drafts[op.node] !== undefined) {
      this.drafts[op.node] = wrote;
      if (this.#baseline[op.node] !== undefined) this.#baseline[op.node] = wrote;
    }
    // Optimistic first, then sent. ApplyWorkspaceEdits is a round trip and
    // typing cannot wait on one -- the character is on screen in this frame,
    // and the op it becomes is Go's business.
    this.#queue(editOf(op));
    return true;
  }

  /**
   * Puts an edit on the wire, in order, one batch at a time.
   *
   * Serialised because Go stamps an edit with the clock it holds when the edit
   * arrives. Two edits to one line racing each other could therefore land with
   * the later keystroke carrying the earlier clock, and the outcome of that is
   * not an error -- it is the wrong text, permanently. The debounce in
   * #settleLater makes the race rare; rare is not never.
   */
  #queue(edit: Edit): void {
    if (!this.send) return;
    this.#outbound.push(edit);
    void this.#drain();
  }

  async #drain(): Promise<void> {
    if (this.#sending || !this.send) return;
    this.#sending = true;
    try {
      while (this.#outbound.length > 0) {
        const batch = this.#outbound;
        this.#outbound = [];
        const merged = await this.send(batch);
        if (merged) this.adopt(merged);
      }
    } catch {
      // A refusal is not a lost edit: the op reached Go's disk before the call
      // returned, or it never became an op at all. Either way what is on screen
      // is what this machine believes, and reverting somebody's typing because
      // a round trip failed would be the worse answer. The sync badge is where
      // a workspace that cannot be written to is reported.
    } finally {
      this.#sending = false;
    }
  }

  /**
   * Takes the merged document as the truth, keeping what is being typed.
   *
   * The State is replaced rather than merged into, and that is the only way a
   * delete can reach the optimistic layer: a tombstone cannot be replayed as a
   * create, so a node the document omits is a node that is simply not in the
   * rebuilt State. See opsOf.
   */
  adopt(doc: Document | null): void {
    if (!doc) return;
    const rebuilt = new State();
    for (const op of opsOf(doc, this.id)) {
      try {
        rebuilt.apply(op);
      } catch {
        // A seed op the local model will not take is a node it cannot show.
        // Skipping it loses one line on this screen; throwing would lose the
        // whole document.
      }
    }
    this.#state = rebuilt;
    this.#replica = new Replica(actorId(), () => this.#state.clock);
    this.#refresh();

    // A line somebody is in whose text moved underneath. Marked, never taken:
    // the caret's owner decides, and until they do, what they typed is what is
    // on screen.
    for (const id of Object.keys(this.#baseline)) {
      const arrived = textOf(findInTree(this.tree, id) ?? this.#detachedNode(id));
      if (arrived === this.#baseline[id]) continue;
      this.#baseline[id] = arrived;
      this.marks = { ...this.marks, [id]: arrived };
    }
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
   * Called before any structural op, so the document takes the edits in the
   * order the person made them. Without it, typing a line and immediately
   * indenting it applies the move first and the text after -- harmless for
   * those two, and not harmless before a delete, where the text op would
   * carry a higher clock than the tombstone it was meant to precede.
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
    if (this.#baseline[id] !== undefined) {
      // Still being typed in. The draft stays, because a line without one
      // takes the document's value -- and the whole point of the marker is
      // that a document arriving does not get to put its text under somebody's
      // caret. The baseline moves to what was just written, so the next remote
      // change is measured against what this machine believes rather than
      // against whatever was there when the caret arrived.
      this.#baseline[id] = draft;
      this.revision++;
      return;
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
