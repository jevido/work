/**
 * A restructuring proposal, waiting to be approved.
 *
 * **Nothing here ever applies itself.** A proposal arrives, it is turned into
 * a list of rows, and it sits there until somebody presses Apply. That is the
 * default and there is no setting that changes it, which is a deliberate
 * position rather than an unfinished one: these ops delete lines and move
 * branches, an outline has no undo, and `delete-node` is a tombstone the
 * protocol has no way to lift. A restructuring that went in by itself would be
 * unrecoverable by the time anybody read it.
 *
 * What this file is actually for is the part between arriving and applying:
 *
 *   - saying what each op does, in the words of the document rather than the
 *     words of the protocol -- "Move *Retry the first connect* under *Networking*",
 *     not `move-node n_91ab -> n_7f3c`;
 *   - noticing the ops that no longer make sense, because the document moved
 *     on while the proposal was being written. A line Claude wanted to rename
 *     may have been deleted thirty seconds ago. That row is shown, struck
 *     through, with why -- rather than silently dropped, because "it applied
 *     and nothing happened" is the worst outcome available here;
 *   - keeping the ops that depend on each other honest. An insert can name
 *     itself with a `ref` so later ops can put things under it; unticking that
 *     insert has to block them, not leave them pointing at nothing.
 *
 * Reads the workspace, and writes to it only through Workspace.applyProposed.
 */
import { Events } from "@wailsio/runtime";
import { CLAUDE_TOOL, PROPOSE_TOOL } from "../bridge/events";
import { findInTree, labelOf, TASK_STATE_LABELS, type Mode } from "./model";
import { readProposal, type Proposal, type ProposedOp } from "./proposal";
import type { Workspace } from "./workspace.svelte";

/** Why a row cannot be applied, or null when it can. */
export type Blocker = string | null;

export interface Row {
  /** Index in the proposal, which is also the order they apply in. */
  at: number;
  op: ProposedOp;
  /** What this does, in English, naming lines by their text. */
  says: string;
  /** Ticked rows are the ones Apply applies. Everything starts ticked. */
  approved: boolean;
  /** Set when the document has moved on under this op. */
  blocked: Blocker;
}

/** What happened when Apply was pressed. Shown once, then cleared. */
export interface Outcome {
  applied: number;
  skipped: number;
  /** Rows that were ticked and still would not go in. */
  failed: string[];
}

export class Review {
  /**
   * The proposal on screen, or null.
   *
   * One at a time. A second arriving while one is open replaces it, and says
   * so -- a stack of proposals is a queue nobody works through, and the older
   * one was written against an older document anyway.
   */
  proposal = $state.raw<Proposal | null>(null);

  /** Which workspace it was proposed for. A proposal is not portable. */
  workspaceId = $state<string | null>(null);

  /**
   * Which mode the panel will appear in.
   *
   * A restructuring is about the outline and the plan, so it is reviewed in
   * one of those two -- never over the office. Someone watching a run when a
   * proposal arrives keeps watching it; the proposal waits, and is said to be
   * waiting rather than silently parked.
   */
  mode = $state<Mode>("idea");

  /** Ticked state per row, by index. */
  #approved = $state<Record<number, boolean>>({});

  /** The result of the last Apply, until something else happens. */
  outcome = $state.raw<Outcome | null>(null);

  /** A proposal that would not parse. Said out loud rather than swallowed. */
  refused = $state<string | null>(null);

  /**
   * The last thing worth announcing, for a live region that outlives it.
   *
   * A proposal arrives without anybody asking for it, and the panel it arrives
   * in is a region somewhere off to the side that a screen reader has no
   * reason to visit. Without this, the entire feature is invisible: no focus
   * moves, no dialog opens, nothing is said, and the first anybody knows about
   * it is the next time they happen to tab past it.
   *
   * Read by a region that is mounted for the life of the window -- see
   * App.svelte. A live region created at the same moment as its first message
   * is a region nobody hears, which is the same reason IdeaOutline keeps its
   * own outside the branch that fills it.
   */
  said = $state("");

  /** Whether there is anything to review. */
  open = $derived(this.proposal !== null);

  /**
   * Whether a proposal applies itself instead of waiting.
   *
   * Off by default, and set from the backend by App. The default is the point:
   * this panel is the only approval gate the app has. When Claude edits files
   * it writes to disk and the app only gets to look afterwards; here the app
   * owns the write, so it can ask first.
   *
   * The opt-out is for somebody who has watched it be right forty times and
   * would rather the forty-first just happened.
   */
  withoutReview = $state(false);

  /**
   * Takes a proposal for a workspace.
   *
   * The workspace is held by id rather than by reference: tabs are rebuilt
   * from Go's view on every status change, and holding the object would pin a
   * Workspace the rest of the app has already replaced.
   */
  take(proposal: Proposal, workspace: Workspace): void {
    this.proposal = proposal;
    this.workspaceId = workspace.id;
    this.mode = workspace.mode === "planning" ? "planning" : "idea";
    // Everything ticked. The person is approving a restructuring, not
    // assembling one -- the common answer is "yes, all of it", and the common
    // answer should not be twelve clicks.
    this.#approved = Object.fromEntries(proposal.ops.map((_, at) => [at, true]));
    this.outcome = null;
    this.refused = null;

    if (this.withoutReview) {
      // Through the same path Apply uses, deliberately. Going around it would
      // mean two ways of applying a proposal, and the second one would be the
      // one that drifts.
      const outcome = this.apply(workspace);
      // apply() sets `said` in the words of somebody who pressed a button.
      // Nobody pressed anything, so it is said again in words that explain why
      // the outline just moved on its own.
      this.said =
        `Claude changed ${outcome.applied} ${outcome.applied === 1 ? "line" : "lines"} in the outline, ` +
        `applied without asking because that setting is on` +
        (outcome.skipped > 0 ? `. ${outcome.skipped} could not be applied` : "") +
        ".";
      // Cleared, because there is nothing to review. The outcome stays on
      // screen: it is the only record that anything happened.
      this.proposal = null;
      return;
    }

    const n = proposal.ops.length;
    const what = `Claude suggested ${n} ${n === 1 ? "change" : "changes"} to the outline. Nothing has been applied.`;
    // Where to go for it, which is not the same sentence when the office is
    // on screen: saying "review them in Suggested changes" to somebody
    // looking at a panel that is not there is worse than saying nothing.
    this.said =
      workspace.mode === "work"
        ? `${what} They are waiting in ${this.mode === "planning" ? "Planning" : "Idea"} mode.`
        : `${what} Review them in Suggested changes.`;
  }

  /** A proposal that could not be read. The console is not the place for it. */
  refuse(why: string): void {
    this.refused = why;
    this.said = "Claude suggested a restructuring that could not be read. Nothing was changed.";
  }

  /**
   * Listens for Claude proposing a restructuring.
   *
   * A proposal is a tool call like any other -- see PROPOSE_TOOL -- so this
   * watches the stream the console already watches rather than asking the
   * backend for a channel of its own.
   *
   * @param target Which workspace the proposal is for, read at the moment one
   *   arrives rather than captured now: tab instances are rebuilt from Go's
   *   view whenever sync status moves, so a reference taken at subscribe time
   *   would be pointing at a Workspace the app has since replaced.
   * @returns The unsubscribe, for the effect that called this.
   */
  listen(target: () => Workspace | null): () => void {
    return Events.On(CLAUDE_TOOL, (e) => {
      const data = e.data as { toolName?: string; toolInput?: unknown };
      if (data?.toolName !== PROPOSE_TOOL) return;

      const workspace = target();
      // A proposal with no workspace to apply it to is dropped. There is no
      // tab to open it in, and holding it until one exists would apply it to
      // whichever tab happened to appear next.
      if (!workspace) return;

      try {
        this.take(readProposal(data.toolInput), workspace);
      } catch (err) {
        this.refuse(err instanceof Error ? err.message : String(err));
      }
    });
  }

  discard(): void {
    this.proposal = null;
    this.workspaceId = null;
    this.#approved = {};
    this.outcome = null;
    this.refused = null;
    this.said = "Suggestion discarded. Nothing was changed.";
  }

  isApproved(at: number): boolean {
    return this.#approved[at] === true;
  }

  setApproved(at: number, approved: boolean): void {
    this.#approved = { ...this.#approved, [at]: approved };
  }

  setAll(approved: boolean): void {
    const ops = this.proposal?.ops ?? [];
    this.#approved = Object.fromEntries(ops.map((_, at) => [at, approved]));
  }

  /**
   * The rows, as they read against the document right now.
   *
   * Recomputed from the workspace rather than cached, so a proposal open while
   * somebody edits the outline underneath it updates: a line that gets deleted
   * strikes its row through the moment it goes, rather than at the moment
   * Apply discovers it.
   */
  rows(workspace: Workspace): Row[] {
    const proposal = this.proposal;
    if (!proposal) return [];

    // Refs an approved insert will have created by the time each later op
    // runs. Unapproved inserts contribute nothing, which is what blocks the
    // ops that were counting on them.
    const willExist = new Set<string>();

    return proposal.ops.map((op, at) => {
      const approved = this.isApproved(at);
      const blocked = this.#blockerFor(op, workspace, willExist);
      if (op.kind === "insert" && op.ref && approved && blocked === null) {
        willExist.add(op.ref);
      }
      return { at, op, says: describe(op, workspace, proposal), approved, blocked };
    });
  }

  /** How many rows Apply would actually write. */
  applicable(workspace: Workspace): number {
    return this.rows(workspace).filter((r) => r.approved && r.blocked === null).length;
  }

  /**
   * Applies the ticked rows, in order, and keeps the proposal on screen.
   *
   * Kept on screen on purpose. Applying is not the end of reviewing: the rows
   * that were struck through are still worth reading, and a proposal that
   * vanished at the moment it did something gives nobody a chance to see what
   * it did. Discard is the button that closes it.
   */
  apply(workspace: Workspace): Outcome {
    const rows = this.rows(workspace);
    /** Proposal-local ref to the id the document actually gave the node. */
    const refs = new Map<string, string>();
    const failed: string[] = [];
    let applied = 0;
    let skipped = 0;

    for (const row of rows) {
      if (!row.approved || row.blocked !== null) {
        skipped++;
        continue;
      }
      const made = workspace.applyProposed(row.op, refs);
      if (made) applied++;
      else failed.push(row.says);
    }

    // Every row is unticked afterwards, so a second press cannot put the same
    // insert in twice. Applying is not idempotent -- an insert makes a new
    // node each time -- and the button is the kind people press again when
    // they are not sure it worked.
    this.setAll(false);
    const outcome: Outcome = { applied, skipped, failed };
    this.outcome = outcome;
    this.said =
      `Applied ${applied} ${applied === 1 ? "change" : "changes"}` +
      (skipped > 0 ? `, left ${skipped} out` : "") +
      (failed.length > 0 ? `. ${failed.length} were refused by the document` : "") +
      ".";
    return outcome;
  }

  /* ---------------------------------------------------------------------- */

  /**
   * Why this op cannot run, or null.
   *
   * Only the reasons that are visible in the document. Whether the merge will
   * take the op is the merge's business and it answers by refusing, which
   * `apply` reports as a failure -- there is no point guessing at it twice.
   */
  #blockerFor(op: ProposedOp, workspace: Workspace, willExist: Set<string>): Blocker {
    const here = (id: string) => findInTree(workspace.tree, id) !== null;
    const known = (id: string) => here(id) || willExist.has(id);

    switch (op.kind) {
      case "set-text":
      case "delete":
      case "promote":
        return known(op.node) ? null : gone;

      case "set-status":
        return workspace.tasks.some((t) => t.id === op.node)
          ? null
          : "that task is not on the plan any more";

      case "insert": {
        if (op.parent !== "" && !known(op.parent)) return "the line it goes under is gone";
        if (op.after !== null && !known(op.after)) return "the line it goes after is gone";
        return null;
      }

      case "move": {
        if (!here(op.node)) return gone;
        if (op.parent !== "" && !known(op.parent)) return "the line it moves under is gone";
        if (op.after !== null && !known(op.after)) return "the line it moves after is gone";
        // A node cannot go inside itself. The merge answers a cycle by
        // detaching the branch, which on screen is a line disappearing -- so
        // it is refused here, where there is somewhere to say why.
        if (op.parent === op.node || isUnder(workspace, op.parent, op.node)) {
          return "that would put the line inside itself";
        }
        return null;
      }
    }
  }
}

const gone = "that line has since been deleted";

/** True when `id` sits anywhere under `rootId`. */
function isUnder(workspace: Workspace, id: string, rootId: string): boolean {
  const root = findInTree(workspace.tree, rootId);
  if (!root) return false;
  return findInTree(root.children, id) !== null;
}

/* -------------------------------------------------------------------------- */
/* Saying what an op does                                                     */
/* -------------------------------------------------------------------------- */

/**
 * One op, in English.
 *
 * The whole review rests on this being readable. A row that says
 * `move-node 7f3c -> 91ab` is a row nobody can approve, so every id is
 * resolved to the text of the line it names -- and a line inside the proposal
 * that does not exist yet is named by what the proposal is about to call it,
 * which is the only name it has.
 *
 * Quotes are curly on purpose: an outline line can contain a straight quote,
 * and a sentence that mixes the two is a sentence where you cannot see where
 * the line begins.
 */
export function describe(op: ProposedOp, workspace: Workspace, proposal: Proposal): string {
  const name = (id: string): string => {
    const node = findInTree(workspace.tree, id);
    if (node) return `“${labelOf(node, 48)}”`;
    const pending = proposal.ops.find((o) => o.kind === "insert" && o.ref === id);
    if (pending && pending.kind === "insert") return `the new line “${clip(pending.text, 48)}”`;
    return "a line that is gone";
  };

  const where = (parent: string, after: string | null): string => {
    const under = parent === "" ? "at the top level" : `under ${name(parent)}`;
    return after === null ? `${under}, first` : `${under}, after ${name(after)}`;
  };

  switch (op.kind) {
    case "set-text": {
      const was = workspace.text(op.node).trim();
      // A rename reads as a rename only when both halves are there. Without
      // the old text it is "set this to that", which says nothing about what
      // is changing.
      return was === ""
        ? `Write “${clip(op.text, 48)}” into an empty line`
        : `Reword ${name(op.node)} to “${clip(op.text, 48)}”`;
    }

    case "insert":
      return `Add “${clip(op.text, 48)}” ${where(op.parent, op.after)}`;

    case "move":
      return `Move ${name(op.node)} ${where(op.parent, op.after)}`;

    case "delete": {
      const node = findInTree(workspace.tree, op.node);
      const under = node ? countDeep(node) : 0;
      // The count is the whole warning. Deleting a line is small; deleting a
      // line with forty under it is not, and the op looks identical.
      return under === 0
        ? `Delete ${name(op.node)}`
        : `Delete ${name(op.node)} and the ${under} ${under === 1 ? "line" : "lines"} under it`;
    }

    case "promote":
      return `Add ${name(op.node)} to the plan as a task`;

    case "set-status":
      return `Mark ${name(op.node)} as ${TASK_STATE_LABELS[op.status].toLowerCase()}`;
  }
}

function countDeep(node: { children: { children: unknown[] }[] }): number {
  let n = 0;
  for (const child of node.children) {
    n += 1 + countDeep(child as never);
  }
  return n;
}

function clip(value: string, limit: number): string {
  const text = value.trim();
  if (text === "") return "an empty line";
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}
