/**
 * Taking back what Claude just did.
 *
 * A proposal used to wait in a panel with a tick per row, because it could not
 * be taken back once applied and a gate in front of something irreversible is
 * worth a click. It can be taken back now, and this is how — so the panel
 * becomes the thing you turn on when you want it rather than the thing you get
 * through every time.
 *
 * Undo is a compensating edit, not an un-send. Every inverse below goes through
 * the same methods a keystroke does: it becomes an op, reaches disk before it
 * is shown, merges, and goes to the server like anything else. What it restores
 * is the document's meaning, not its op log — and three of these do it by
 * tombstoning something the proposal itself created, which is the same thing
 * from the reader's side and a different thing in the log.
 *
 * One of the nine has no inverse. See `none`, and Review.undo, which says so
 * out loud rather than quietly restoring eight and calling it done.
 */
import { findInTree, statusOf, textOf, type TaskState } from "./model";
import type { ProposedOp } from "./proposal";
import type { Workspace } from "./workspace.svelte";

/**
 * How to put one applied op back.
 *
 * A tagged union rather than a closure per row. A closure is a thing you can
 * only find out about by running it; these have to be describable — counted,
 * checked against a document that has moved, and reported on when they cannot
 * run — before anybody presses anything.
 */
export type Inverse =
  /** Put the text back, if the line still says what the proposal wrote. */
  | { kind: "set-text"; node: string; text: string; expect: string }
  /** Tombstone a node the proposal created. */
  | { kind: "delete"; node: string }
  /** Put a line back where it was, under the sibling it followed. */
  | { kind: "move"; node: string; parent: string; after: string | null }
  /** Take a task off the plan, leaving the line it came from alone. */
  | { kind: "demote"; task: string }
  | { kind: "set-status"; node: string; status: TaskState; expect: TaskState }
  /** Make the link again. A new edge node, the same relationship. */
  | { kind: "link"; node: string; other: string }
  /** Remove a link the proposal made. */
  | { kind: "unlink"; node: string; other: string }
  /** Undo a grouping: drop the region the proposal made, and its members. */
  | { kind: "ungroup"; region: string; members: string[] }
  /** Put a line back in the region it was in before. */
  | { kind: "regroup"; node: string; region: string | null }
  /** Nothing can be done, and `why` is what to tell somebody. */
  | { kind: "none"; why: string };

/** Why a deletion is the one that cannot be taken back. */
export const DELETION_IS_FOREVER =
  "a deleted line cannot be brought back — the merge treats a deletion as final, in either order";

/**
 * What would put this op back, read *before* it is applied.
 *
 * Before, because most of these need the value the op is about to replace: the
 * text a line said, the region it was in, where among its siblings it sat. None
 * of that is recoverable afterwards.
 *
 * `real` resolves a proposal-local ref the same way applyProposed does, so an
 * op naming a node an earlier row created is planned against the id it actually
 * got.
 */
export function inverseOf(
  op: ProposedOp,
  workspace: Workspace,
  real: (id: string) => string,
): Inverse {
  switch (op.kind) {
    case "set-text": {
      const node = real(op.node);
      return { kind: "set-text", node, text: workspace.text(node), expect: op.text };
    }

    case "insert":
      // Planned against an id that does not exist yet. Filled in by `landed`
      // once the insert has run and the document has told us what it is.
      return { kind: "delete", node: "" };

    case "move": {
      const node = real(op.node);
      const row = workspace.rows.find((r) => r.node.id === node);
      if (!row) return { kind: "none", why: "that line is not in the outline" };
      const siblings = workspace.rows.filter((r) => r.parentId === row.parentId);
      const at = siblings.findIndex((r) => r.node.id === node);
      return {
        kind: "move",
        node,
        parent: row.parentId,
        // The sibling it followed, or null for "first among them", which is
        // the same vocabulary a proposal uses.
        after: at > 0 ? siblings[at - 1].node.id : null,
      };
    }

    case "delete":
      return { kind: "none", why: DELETION_IS_FOREVER };

    case "promote":
      // A line already on the plan is a row that changes nothing, so there is
      // nothing to take back either. Otherwise the task id arrives via
      // `landed`.
      return workspace.taskFor(real(op.node))
        ? { kind: "none", why: "that line was already on the plan" }
        : { kind: "demote", task: "" };

    case "set-status": {
      const node = real(op.node);
      const task = findInTree(workspace.tree, node);
      if (!task) return { kind: "none", why: "that task is not on the plan" };
      return { kind: "set-status", node, status: statusOf(task), expect: op.status };
    }

    case "link":
      return { kind: "unlink", node: real(op.node), other: real(op.other) };

    case "unlink":
      // A new edge node with the same two ends. An edge is a node and a
      // tombstone is permanent, so the relationship comes back and the node it
      // was carried on does not — which is a distinction only the log can see.
      return { kind: "link", node: real(op.node), other: real(op.other) };

    case "group": {
      const node = real(op.node);
      if (op.region) {
        // Into a region that already existed, so the inverse is whichever one
        // it was in before — including none.
        return { kind: "regroup", node, region: workspace.regionOf(node)?.id ?? null };
      }
      // A new region, whose id and membership arrive via `landed`.
      return { kind: "ungroup", region: "", members: [] };
    }
  }
}

/**
 * Fills in what only the document could say, once the op has run.
 *
 * Four of the nine mint a node, and their inverse cannot name it until it
 * exists. `applyProposed` answers with the id it created for exactly this.
 */
export function landed(inverse: Inverse, id: string, workspace: Workspace): Inverse {
  switch (inverse.kind) {
    case "delete":
      return inverse.node === "" ? { kind: "delete", node: id } : inverse;
    case "demote":
      return inverse.task === "" ? { kind: "demote", task: id } : inverse;
    case "ungroup":
      if (inverse.region !== "") return inverse;
      // Every line the grouping stamped, read now: `group` puts a whole branch
      // in a region, and undoing it has to take all of them back out rather
      // than only the one the proposal named.
      return { kind: "ungroup", region: id, members: workspace.regionMembers(id) };
    default:
      return inverse;
  }
}

/** Why this inverse cannot run against the document as it now is, or null. */
export function blockedBecause(inverse: Inverse, workspace: Workspace): string | null {
  const gone = "that line is no longer in the outline";
  const here = (id: string) => findInTree(workspace.tree, id) !== null;

  switch (inverse.kind) {
    case "none":
      return inverse.why;

    case "set-text": {
      if (!here(inverse.node)) return gone;
      const node = findInTree(workspace.tree, inverse.node);
      // Somebody has typed in it since. Putting the old words back would be
      // undoing their edit as well as Claude's, which is not what the button
      // says.
      return node && textOf(node) === inverse.expect ? null : "you have changed that line since";
    }

    case "set-status": {
      const task = findInTree(workspace.tree, inverse.node);
      if (!task) return "that task is no longer on the plan";
      return statusOf(task) === inverse.expect ? null : "that task has moved on since";
    }

    case "delete":
    case "move":
    case "regroup":
      return here(inverse.node ?? "") ? null : gone;

    case "demote":
      return here(inverse.task) ? null : "that task is no longer on the plan";

    case "link":
    case "unlink":
      return here(inverse.node) && here(inverse.other) ? null : gone;

    case "ungroup":
      return here(inverse.region) ? null : "that region is already gone";
  }
}

/**
 * Runs one inverse. Returns false if the document refused it.
 *
 * Everything that can go through `applyProposed` does, so Undo inherits its
 * guards — the cycle refusal, the sibling check — rather than reimplementing
 * them a second time and slightly differently.
 */
export function revert(inverse: Inverse, workspace: Workspace): boolean {
  const none = new Map<string, string>();

  switch (inverse.kind) {
    case "none":
      return false;

    case "set-text":
      return workspace.applyProposed(
        { kind: "set-text", node: inverse.node, text: inverse.text },
        none,
      ) !== null;

    case "delete":
      return workspace.applyProposed({ kind: "delete", node: inverse.node }, none) !== null;

    case "move":
      return workspace.applyProposed(
        { kind: "move", node: inverse.node, parent: inverse.parent, after: inverse.after },
        none,
      ) !== null;

    case "demote":
      return workspace.removeTask(inverse.task);

    case "set-status":
      return workspace.applyProposed(
        { kind: "set-status", node: inverse.node, status: inverse.status },
        none,
      ) !== null;

    case "link":
      return workspace.applyProposed(
        { kind: "link", node: inverse.node, other: inverse.other },
        none,
      ) !== null;

    case "unlink":
      return workspace.applyProposed(
        { kind: "unlink", node: inverse.node, other: inverse.other },
        none,
      ) !== null;

    case "regroup":
      // Back where it was, or out of any region at all.
      return inverse.region
        ? workspace.applyProposed(
            { kind: "group", node: inverse.node, region: inverse.region },
            none,
          ) !== null
        : workspace.ungroup(inverse.node);

    case "ungroup": {
      // The members first, then the region node itself. A region whose members
      // still point at a tombstone is a region that is gone from the map and
      // still written on every line that was in it.
      let ok = true;
      for (const member of inverse.members) {
        if (!workspace.ungroup(member)) ok = false;
      }
      return workspace.remove(inverse.region) && ok;
    }
  }
}
