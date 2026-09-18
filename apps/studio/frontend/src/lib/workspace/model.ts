/**
 * What a Work document means, on top of the op merge that carries it.
 *
 * ./ops.ts knows about nodes, fields and stamps and nothing about ideas or
 * tasks. This file is the other half: the field names the app agrees to use,
 * and the two views it reads out of one tree. Both are conventions rather than
 * anything the protocol enforces -- services/sync/README.md is deliberately silent on
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

/**
 * The kinds of node one document holds.
 *
 * All of them are nodes rather than new op kinds: create-node and delete-node
 * already merge, queue offline and report a conflict, and a node with a type a
 * client does not recognise is a node it does not draw. See TypeEdge in Go.
 *
 * The last four are the board's two vocabularies and the two joins that attach
 * them to cards. A join is a node for the same reason an edge is: a card can
 * carry several guidelines and several interested parties, and a *set* cannot
 * live in a field. A field is one last-write-wins slot, so two people adding
 * two different guidelines to one card at the same moment would keep one and
 * lose the other with nothing to show for it. A node each, and they do not
 * collide at all.
 *
 * Guidelines and interested parties are kept as two vocabularies rather than
 * one with a flavour field. They answer different questions -- "is this worth
 * doing" and "who is waiting for it" -- and a single list would make the
 * settings panel a list of two kinds of thing where each is short.
 */
export const TYPE_IDEA = "idea";
export const TYPE_TASK = "task";
export const TYPE_EDGE = "edge";
export const TYPE_REGION = "region";

/**
 * A thing this workspace is trying to be: "improves performance".
 *
 * Per workspace, because what counts as progress is not the same on two
 * projects, and a built-in list would be a list somebody has to work around on
 * the first project it does not fit.
 */
export const TYPE_GUIDELINE = "guideline";
/** A card meets a guideline. `from` is the card, `to` is the guideline. */
export const TYPE_GUIDED = "guided";

/**
 * Somebody who wants a card: a person, a team, a customer.
 *
 * Content, not attribution. This is a line of text somebody wrote on a card,
 * exactly like every other line, and it records nothing about who wrote it --
 * the op envelope carries a replica, not an author, and it gains no field for
 * one here. A workspace where nobody can be named can still say out loud that
 * Sales is waiting on something.
 */
export const TYPE_PARTY = "party";
/** A party wants a card. `from` is the card, `to` is the party. */
export const TYPE_INTEREST = "interest";

/** The two ends of an edge, and the region a node is in. */
export const FIELD_FROM = "from";
export const FIELD_TO = "to";
export const FIELD_REGION = "region";
export const FIELD_TEXT = "text";
export const FIELD_COLLAPSED = "collapsed";
export const FIELD_STATUS = "status";

/**
 * The glyph on a cluster head, by name -- see lib/mindmap/icons.ts.
 *
 * A name, not a character. The names are a closed set this build knows how to
 * draw, so a value from a newer release is a glyph this one leaves off rather
 * than a box with a question mark in it, and the line itself is untouched
 * either way. Last-write-wins is the right merge: it is one person's choice
 * about one line, not a set two people add to.
 */
export const FIELD_ICON = "icon";

/**
 * What a card says when there is room to say it.
 *
 * The board draws `text` and nothing else -- a note is read at a glance, and a
 * paragraph in a box is a paragraph nobody reads. This is the rest of it, shown
 * when a card is opened. Newlines survive here, which they deliberately do not
 * in `text`: a line is a line, and this is where a paragraph goes.
 */
export const FIELD_DETAIL = "detail";

/**
 * Where somebody put a card, if they put it anywhere.
 *
 * The board's layout is a function of the tree and produces a readable
 * arrangement of a document nobody has touched. These two are the exception
 * and they are the whole of dragging: a card that has been moved by hand stays
 * where the hand left it, and every card that has not is still placed by the
 * layout around it.
 *
 * Map space, which is the space the layout works in: the same units the
 * computed positions are in, so the two can sit side by side on one board.
 * Both or neither -- a card with one of them is treated as unplaced, because
 * half a coordinate is not a place.
 */
export const FIELD_X = "x";
export const FIELD_Y = "y";

export { FIELD_EXTRACTED_FROM, FIELD_TASK_ID };

/** Which of the four faces of a workspace is on screen. */
export type Mode = "orientation" | "isolation" | "implementation" | "documentation";

/** In the order the toggle offers them, which is the order work happens in. */
export const MODES: readonly Mode[] = [
  "orientation",
  "isolation",
  "implementation",
  "documentation",
];

export const MODE_LABELS: Record<Mode, string> = {
  orientation: "Orientation",
  isolation: "Isolation",
  implementation: "Implementation",
  documentation: "Documentation",
};

/** What each mode is for, for the toggle's description line. */
export const MODE_HINTS: Record<Mode, string> = {
  orientation: "See what is there, what is not, and what to build",
  isolation: "Take one thing apart until it is tasks",
  implementation: "Watch the team do them",
  documentation: "Publish what was built",
};

/**
 * What a mode was called before the four-mode rename.
 *
 * Saved workspaces hold the mode they were left in, and a snapshot written
 * before this existed says "idea". Dropping those back to the first mode would
 * be survivable; silently dropping them to a mode that no longer exists is
 * not, because MODES.includes() is what decides whether the value is kept.
 */
const RENAMED: Record<string, Mode> = {
  idea: "orientation",
  planning: "isolation",
  work: "implementation",
};

/** The mode a stored value means, or null when it means nothing. */
export function modeFrom(value: unknown): Mode | null {
  if (typeof value !== "string") return null;
  if ((MODES as readonly string[]).includes(value)) return value as Mode;
  return RENAMED[value] ?? null;
}

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
  return node.fields[FIELD_TYPE] === TYPE_TASK;
}

export function isCollapsed(node: Node): boolean {
  return node.fields[FIELD_COLLAPSED] === true;
}

/**
 * Where a card was put by hand, or null for one the layout still places.
 *
 * Non-finite numbers read as absent: a NaN that reached a field would put a
 * card nowhere at all, and nowhere is off the map with no way back to it.
 */
export function placedAt(node: Node): { x: number; y: number } | null {
  const x = node.fields[FIELD_X];
  const y = node.fields[FIELD_Y];
  if (typeof x !== "number" || typeof y !== "number") return null;
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { x, y };
}

/** The icon name on a line, or "" for none. */
export function iconOf(node: Node): string {
  const value = node.fields[FIELD_ICON];
  return typeof value === "string" ? value : "";
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
  walk(tree.filter(isOutlineNode), "", 0);
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

/**
 * Whether a node is a line the outline draws.
 *
 * By what it is, not by what it is not. "Everything except a task" was right
 * when there were two types and became wrong the moment there were four,
 * silently: an edge would have been drawn as a line with no text in it.
 */
export function isOutlineNode(node: Node): boolean {
  const type = node.fields[FIELD_TYPE];
  return type === undefined || type === TYPE_IDEA;
}

/** Whether a node is a link between two others. */
export function isEdge(node: Node): boolean {
  return node.fields[FIELD_TYPE] === TYPE_EDGE;
}

/** Whether a node is a named set of nodes. */
export function isRegion(node: Node): boolean {
  return node.fields[FIELD_TYPE] === TYPE_REGION;
}

export function isGuideline(node: Node): boolean {
  return node.fields[FIELD_TYPE] === TYPE_GUIDELINE;
}

export function isParty(node: Node): boolean {
  return node.fields[FIELD_TYPE] === TYPE_PARTY;
}

/** Whether a node joins a card to a guideline. */
export function isGuided(node: Node): boolean {
  return node.fields[FIELD_TYPE] === TYPE_GUIDED;
}

/** Whether a node joins a card to an interested party. */
export function isInterest(node: Node): boolean {
  return node.fields[FIELD_TYPE] === TYPE_INTEREST;
}

/** What a card says at length, or "" when it says nothing more than its title. */
export function detailOf(node: Node | null | undefined): string {
  const value = node?.fields[FIELD_DETAIL];
  return typeof value === "string" ? value : "";
}
