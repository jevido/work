/**
 * Where the lines of an outline sit when it is drawn as a map.
 *
 * Computed, not stored. The `position` on a node is a sort key -- a fractional
 * index minted between two siblings, see position.ts -- and a sort key orders
 * lines, it does not place them on a plane. Storing coordinates instead would
 * be a second thing to merge, and two people dragging one node would land it
 * somewhere neither of them chose.
 *
 * So the map is a view of the tree: the tree decides where things are, and a
 * drag is how somebody changes the tree. What the canvas gives that the outline
 * cannot is seeing the shape all at once, and seeing the links that do not run
 * along it.
 *
 * Pure, and separate from the renderer, because a layout that can only be
 * checked by looking at pixels is a layout nobody checks.
 */
import type { Row } from "../workspace/model";

/** One node, boxed, in map space. */
export interface Box {
  id: string;
  text: string;
  depth: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A line from a parent to one of its children. */
export interface Branch {
  from: string;
  to: string;
}

/** The whole map, in map space, with its own extent. */
export interface Layout {
  boxes: Box[];
  byId: Map<string, Box>;
  branches: Branch[];
  width: number;
  height: number;
}

/** How wide a column is, how tall a row is, and how big a box gets. */
export const COLUMN = 220;
export const ROW = 44;
export const BOX_HEIGHT = 30;
export const BOX_WIDTH = 190;
export const PADDING = 40;

/**
 * Lays the rows out as a tidy tree: depth across, order down.
 *
 * The rows, not the tree, and deliberately: `rows` is what the outline renders
 * and what the keyboard walks, already flattened, already respecting folds. A
 * map built from the tree would show branches the outline has folded away, so
 * the two views would disagree about what is on screen while claiming to be the
 * same document.
 */
export function layout(rows: readonly Row[]): Layout {
  const boxes: Box[] = [];
  const byId = new Map<string, Box>();
  const branches: Branch[] = [];

  rows.forEach((row, at) => {
    const box: Box = {
      id: row.node.id,
      text: textOf(row),
      depth: row.depth,
      x: PADDING + row.depth * COLUMN,
      y: PADDING + at * ROW,
      width: BOX_WIDTH,
      height: BOX_HEIGHT,
    };
    boxes.push(box);
    byId.set(box.id, box);
    if (row.parentId && byId.has(row.parentId)) {
      branches.push({ from: row.parentId, to: box.id });
    }
  });

  return {
    boxes,
    byId,
    branches,
    width: PADDING * 2 + (maxDepth(rows) + 1) * COLUMN,
    height: PADDING * 2 + Math.max(rows.length, 1) * ROW,
  };
}

/** Which box a point in map space is in, if any. Topmost last drawn wins. */
export function boxAt(map: Layout, x: number, y: number): Box | null {
  for (let i = map.boxes.length - 1; i >= 0; i--) {
    const box = map.boxes[i];
    if (x >= box.x && x <= box.x + box.width && y >= box.y && y <= box.y + box.height) {
      return box;
    }
  }
  return null;
}

/**
 * The rectangle around a set of boxes, with room to breathe.
 *
 * What a region is drawn as. A region whose members are scattered gets a large
 * rectangle with unrelated lines inside it, and that is honest: it is what
 * having put them in one region while leaving them apart actually looks like.
 */
export function hull(boxes: readonly Box[], margin = 10): {
  x: number;
  y: number;
  width: number;
  height: number;
} | null {
  if (boxes.length === 0) return null;
  let left = Infinity;
  let top = Infinity;
  let right = -Infinity;
  let bottom = -Infinity;
  for (const box of boxes) {
    left = Math.min(left, box.x);
    top = Math.min(top, box.y);
    right = Math.max(right, box.x + box.width);
    bottom = Math.max(bottom, box.y + box.height);
  }
  return {
    x: left - margin,
    y: top - margin,
    width: right - left + margin * 2,
    height: bottom - top + margin * 2,
  };
}

function maxDepth(rows: readonly Row[]): number {
  let deepest = 0;
  for (const row of rows) deepest = Math.max(deepest, row.depth);
  return deepest;
}

function textOf(row: Row): string {
  const value = row.node.fields.text;
  return typeof value === "string" ? value : "";
}
