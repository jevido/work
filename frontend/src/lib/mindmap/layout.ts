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
/**
 * The least a row has to be for this file to place it.
 *
 * Structural, and deliberately not an import of `Row` from workspace/model.
 * Nothing here needs a fold state, a sibling count or a descendant count -- it
 * needs an id, some text, a depth and a parent -- and typing it as the outline's
 * own Row would tie a pure geometry file to the desktop app's document model.
 *
 * That matters because the read-only web viewer draws the same map from a
 * different document type: the server merges for it and hands back a plain
 * tree, so it has no Row and never will. The desktop app's Row satisfies this
 * by shape, so nothing there changed.
 */
export interface MapRow {
  node: { id: string; fields: Record<string, unknown> };
  depth: number;
  /** Empty at the top level, matching the protocol's own "" for a root. */
  parentId: string;
}

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

/**
 * The two shapes the same document can be drawn in.
 *
 * "tidy" is depth across and order down, which is the outline's own shape laid
 * on its side -- a map you can read top to bottom and still find the line you
 * were looking at in the other view. "radial" puts the root in the middle and
 * throws the branches outward, which says nothing about order and everything
 * about how many directions the thinking went in.
 *
 * Both are functions of the same tree. Neither stores anything, so switching is
 * a repaint and not an edit.
 */
export type LayoutKind = "tidy" | "radial";

export const LAYOUTS: readonly LayoutKind[] = ["tidy", "radial"];

export const LAYOUT_LABELS: Record<LayoutKind, string> = {
  tidy: "Tidy",
  radial: "Radial",
};

/**
 * How a box is drawn at each depth.
 *
 * Depth is the only ranking a tree has, so it is the one the map shows: the
 * root is the biggest and brightest thing on screen, and each step away from it
 * is a little smaller, a little darker and a little further back. Without this
 * a map of forty identical rectangles makes you read every one of them to find
 * out which is the subject and which is a footnote.
 *
 * Four tiers rather than a formula. Past the third step the difference stops
 * meaning anything -- everything that deep is detail -- so the last tier
 * repeats rather than fading to nothing.
 *
 * Size and shape only. What each tier is *coloured* is in palette.ts, because
 * the read-only viewer draws this same map in a light theme and the geometry
 * is the same in both.
 */
export interface Tier {
  width: number;
  height: number;
  radius: number;
  font: string;
}

export const TIERS: readonly Tier[] = [
  {
    width: 150,
    height: 44,
    radius: 8,
    font: "700 15px Inter, system-ui, sans-serif",
  },
  {
    width: 190,
    height: 38,
    radius: 8,
    font: "600 13.5px Inter, system-ui, sans-serif",
  },
  {
    width: 220,
    height: 34,
    radius: 7,
    font: "12.5px Inter, system-ui, sans-serif",
  },
  {
    width: 210,
    height: 30,
    radius: 6,
    font: "12px Inter, system-ui, sans-serif",
  },
];

/** The drawing rules for a depth, clamped to the last tier. */
export function tier(depth: number): Tier {
  return TIERS[Math.min(Math.max(depth, 0), TIERS.length - 1)];
}

/** The gap between a column and the next, and between siblings and subtrees. */
export const COLUMN_GAP = 60;
export const SIBLING_GAP = 16;
export const SUBTREE_GAP = 26;
export const PADDING = 40;

/** Where the branches radiate to, and how far apart they land. */
const RADIAL_RING = 250;
const RADIAL_STEP = 210;
/** How wide a subtree's wedge is allowed to get, so deep branches do not cross. */
const RADIAL_SPREAD = 0.62;

/** Lays the rows out in the shape asked for. Both are functions of the tree. */
export function layout(rows: readonly MapRow[], kind: LayoutKind = "tidy"): Layout {
  return kind === "radial" ? radial(rows) : tidy(rows);
}

/**
 * Depth across, order down, and every parent centred on its own children.
 *
 * The centring is the whole difference between a map and an indented list
 * drawn with boxes. A parent pinned to the top of its subtree makes the eye
 * walk down to find out what it covers; a parent level with the middle of its
 * children can be read as "these belong to that" without following a single
 * line.
 *
 * Two passes, which is what makes it possible at all: the first stacks the
 * leaves, because a leaf is the only node whose position does not depend on
 * anything below it, and the second lifts every parent to the middle of the
 * span its children ended up occupying. One pass cannot do it -- a parent's
 * place is a fact about its descendants, and they have not been placed yet.
 */
function tidy(rows: readonly MapRow[]): Layout {
  const boxes: Box[] = [];
  const byId = new Map<string, Box>();
  const branches: Branch[] = [];
  if (rows.length === 0) {
    return { boxes, byId, branches, width: PADDING * 2, height: PADDING * 2 };
  }

  // Columns first: a box's x depends only on its depth, and every box at a
  // depth is the same width, so the column a depth sits in is the sum of the
  // widths before it. Computed once rather than per box.
  const deepest = rows.reduce((most, row) => Math.max(most, row.depth), 0);
  const columns: number[] = [];
  let x = PADDING;
  for (let depth = 0; depth <= deepest; depth++) {
    columns.push(x);
    x += tier(depth).width + COLUMN_GAP;
  }

  const children = new Map<string, MapRow[]>();
  const roots: MapRow[] = [];
  for (const row of rows) {
    if (row.parentId === "") roots.push(row);
    else {
      const list = children.get(row.parentId);
      if (list) list.push(row);
      else children.set(row.parentId, [row]);
    }
  }

  // `cursor` is the next free y for a leaf. It only ever moves downward, which
  // is what keeps two subtrees from overlapping without any of them knowing
  // about each other.
  let cursor = PADDING;

  for (const root of roots) {
    place(root);
    cursor += SUBTREE_GAP;
  }

  /** Places a row and everything under it, and answers with its centre. */
  function place(row: MapRow): number {
    const metrics = tier(row.depth);
    const kids = children.get(row.node.id) ?? [];

    let centre: number;
    if (kids.length === 0) {
      centre = cursor + metrics.height / 2;
      cursor += metrics.height + SIBLING_GAP;
    } else {
      // The children first, then this. A parent drawn before its subtree would
      // have to guess how tall the subtree was going to be.
      const first = place(kids[0]);
      let last = first;
      for (let i = 1; i < kids.length; i++) last = place(kids[i]);
      centre = (first + last) / 2;
    }

    const box: Box = {
      id: row.node.id,
      text: textOf(row),
      depth: row.depth,
      x: columns[row.depth],
      y: centre - metrics.height / 2,
      width: metrics.width,
      height: metrics.height,
    };
    boxes.push(box);
    byId.set(box.id, box);
    if (row.parentId !== "" && byId.has(row.parentId)) {
      branches.push({ from: row.parentId, to: box.id });
    }
    return centre;
  }

  // A parent centred between widely separated children can end up above the
  // first of them, and the extent has to cover that rather than clipping it.
  return { boxes, byId, branches, ...extent(boxes) };
}

/**
 * The root in the middle, and the branches thrown outward from it.
 *
 * The same tree, saying a different thing about itself. Order is gone --
 * nothing about a ring says which of two branches came first -- and what is
 * left is how many directions there are and how far each one went. That is the
 * question a long outline stops being able to answer, because by then the
 * shape is taller than the screen.
 *
 * Each subtree keeps a wedge of angle to itself, and a child's wedge is a
 * fraction of its parent's. That is what stops deep branches from fanning into
 * each other: a branch cannot spread wider than the room its parent had.
 */
function radial(rows: readonly MapRow[]): Layout {
  const boxes: Box[] = [];
  const byId = new Map<string, Box>();
  const branches: Branch[] = [];
  if (rows.length === 0) {
    return { boxes, byId, branches, width: PADDING * 2, height: PADDING * 2 };
  }

  const children = new Map<string, MapRow[]>();
  const roots: MapRow[] = [];
  for (const row of rows) {
    if (row.parentId === "") roots.push(row);
    else {
      const list = children.get(row.parentId);
      if (list) list.push(row);
      else children.set(row.parentId, [row]);
    }
  }

  // Several top-level lines and no single root is the ordinary case for an
  // outline, so the centre is a point rather than a node: the top level rings
  // it, exactly as one root's children would.
  const centreX = 0;
  const centreY = 0;
  // Starting a quarter turn back puts the first branch at the top rather than
  // at three o'clock, which is where a reader looks first.
  spread(roots, -Math.PI / 2, Math.PI * 2, 0);

  /**
   * Places a run of siblings across a wedge.
   *
   * @param from Where the wedge starts, and `width` how much of it there is.
   *   A child gets its own slice of that, so the arithmetic is the same at
   *   every depth and nothing has to know how deep it is.
   */
  function spread(list: readonly MapRow[], from: number, width: number, depth: number) {
    if (list.length === 0) return;
    const slice = width / list.length;
    for (let i = 0; i < list.length; i++) {
      const row = list[i];
      // The middle of this child's slice.
      const angle = from + slice * (i + 0.5);
      const metrics = tier(row.depth);
      const radius = RADIAL_RING + RADIAL_STEP * depth;
      const box: Box = {
        id: row.node.id,
        text: textOf(row),
        depth: row.depth,
        x: centreX + Math.cos(angle) * radius - metrics.width / 2,
        y: centreY + Math.sin(angle) * radius - metrics.height / 2,
        width: metrics.width,
        height: metrics.height,
      };
      boxes.push(box);
      byId.set(box.id, box);
      if (row.parentId !== "" && byId.has(row.parentId)) {
        branches.push({ from: row.parentId, to: box.id });
      }

      const kids = children.get(row.node.id) ?? [];
      if (kids.length > 0) {
        // Narrower than the slice this child got, and centred on it. Equal
        // would let a subtree's outermost leaves sit exactly on the boundary
        // its neighbour's do.
        const wedge = slice * RADIAL_SPREAD;
        spread(kids, angle - wedge / 2, wedge, depth + 1);
      }
    }
  }

  // Map space has no negative half, so the whole thing is slid into view. The
  // renderer pans in map space and would otherwise start outside the document.
  const bounds = extentOf(boxes);
  const shiftX = PADDING - bounds.left;
  const shiftY = PADDING - bounds.top;
  for (const box of boxes) {
    box.x += shiftX;
    box.y += shiftY;
  }

  return { boxes, byId, branches, ...extent(boxes) };
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
  const bounds = extentOf(boxes);
  return {
    x: bounds.left - margin,
    y: bounds.top - margin,
    width: bounds.right - bounds.left + margin * 2,
    height: bounds.bottom - bounds.top + margin * 2,
  };
}

function extentOf(boxes: readonly Box[]): {
  left: number;
  top: number;
  right: number;
  bottom: number;
} {
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
  if (boxes.length === 0) return { left: 0, top: 0, right: 0, bottom: 0 };
  return { left, top, right, bottom };
}

function extent(boxes: readonly Box[]): { width: number; height: number } {
  const bounds = extentOf(boxes);
  return {
    width: bounds.right + PADDING,
    height: bounds.bottom + PADDING,
  };
}

function textOf(row: MapRow): string {
  const value = row.node.fields.text;
  return typeof value === "string" ? value : "";
}
