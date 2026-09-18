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
import {
  blockedBecause,
  inverseOf,
  landed,
  revert,
  type Inverse,
} from "./undo";
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

/**
 * What went in without being asked for, and how to take it back.
 *
 * The rows are in the order they were applied, because Undo runs them in
 * reverse and reverse only means something if the order is the real one.
 */
export interface Applied {
  summary: string;
  /** What went in, in the words the row was described in. */
  rows: { says: string; inverse: Inverse }[];
  /** Rows of the proposal that did not go in at all. */
  skipped: number;
}

/** What happened when Undo was pressed. */
export interface UndoOutcome {
  reversed: number;
  /** What could not be taken back, and why, in the row's own words. */
  kept: { says: string; why: string }[];
  /** Of those, the deletions: the only ones nothing can ever fix. */
  deleted: number;
}

/** What happened when Apply was pressed. Shown once, then cleared. */
export interface Outcome {
  applied: number;
  skipped: number;
  /** Rows that were ticked and still would not go in. */
  failed: string[];
  /**
   * Of the applied rows, how many Undo will not be able to take back.
   *
   * Deletions, in practice: a tombstone is final in the merge whichever order
   * it arrives in. Counted here so the sentence that announces the change can
   * say so up front, rather than leaving somebody to find out by pressing Undo
   * and reading an apology.
   */
  irreversible: number;
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
   * Off by default, and set from the backend by App.
   *
   * The default is the other way round from how it started, and the reason is
   * Undo. This panel was the only approval gate the app had, because a
   * proposal that had landed could not be taken back — a gate in front of
   * something irreversible is worth a click on every single answer. A change
   * that lands and can be put back in one press is not, and the panel is
   * better as the thing somebody turns on when they want to read every row.
   */
  reviewFirst = $state(false);

  /**
   * What went in, and how to take it back out.
   *
   * Null until something has been applied without being asked for. The bar in
   * the console reads this; `undo` walks it backwards.
   */
  applied: Applied | null = $state.raw<Applied | null>(null);

  /**
   * Takes a proposal for a workspace.
   *
   * The workspace is held by id rather than by reference: tabs are rebuilt
   * from Go's view on every status change, and holding the object would pin a
   * Workspace the rest of the app has already replaced.
   */
  take(proposal: Proposal, workspace: Workspace): void {
    // Whatever the last one did, this is not it. Deliberately not cleared by
    // the document changing: Workspace.revision moves on a colleague's
    // keystroke too, so expiring on that would let somebody else's edit three
    // seconds later eat your Undo. The per-inverse staleness check is what
    // handles a document that has moved, and it handles it precisely.
    this.applied = null;
    this.proposal = proposal;
    this.workspaceId = workspace.id;
    this.mode = workspace.mode === "planning" ? "planning" : "idea";
    // Everything ticked. The person is approving a restructuring, not
    // assembling one -- the common answer is "yes, all of it", and the common
    // answer should not be twelve clicks.
    this.#approved = Object.fromEntries(proposal.ops.map((_, at) => [at, true]));
    this.outcome = null;
    this.refused = null;

    if (!this.reviewFirst && !replaces(proposal)) {
      // Through the same path Apply uses, deliberately. Going around it would
      // mean two ways of applying a proposal, and the second one would be the
      // one that drifts.
      const outcome = this.apply(workspace);
      // apply() sets `said` in the words of somebody who pressed a button.
      // Nobody pressed anything, so it is said again in words that explain why
      // the map just moved on its own -- and name the way back.
      this.said =
        `Claude changed ${outcome.applied} ${outcome.applied === 1 ? "line" : "lines"} on the map. ` +
        (outcome.irreversible > 0
          ? `${outcome.irreversible} of them cannot be undone. `
          : "Undo is in the panel on the right. ") +
        (outcome.skipped > 0 ? `${outcome.skipped} could not be applied.` : "");
      // Cleared, because there is nothing to review. The applied bar stays on
      // screen: it is the record that anything happened, and the way back.
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
    // Not `applied`. Discarding is about the proposal on screen; something
    // that already went in is not discardable, it is undoable, and taking the
    // Undo away here would be the one button that loses the other one.
    this.said = "Suggestion discarded. Nothing was changed.";
  }

  /**
   * Takes the record of what was applied off the screen.
   *
   * Not an undo and deliberately not one: the changes stay, and this is
   * somebody saying they have read the bar. It exists because the bar had no
   * way out -- it sat above the composer until the next proposal arrived,
   * which on a quiet afternoon is the rest of the day.
   */
  dismissApplied(): void {
    this.applied = null;
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
    const undoable: { says: string; inverse: Inverse }[] = [];
    let applied = 0;
    let skipped = 0;

    for (const row of rows) {
      if (!row.approved || row.blocked !== null) {
        skipped++;
        continue;
      }
      // Planned before the op runs, because most inverses need the value the
      // op is about to replace -- the text a line said, the region it was in,
      // where among its siblings it sat. None of that survives the write.
      const plan = inverseOf(row.op, workspace, (id) => refs.get(id) ?? id);
      const made = workspace.applyProposed(row.op, refs);
      if (made !== null) {
        applied++;
        undoable.push({ says: row.says, inverse: landed(plan, made, workspace) });
      } else {
        failed.push(row.says);
      }
    }

    this.applied =
      applied > 0
        ? { summary: this.proposal?.summary ?? "", rows: undoable, skipped: skipped + failed.length }
        : null;

    // Every row is unticked afterwards, so a second press cannot put the same
    // insert in twice. Applying is not idempotent -- an insert makes a new
    // node each time -- and the button is the kind people press again when
    // they are not sure it worked.
    this.setAll(false);
    const outcome: Outcome = {
      applied,
      skipped,
      failed,
      irreversible: undoable.filter((row) => row.inverse.kind === "none").length,
    };
    this.outcome = outcome;
    this.said =
      `Applied ${applied} ${applied === 1 ? "change" : "changes"}` +
      (skipped > 0 ? `, left ${skipped} out` : "") +
      (failed.length > 0 ? `. ${failed.length} were refused by the document` : "") +
      ".";
    return outcome;
  }

  /**
   * Puts back what was applied without being asked for.
   *
   * Backwards through the rows, and that is load-bearing rather than tidy: a
   * proposal that inserts a heading and then moves three lines under it has to
   * un-move the three before the heading is tombstoned, or they are left with
   * a dead parent and land in `detached`.
   *
   * Each inverse is checked against the document as it now is before it runs.
   * Somebody who has typed in a line since keeps their words -- putting the old
   * ones back would be undoing their edit as well as Claude's, which is not
   * what the button says.
   */
  undo(workspace: Workspace): UndoOutcome {
    const record = this.applied;
    if (!record) return { reversed: 0, kept: [], deleted: 0 };

    const kept: { says: string; why: string }[] = [];
    let reversed = 0;
    let deleted = 0;

    for (let at = record.rows.length - 1; at >= 0; at--) {
      const row = record.rows[at];
      const why = blockedBecause(row.inverse, workspace);
      if (why !== null) {
        kept.push({ says: row.says, why });
        if (row.inverse.kind === "none") deleted++;
        continue;
      }
      if (revert(row.inverse, workspace)) reversed++;
      else kept.push({ says: row.says, why: "the document refused it" });
    }

    this.applied = null;
    this.said =
      `Put back ${reversed} ${reversed === 1 ? "change" : "changes"}` +
      (deleted > 0
        ? `. ${deleted} deleted ${deleted === 1 ? "line is" : "lines are"} gone for good`
        : "") +
      (kept.length > deleted ? `. ${kept.length - deleted} could not be put back` : "") +
      ".";
    return { reversed, kept, deleted };
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

      case "link":
        if (!known(op.node) || !known(op.other)) return gone;
        if (op.node === op.other) return "a line cannot link to itself";
        return workspace.linksOf(op.node).some((l) => l.other === op.other)
          ? "those two are already linked"
          : null;

      case "unlink":
        return workspace.linksOf(op.node).some((l) => l.other === op.other)
          ? null
          : "those two are not linked";

      case "caption":
        if (!known(op.node) || !known(op.other)) return gone;
        return workspace.linksOf(op.node).some((l) => l.other === op.other)
          ? null
          : "those two are not linked";

      case "set-icon":
      case "set-detail":
        return known(op.node) ? null : gone;

      case "guide":
        if (!known(op.node)) return gone;
        if (!workspace.graph.guidelines.has(op.guideline)) return "that guideline does not exist";
        return workspace.guidelinesOf(op.node).some((g) => g.id === op.guideline)
          ? "that card is already under it"
          : null;

      case "unguide":
        if (!known(op.node)) return gone;
        return workspace.guidelinesOf(op.node).some((g) => g.id === op.guideline)
          ? null
          : "that card is not under it";

      case "interest":
        if (!known(op.node)) return gone;
        if (!workspace.graph.parties.has(op.party)) return "nobody by that name is set up here";
        return workspace.partiesOf(op.node).some((p) => p.id === op.party)
          ? "they are already interested"
          : null;

      case "uninterest":
        if (!known(op.node)) return gone;
        return workspace.partiesOf(op.node).some((p) => p.id === op.party)
          ? null
          : "they are not down as interested";

      case "replace":
        // It names nothing: a replace acts on whatever is there -- including
        // nothing, which is a board that was already empty and an archive line
        // with no children under it.
        return null;

      case "group":
        if (!known(op.node)) return gone;
        // A region it was not shown is a region it invented, and an invented
        // one is refused rather than made under a name nobody chose.
        if (op.region && !workspace.graph.regions.has(op.region)) {
          return "that region does not exist";
        }
        return null;

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

    case "link":
      return `Link ${name(op.node)} and ${name(op.other)}`;

    case "unlink":
      return `Unlink ${name(op.node)} from ${name(op.other)}`;

    case "group":
      return op.region
        ? `Put ${name(op.node)} in the region “${clip(workspace.graph.regions.get(op.region) ?? "", 40)}”`
        : `Group ${name(op.node)} and everything under it into a new region “${clip(op.name ?? "", 40)}”`;

    case "set-status":
      return `Mark ${name(op.node)} as ${TASK_STATE_LABELS[op.status].toLowerCase()}`;

    case "set-detail":
      return op.text.trim() === ""
        ? `Clear what ${name(op.node)} says at length`
        : `Write ${op.text.trim().length} characters of detail on ${name(op.node)}`;

    case "guide":
      return `Put ${name(op.node)} under “${term(workspace.graph.guidelines, op.guideline)}”`;

    case "unguide":
      return `Take ${name(op.node)} out from under “${term(workspace.graph.guidelines, op.guideline)}”`;

    case "interest":
      return `Note that ${term(workspace.graph.parties, op.party)} is waiting on ${name(op.node)}`;

    case "uninterest":
      return `Note that ${term(workspace.graph.parties, op.party)} is no longer waiting on ${name(op.node)}`;

    case "set-icon":
      return op.icon === ""
        ? `Take the glyph off ${name(op.node)}`
        : `Put the ${op.icon} glyph on ${name(op.node)}`;

    case "caption":
      return op.text.trim() === ""
        ? `Take the caption off the link between ${name(op.node)} and ${name(op.other)}`
        : `Say “${clip(op.text, 40)}” on the link between ${name(op.node)} and ${name(op.other)}`;

    case "replace": {
      // The count is the warning, the way it is on a delete -- except that
      // this one is reversible, and saying so is the difference between a row
      // somebody reads and a row somebody panics at.
      const roots = workspace.rows.filter((r) => r.depth === 0).length;
      return roots === 0
        ? `Start a new board — ${clip(op.reason, 80)}`
        : `Set the whole board aside — ${roots} top-level ${roots === 1 ? "line" : "lines"} ` +
          `move under one collapsed line, and Undo brings them back. ${clip(op.reason, 80)}`;
    }
  }
}

/**
 * True if a proposal starts the board again.
 *
 * Such a proposal always goes through review, whatever `reviewFirst` says. The
 * apply-then-offer-Undo default is right for "add three lines"; it is not right
 * for "here is a different board". This is the one change big enough that
 * seeing it first is worth the click -- and it is exactly the one where the
 * Undo behind it is a set of moves rather than one edit, so having read the row
 * is what makes the way back obvious.
 */
function replaces(proposal: Proposal): boolean {
  return proposal.ops.some((op) => op.kind === "replace");
}

/** A word out of one of the vocabularies, or a placeholder if it has gone. */
function term(words: Map<string, string>, id: string): string {
  const name = words.get(id);
  return name === undefined || name.trim() === "" ? "a word that is gone" : name.trim();
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
