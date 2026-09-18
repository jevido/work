/**
 * Where the lines of an outline sit when it is drawn as a map.
 *
 * Computed for every card nobody has moved. The `position` on a node is a sort
 * key -- a fractional index minted between two siblings, see position.ts -- and
 * a sort key orders lines, it does not place them on a plane, so the plane is
 * worked out here from the shape of the tree.
 *
 * A card somebody has dragged is the exception, and carries `x` and `y` of its
 * own. That is stored state and it does have to be merged, which is the price
 * of a board where moving something moves it: an arrangement that sprang back
 * to the computed one the moment it was let go is not a board, it is a picture
 * of a tree. Last write wins, the same as every other field.
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

/**
 * The two fields a dragged card carries, read here rather than imported.
 *
 * Spelled twice on purpose: model.ts declares them for the document, and this
 * file is the geometry the read-only web viewer shares, which has no document
 * model and never will. Two declarations of two short strings is the cost of
 * that split, and it is the same trade carry.ts makes for the wire's op kinds.
 */
const FIELD_X = "x";
const FIELD_Y = "y";

/** Where a card was put by hand, or null for one the layout still places. */
function placedAt(node: { fields: Record<string, unknown> }): { x: number; y: number } | null {
  const x = node.fields[FIELD_X];
  const y = node.fields[FIELD_Y];
  if (typeof x !== "number" || typeof y !== "number") return null;
  // A non-finite coordinate is off the map with no way back to it, so it reads
  // as no coordinate at all.
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { x, y };
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
  /**
   * Which top-level branch this is under -- the index of its root in `rows`.
   *
   * What the board colours by. Computed rather than stored, for the same reason
   * everything else here is: the same tree gives the same index on every
   * machine and in the viewer, so two people never see one note in two colours
   * and there is no field to merge. -1 is the subject in the middle, which
   * belongs to no branch.
   */
  cluster: number;
  /** The text, already wrapped to the box width. Empty for an empty line. */
  lines: string[];
  /**
   * The tilt a note is pinned at, in radians.
   *
   * Hashed from the id. Random would shimmer every frame, and stored would be
   * a third thing to merge for the sake of a degree and a half.
   */
  angle: number;
  /**
   * The glyph on a cluster head, by name, or "" for none.
   *
   * Carried on the box rather than asked of the scene, because the room it
   * takes is part of how tall the note is -- and a renderer that reserved that
   * room out of a source the layout could not see would be the two of them
   * disagreeing about the same note.
   */
  icon: string;
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
  /**
   * The point a reader should be looking at when the map opens.
   *
   * A board is built around a subject and belongs in the middle of the
   * window, which is a different point from the corner the boxes start at --
   * so it is answered here rather than guessed at by the renderer. The
   * renderer centres this point and then clamps, so a map larger than the
   * window still never shows blank paper past its own edge.
   */
  home: { x: number; y: number };
}

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

/**
 * Where the cluster heads sit, and how a cluster grows away from them.
 *
 * The ring is wider than it is tall because screens are, and a circle of heads
 * on a 16:9 window wastes the two ends of it.
 */
const BOARD_RING_X = 340;
const BOARD_RING_Y = 240;
/** The gap between one generation of a cluster and the next, and between notes. */
const BOARD_STEP = 54;
const BOARD_GAP = 14;
/** How much clear board is left between two clusters. */
const CLUSTER_GAP = 34;
/** How many times overlapping clusters are pushed apart before we stop trying. */
const RELAX_PASSES = 32;
/**
 * How wide the two notes that carry the board are.
 *
 * TIERS runs the other way -- 150 at the root, 210 four steps down -- which
 * suits an indented tree, where depth 0 is a heading over a column of detail
 * and the detail is what has words in it. On a board it is backwards: the
 * subject is the sentence somebody reads first and the leaves are three words
 * each, and the narrowest paper on the board holding the longest line is how
 * the middle of the reference picture ends up as an ellipsis.
 */
const SUBJECT_WIDTH = 270;
const HEAD_WIDTH = 200;

/**
 * How wide a string is, in the font it will be drawn in.
 *
 * A callback rather than a measurement, because this file is pure and means to
 * stay that way: the web viewer imports it, and a unit test of a layout has no
 * canvas within reach. The renderer hands in a cached `ctx.measureText`;
 * everything else gets `estimate`, which is close enough to decide where a line
 * wraps and -- the part that matters -- gives the same answer twice.
 */
export type Measure = (text: string, font: string) => number;

/** Average advance as a fraction of the font size, for a proportional sans. */
const AVERAGE_ADVANCE = 0.52;

/** The size out of a CSS font shorthand, or 13 if it does not say. */
export function sizeOf(font: string): number {
  const found = /(\d+(?:\.\d+)?)px/.exec(font);
  return found ? Number.parseFloat(found[1]) : 13;
}

export const estimate: Measure = (text, font) => text.length * sizeOf(font) * AVERAGE_ADVANCE;

/** How many lines a note holds before the rest is elided. */
const MAX_LINES = 4;
/**
 * The room inside a note, and how its lines are spaced.
 *
 * Exported because the renderer draws inside the box this file sized, and two
 * copies of these numbers is two of them being edited and one of them not --
 * which shows up as text sitting a few pixels outside its own paper.
 */
export const TEXT_PAD_X = 12;
export const TEXT_PAD_Y = 8;
export const LINE_HEIGHT = 1.35;

/**
 * Breaks a line into the lines a note will actually show.
 *
 * Words, then characters when a single word is wider than the note -- a URL or
 * a Java class name is one word and would otherwise run off the paper. The last
 * line is elided rather than the note growing without limit: a note is
 * something you can read at a glance, and the full text is one click away in
 * the editor that opens on it.
 */
export function wrap(text: string, width: number, font: string, measure: Measure): string[] {
  const room = width - TEXT_PAD_X * 2;
  const words = text.split(/\s+/).filter((word) => word !== "");
  if (words.length === 0) return [];

  const lines: string[] = [];
  let line = "";
  for (const word of words) {
    for (const piece of split(word, room, font, measure)) {
      const next = line === "" ? piece : `${line} ${piece}`;
      if (line !== "" && measure(next, font) > room) {
        lines.push(line);
        line = piece;
      } else {
        line = next;
      }
    }
  }
  if (line !== "") lines.push(line);

  if (lines.length <= MAX_LINES) return lines;
  const kept = lines.slice(0, MAX_LINES);
  kept[MAX_LINES - 1] = `${kept[MAX_LINES - 1].replace(/\s*\S*$/, "")}\u2026`;
  return kept;
}

/** One word, as the pieces that fit. Most words come back as themselves. */
function split(word: string, room: number, font: string, measure: Measure): string[] {
  if (measure(word, font) <= room) return [word];
  const pieces: string[] = [];
  let piece = "";
  for (const char of word) {
    if (piece !== "" && measure(piece + char, font) > room) {
      pieces.push(piece);
      piece = char;
    } else {
      piece += char;
    }
  }
  if (piece !== "") pieces.push(piece);
  return pieces;
}

/** A note's size once its text has been wrapped into it. */
interface Shape {
  width: number;
  height: number;
  lines: string[];
}

/**
 * How big a note has to be to hold what is written on it.
 *
 * Width is the tier's and does not move -- a column of notes at one depth all
 * the same width is most of what makes a map readable. Height is the tier's
 * until the text needs more, which is what lets a sentence be a note rather
 * than an ellipsis.
 */
function shape(row: MapRow, measure: Measure, width?: number): Shape {
  const base = tier(row.depth);
  const metrics = width === undefined ? base : { ...base, width };
  const lines = wrap(textOf(row), metrics.width, metrics.font, measure);
  const room = iconOf(row) === "" ? 0 : ICON_ROOM;
  const text = Math.ceil(lines.length * sizeOf(metrics.font) * LINE_HEIGHT) + TEXT_PAD_Y * 2 + room;
  return { width: metrics.width, height: Math.max(metrics.height + room, text), lines };
}

/** The field read here. Named, not imported: see MapRow for why. */
const FIELD_ICON = "icon";

function iconOf(row: MapRow): string {
  const value = row.node.fields[FIELD_ICON];
  return typeof value === "string" ? value : "";
}

/** The room a glyph takes above the text, when there is one. */
export const ICON_ROOM = 26;

/**
 * The tilt a note is pinned at.
 *
 * FNV-1a over the id, because it is five lines and the same five lines
 * everywhere. `>>> 0` before the modulo: `Math.imul` leaves a signed 32-bit
 * value, and a negative one would use half the range and tilt half the notes
 * the same way.
 */
const MAX_TILT = 0.026;

function tiltOf(id: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (((hash >>> 0) % 2001) / 1000 - 1) * MAX_TILT;
}

function boxOf(row: MapRow, x: number, y: number, made: Shape, cluster: number): Box {
  return {
    id: row.node.id,
    text: textOf(row),
    depth: row.depth,
    x,
    y,
    width: made.width,
    height: made.height,
    cluster,
    lines: made.lines,
    angle: tiltOf(row.node.id),
    icon: iconOf(row),
  };
}

/**
 * Which branch each line belongs to.
 *
 * One pass, because `rows` is outline order and a parent is always in it before
 * its children. `seed` is the depth a new branch starts at: the top level
 * normally, one deeper when the board found a subject to put in the middle.
 * Anything above the seed belongs to no branch and gets -1.
 */
/**
 * The depth at which a new branch starts.
 *
 * One root with something under it is a subject: it belongs to no branch of its
 * own, and the branches are its children. Anything else -- several roots, or a
 * single root with nothing under it -- and the top level is the branches.
 *
 * Without it, a document with one root comes out in a single colour, because
 * everything in it is under the one branch.
 */
function seedDepth(rows: readonly MapRow[]): number {
  let roots = 0;
  let only = "";
  for (const row of rows) {
    if (row.parentId !== "") continue;
    roots++;
    only = row.node.id;
  }
  if (roots !== 1) return 0;
  return rows.some((row) => row.parentId === only) ? 1 : 0;
}

function clustersOf(rows: readonly MapRow[], seed: number): Map<string, number> {
  const at = new Map<string, number>();
  let next = 0;
  for (const row of rows) {
    if (row.depth < seed) at.set(row.node.id, -1);
    else if (row.depth === seed) at.set(row.node.id, next++);
    else at.set(row.node.id, at.get(row.parentId) ?? 0);
  }
  return at;
}

/**
 * Lays the rows out as the board, then puts back the cards somebody moved.
 *
 * The arrangement is a function of the tree for every card nobody has touched,
 * which is what makes a document that has never been dragged readable the
 * first time it is opened. A card that *has* been dragged carries the place it
 * was left on it -- see placedAt -- and that wins: the whole point of moving
 * one by hand is that it is still there afterwards.
 *
 * The two live in one space. Placements are read in the same units the layout
 * works in and everything is shifted into positive space together at the end,
 * so a placed card and a computed one keep their positions relative to each
 * other however the rest of the tree changes shape around them.
 */
export function layout(rows: readonly MapRow[], measure: Measure = estimate): Layout {
  const map = clustered(rows, measure);
  return place(rows, map);
}

/**
 * Moves every card that has a place of its own to it, and renormalises.
 *
 * Renormalised because the layout hands back a map whose top left is at
 * PADDING, and the renderer pans in that space: a card dragged above or to the
 * left of everything else would otherwise sit at a negative coordinate, which
 * is off the map rather than on the part of it nobody has scrolled to.
 *
 * `home` moves with everything else. It is a point in the same space, and a
 * board that opened on where the middle used to be would be answering a
 * question about a document that has since been rearranged.
 */
function place(rows: readonly MapRow[], map: Layout): Layout {
  let moved = false;
  for (const row of rows) {
    const at = placedAt(row.node);
    if (!at) continue;
    const box = map.byId.get(row.node.id);
    if (!box) continue;
    box.x = at.x;
    box.y = at.y;
    moved = true;
  }
  if (!moved || map.boxes.length === 0) return map;

  const bounds = extentOf(map.boxes);
  const shiftX = PADDING - bounds.left;
  const shiftY = PADDING - bounds.top;
  for (const box of map.boxes) {
    box.x += shiftX;
    box.y += shiftY;
  }
  return {
    ...map,
    ...extent(map.boxes),
    home: { x: map.home.x + shiftX, y: map.home.y + shiftY },
  };
}

/** One branch per line that has a parent on the map, once they are all placed. */
function link(rows: readonly MapRow[], byId: Map<string, Box>, branches: Branch[]): void {
  for (const row of rows) {
    if (row.parentId === "") continue;
    if (byId.has(row.parentId) && byId.has(row.node.id)) {
      branches.push({ from: row.parentId, to: row.node.id });
    }
  }
}

/**
 * The board: a subject in the middle, clusters around it, notes fanning out.
 *
 * The one shape the map is drawn in. What it says that an indented tree does
 * not is which handful of things this is about -- a tree of forty lines has
 * one column of nine and no centre, and you have to read it to find out where
 * the weight is.
 *
 * Three ideas hold it up:
 *
 *   A cluster grows *away* from the middle. A head on the left of the board
 *   puts its notes further left, so nothing has to cross the centre to reach
 *   the thing it belongs to. Within a cluster it is depth outward, order down,
 *   every parent level with its own children.
 *
 *   Heads get a share of the circle proportional to their weight. An even
 *   split is why a branch with nine descendants and one with two would come
 *   out equally cramped.
 *
 *   Clusters are then pushed apart until they stop overlapping. A ring cannot
 *   know in advance how tall a cluster will be once its text has wrapped, so
 *   the ring is the guess and this is the correction.
 *
 * Positions are still nobody's decision: every number below is a function of
 * the tree, so two people looking at one workspace see one board.
 */
function clustered(rows: readonly MapRow[], measure: Measure): Layout {
  const boxes: Box[] = [];
  const byId = new Map<string, Box>();
  const branches: Branch[] = [];
  if (rows.length === 0) {
    return { boxes, byId, branches, width: PADDING * 2, height: PADDING * 2, home: { x: 0, y: 0 } };
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

  const trunk = roots;

  /*
   * One root with something under it is a subject, and the board is built
   * around it. Several roots -- which is what an outline usually is -- ring a
   * point instead: promoting the first of them to the middle would be choosing
   * a subject nobody chose.
   */
  const seed = seedDepth(rows);
  const middle = seed === 1 ? trunk[0] : null;
  const heads = middle ? (children.get(middle.node.id) ?? []) : trunk;
  const at = clustersOf(rows, seed);

  /** A cluster, as the boxes it owns. Moved as one thing by the relaxation. */
  interface Cluster {
    boxes: Box[];
    /** The subject does not move; everything else gets out of its way. */
    fixed: boolean;
  }
  const clusters: Cluster[] = [];

  /** How wide a note is on a board, which is not how wide it is in a tree. */
  function widthOf(row: MapRow): number | undefined {
    if (middle && row.node.id === middle.node.id) return SUBJECT_WIDTH;
    return row.depth === (middle ? 1 : 0) ? HEAD_WIDTH : undefined;
  }

  if (middle) {
    const made = shape(middle, measure, SUBJECT_WIDTH);
    clusters.push({
      boxes: [boxOf(middle, -made.width / 2, -made.height / 2, made, -1)],
      fixed: true,
    });
  }

  /**
   * One cluster, laid out in its own frame with the head centred on the origin.
   *
   * `dir` is which way it grows: +1 for a head on the right of the board, -1
   * for one on the left.
   */
  function grow(head: MapRow, dir: 1 | -1): Box[] {
    const made: Box[] = [];
    let cursor = 0;

    /** How far out the nth generation of this cluster sits, from the head. */
    function column(step: number): number {
      let x = HEAD_WIDTH + BOARD_STEP;
      for (let i = 1; i < step; i++) x += tier(head.depth + i).width + BOARD_STEP;
      return step === 0 ? 0 : x;
    }

    function place(row: MapRow): number {
      const box = shape(row, measure, widthOf(row));
      const kids = children.get(row.node.id) ?? [];

      let centre: number;
      if (kids.length === 0) {
        centre = cursor + box.height / 2;
        cursor += box.height + BOARD_GAP;
      } else {
        const first = place(kids[0]);
        let last = first;
        for (let i = 1; i < kids.length; i++) last = place(kids[i]);
        centre = (first + last) / 2;
      }

      const step = row.depth - head.depth;
      const x = dir === 1 ? column(step) : -column(step) - box.width;
      made.push(boxOf(row, x, centre - box.height / 2, box, at.get(row.node.id) ?? 0));
      return centre;
    }

    place(head);

    // The head is placed last -- its own position is a fact about its children
    // -- so it is the last thing pushed, and everything else is moved to it.
    const anchor = made[made.length - 1];
    const dx = -(anchor.x + anchor.width / 2);
    const dy = -(anchor.y + anchor.height / 2);
    for (const box of made) {
      box.x += dx;
      box.y += dy;
    }
    return made;
  }

  /** A head's share of the circle: itself and everything under it. */
  function weigh(row: MapRow): number {
    let total = 1;
    for (const kid of children.get(row.node.id) ?? []) total += weigh(kid);
    return total;
  }

  const weights = heads.map(weigh);
  const total = weights.reduce((sum, weight) => sum + weight, 0) || 1;
  // A quarter turn back, so the first cluster is at the top rather than at
  // three o'clock -- which is where a reader looks first.
  let from = -Math.PI / 2;
  for (let i = 0; i < heads.length; i++) {
    const slice = (weights[i] / total) * Math.PI * 2;
    const angle = from + slice / 2;
    from += slice;
    const cx = Math.cos(angle) * BOARD_RING_X;
    const cy = Math.sin(angle) * BOARD_RING_Y;
    const grown = grow(heads[i], cx < 0 ? -1 : 1);
    for (const box of grown) {
      box.x += cx;
      box.y += cy;
    }
    clusters.push({ boxes: grown, fixed: false });
  }

  relax(clusters);

  for (const cluster of clusters) {
    for (const box of cluster.boxes) {
      boxes.push(box);
      byId.set(box.id, box);
    }
  }

  // Built from the rows rather than as the boxes are made: `grow` places a
  // cluster before the next one exists, and the branch from the subject to a
  // head crosses two of them.
  link(rows, byId, branches);

  // Map space has no negative half, and the board is built around an origin in
  // the middle of it, so the whole thing is slid into view.
  const bounds = extentOf(boxes);
  const shiftX = PADDING - bounds.left;
  const shiftY = PADDING - bounds.top;
  for (const box of boxes) {
    box.x += shiftX;
    box.y += shiftY;
  }

  // The board was built around the origin, so that is where its subject sits
  // -- or, with several roots and no subject, the point they ring.
  return { boxes, byId, branches, ...extent(boxes), home: { x: shiftX, y: shiftY } };
}

/**
 * Pushes overlapping clusters apart, along whichever axis is the shorter move.
 *
 * Separating two clusters the short way keeps the ring a ring: resolving a
 * 20-pixel vertical overlap by sliding one of them 400 pixels sideways is a
 * correct answer to the wrong question. Bounded rather than run to convergence,
 * because a board crowded enough not to converge still has to be drawn, and a
 * layout that can loop is a layout that can hang a frame.
 */
function relax(clusters: { boxes: Box[]; fixed: boolean }[]): void {
  for (let pass = 0; pass < RELAX_PASSES; pass++) {
    let moved = false;
    for (let i = 0; i < clusters.length; i++) {
      for (let j = i + 1; j < clusters.length; j++) {
        const a = clusters[i];
        const b = clusters[j];
        if (a.fixed && b.fixed) continue;
        const one = extentOf(a.boxes);
        const two = extentOf(b.boxes);
        const overX = Math.min(one.right, two.right) - Math.max(one.left, two.left) + CLUSTER_GAP;
        const overY = Math.min(one.bottom, two.bottom) - Math.max(one.top, two.top) + CLUSTER_GAP;
        if (overX <= 0 || overY <= 0) continue;

        moved = true;
        const push = Math.min(overX, overY) / 2 + 0.5;
        // A fixed cluster does not take its half, so the other takes both.
        const mine = a.fixed ? 0 : b.fixed ? push * 2 : push;
        const theirs = b.fixed ? 0 : a.fixed ? push * 2 : push;
        if (overX < overY) {
          const away = one.left + one.right <= two.left + two.right ? -1 : 1;
          shift(a.boxes, away * mine, 0);
          shift(b.boxes, -away * theirs, 0);
        } else {
          const away = one.top + one.bottom <= two.top + two.bottom ? -1 : 1;
          shift(a.boxes, 0, away * mine);
          shift(b.boxes, 0, -away * theirs);
        }
      }
    }
    if (!moved) return;
  }
}

function shift(boxes: Box[], dx: number, dy: number): void {
  if (dx === 0 && dy === 0) return;
  for (const box of boxes) {
    box.x += dx;
    box.y += dy;
  }
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
