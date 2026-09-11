/**
 * What a Work document means, on top of the op merge that carries it.
 *
 * ./ops.ts knows about nodes, fields and stamps and nothing about ideas or
 * tasks. This file is the other half: the field names the app agrees to use,
 * and the two views it reads out of one tree. Both are conventions rather than
 * anything the protocol enforces -- server/README.md is deliberately silent on
 * what a field means -- so they are written down here once and read from here
 * by the desktop app and by the web viewer alike.
 *
 * The conventions:
 *
 *   type       "task" for a line on the plan, absent or "idea" for a line of
 *              the outline. One tree holds both; which view a node belongs to
 *              is this field and nothing else.
 *   text       The line itself.
 *   collapsed  Whether an outline node's children are folded away. Part of the
 *              document rather than a per-viewer preference: a folded branch is
 *              how somebody says "this part is settled", and a viewer that
 *              opened everything would throw that away.
 *   status     "todo", "doing" or "done", on task nodes.
 *
 * plus `taskId` and `extractedFrom`, which the protocol itself reserves for
 * the two ends of an extraction.
 */
import { FIELD_EXTRACTED_FROM, FIELD_TASK_ID, type Node, type TreeNode } from "./ops";

export const FIELD_TYPE = "type";
export const FIELD_TEXT = "text";
export const FIELD_COLLAPSED = "collapsed";
export const FIELD_STATUS = "status";

export { FIELD_EXTRACTED_FROM, FIELD_TASK_ID };

/** Which of the three faces of a workspace is on screen. */
export type Mode = "idea" | "planning" | "work";

/** In the order the toggle offers them, which is the order work happens in. */
export const MODES: readonly Mode[] = ["idea", "planning", "work"];

export const MODE_LABELS: Record<Mode, string> = {
  idea: "Idea",
  planning: "Planning",
  work: "Work",
};

/** What each mode is for, for the toggle's description line. */
export const MODE_HINTS: Record<Mode, string> = {
  idea: "Think out loud as an outline",
  planning: "Order the outline into tasks",
  work: "Watch the team do them",
};

export type TaskState = "todo" | "doing" | "done";

export const TASK_STATES: readonly TaskState[] = ["todo", "doing", "done"];

export const TASK_STATE_LABELS: Record<TaskState, string> = {
  todo: "To do",
  doing: "In progress",
  done: "Done",
};

/* -------------------------------------------------------------------------- */
/* Reading a node                                                             */
/* -------------------------------------------------------------------------- */

export function textOf(node: Node | null | undefined): string {
  const value = node?.fields[FIELD_TEXT];
  return typeof value === "string" ? value : "";
}

export function isTask(node: Node): boolean {
  return node.fields[FIELD_TYPE] === "task";
}

export function isCollapsed(node: Node): boolean {
  return node.fields[FIELD_COLLAPSED] === true;
}

export function statusOf(node: Node): TaskState {
  const value = node.fields[FIELD_STATUS];
  return value === "doing" || value === "done" ? value : "todo";
}

/** The task extracted from this idea, if one was. */
export function taskIdOf(node: Node): string | null {
  const value = node.fields[FIELD_TASK_ID];
  return typeof value === "string" && value !== "" ? value : null;
}

/** The idea a task came out of, if it came out of one. */
export function sourceIdOf(node: Node): string | null {
  const value = node.fields[FIELD_EXTRACTED_FROM];
  return typeof value === "string" && value !== "" ? value : null;
}

/**
 * What to call a node when something else has to name it.
 *
 * A blank line is a real state in an outliner -- you get one the moment you
 * press Enter -- so everything that labels a node needs an answer that is not
 * the empty string. These end up in button names and in a task's "from" line,
 * so they are trimmed and clipped.
 */
export function labelOf(node: Node | null | undefined, limit = 60): string {
  const text = textOf(node).trim();
  if (text === "") return "Untitled line";
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}

/* -------------------------------------------------------------------------- */
/* The two views                                                              */
/* -------------------------------------------------------------------------- */

/** A line of the outline, as it is drawn and as the keyboard walks it. */
export interface Row {
  node: TreeNode;
  depth: number;
  /** Empty at the top level, matching the protocol's own "" for a root. */
  parentId: string;
  /** Position among its siblings, so "move up" knows whether it can. */
  index: number;
  siblings: number;
  /** Nodes underneath, at any depth. What a fold is hiding. */
  descendants: number;
}

/**
 * The outline, flattened to what is on screen.
 *
 * Children of a folded node are skipped, which makes this the list the
 * keyboard moves through as well as the list that renders: up and down have to
 * land where the eye does, and a hidden line is not somewhere the caret may
 * go.
 *
 * Task nodes are not in it. They live in the same tree -- one log, one merge --
 * and they are the plan, not the outline.
 */
export function outlineRows(tree: readonly TreeNode[]): Row[] {
  const rows: Row[] = [];
  walk(tree.filter((n) => !isTask(n)), "", 0);
  return rows;

  function walk(nodes: readonly TreeNode[], parentId: string, depth: number) {
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i];
      rows.push({
        node,
        depth,
        parentId,
        index: i,
        siblings: nodes.length,
        descendants: countDescendants(node),
      });
      if (!isCollapsed(node) && node.children.length > 0) {
        walk(node.children, node.id, depth + 1);
      }
    }
  }
}

/**
 * The plan, in order.
 *
 * Tasks sit at the top level of the same tree, so the merge has already put
 * them in position order and broken ties by id -- there is nothing to sort
 * here, only something to pick out.
 */
export function planTasks(tree: readonly TreeNode[]): TreeNode[] {
  return tree.filter(isTask);
}

export function countDescendants(node: TreeNode): number {
  let n = 0;
  for (const child of node.children) n += 1 + countDescendants(child);
  return n;
}

/** Finds a node anywhere in the tree. */
export function findInTree(tree: readonly TreeNode[], id: string): TreeNode | null {
  for (const node of tree) {
    if (node.id === id) return node;
    const hit = findInTree(node.children, id);
    if (hit) return hit;
  }
  return null;
}

/* -------------------------------------------------------------------------- */

/**
 * An identifier, for a node, a task, an op or a replica.
 *
 * Random rather than handed out by the server: an edit lands locally before
 * anything is online, so there is nobody to ask. 128 bits of randomness, so
 * two replicas inventing an id at the same moment while both offline is not a
 * thing that needs handling.
 *
 * crypto.randomUUID would do the same job and is only defined in a secure
 * context. The desktop app is a webview on a custom scheme, which is not
 * reliably one.
 */
export function newId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) out += b.toString(16).padStart(2, "0");
  return out;
}
