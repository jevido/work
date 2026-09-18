/**
 * The map, drawn.
 *
 * One requestAnimationFrame loop and nothing else, the way the office does it —
 * see lib/office/renderer.ts, which this follows deliberately rather than
 * inventing a second way to own a canvas in one app. Hidden, it stops drawing;
 * shown, it resumes where it was rather than being rebuilt.
 *
 * It owns no document. Everything it draws comes from the `Workspace` the
 * outline reads, through one function it is handed: a canvas with its own copy
 * would be a second model to keep in step, and it would drift the first time
 * somebody edited from the other view.
 */
import { DARK, paperOf, readPalette, type Palette } from "./palette";
import { ICON_BOX, iconPath } from "./icons";
import {
  ICON_ROOM,
  LINE_HEIGHT,
  TEXT_PAD_X,
  TEXT_PAD_Y,
  boxAt,
  hull,
  layout,
  sizeOf,
  tier,
  type Box,
  type Layout,
  type MapRow,
  type Measure,
} from "./layout";

/** What the renderer needs to know, fetched fresh each frame it repaints. */
export interface Scene {
  rows: readonly MapRow[];
  /** The region a node is in, or null. */
  regionOf(id: string): { id: string; name: string } | null;
  /** How many tasks were extracted from a line. Zero for most of them. */
  tasksOf(id: string): number;
  /** The line the caret is on, drawn as selected. */
  focused(): string | null;
}

/**
 * Where a drag left things, for the caller to write down.
 *
 * Every card that moved, with the place it ended up in map space: the one in
 * hand and everything hanging off it, which travelled the same distance. A
 * drag that went nowhere is not reported at all.
 *
 * This used to be `{node, onto}` -- a drag was a reparent and the card sprang
 * back to wherever the layout then put it. That is a fine gesture for an
 * outline and a poor one for a board: somebody dragging a card across a board
 * is arranging it, and an arrangement that is thrown away the moment the hand
 * lets go is not one. Reparenting is the outline's job, where it is Tab and
 * Shift+Tab and says what it did.
 */
export interface Drop {
  moves: { id: string; x: number; y: number }[];
}

/*
 * Colours are in palette.ts, read off the canvas so the viewer's light theme
 * gets its own. One thing worth saying here about them: selection is the accent
 * rather than the link blue. Blue already means "a relationship that does not
 * run along the tree" -- links and regions are both drawn in it -- and a
 * selected box in the same colour reads as a third kind of that, rather than as
 * the thing the caret is on. The accent is what the mode toggle and the open
 * tab already use for "here", so the map says it the same way the rest of the
 * window does.
 */

/** How far a pointer may travel and still count as a click rather than a drag. */
const CLICK_SLOP = 4;

/** What the wheel may zoom to. Past either end the map stops being readable. */
const MIN_SCALE = 0.4;
const MAX_SCALE = 2;

/** How far apart the dots on the board are, in map pixels. */
const GRID = 28;
/**
 * The smallest a grid square may get on screen before the dots are dropped.
 *
 * Below this they stop reading as a surface and start reading as noise, and
 * they also stop being cheap: a board zoomed out to a corner is tens of
 * thousands of arcs a frame for a texture nobody can see.
 */
const GRID_FLOOR = 9;

/** What lifts a note off the board. */
const SHADOW = "rgba(0, 0, 0, 0.45)";

/** The glyph on a cluster head, and the tape above it. */
const ICON_SIZE = 18;
const TAPE_WIDTH = 46;
const TAPE_HEIGHT = 15;

/** How big an arrowhead is where a branch arrives. */
const ARROW = 7;

/** How many measured strings are kept before the cache is thrown away. */
const MEASURE_CACHE = 4000;

/*
 * How a dragged branch follows the hand.
 *
 * Dragging used to move one box and leave everything under it where it was,
 * which looks like the branch coming apart -- and since the drop moves the
 * whole branch, it was also a lie about what was going to happen.
 *
 * So the descendants come too, and they lag. STICK is how much of the distance
 * a direct child closes each frame, and FALLOFF makes each generation slower
 * again, so a branch trails out behind the hand and gathers itself when the
 * hand stops. That lag is the whole effect: moving them rigidly reads as
 * dragging a picture of a tree, and the delay is what makes it read as
 * something attached.
 *
 * Per frame rather than per second. This is a feel constant on a loop that
 * only runs while a pointer is down, and a dt-scaled spring here would be
 * arithmetic in service of a number nobody can perceive.
 */
const STICK = 0.3;
const STICK_FALLOFF = 0.62;
/** Under this many pixels from home, a follower has arrived. */
const STICK_REST = 0.4;
/**
 * How long a drop's offsets are held before they are dropped regardless.
 *
 * Long enough for a write to go through the document and come back as a
 * layout, which is a frame or two, with room for a slow one.
 */
const LANDING_FRAMES = 30;

/** How much a note lifts off the board while it is held. */
const LIFT_SCALE = 1.04;
const LIFT_BLUR = 26;
const LIFT_OFFSET = 10;

export class MindmapRenderer {
  #canvas: HTMLCanvasElement;
  #ctx: CanvasRenderingContext2D;
  #scene: () => Scene;
  #onDrop: (drop: Drop) => void;
  #onView: (view: { scale: number }) => void;

  #raf = 0;
  #running = false;
  #shown = true;
  /** Set when something changed and the next frame must actually paint. */
  #dirty = true;
  /**
   * Set when the document changed and the arrangement has to be worked out
   * again.
   *
   * Separate from `#dirty` because most repaints are not re-layouts: panning,
   * zooming, dragging and lifting a note all draw the same boxes in the same
   * places. See #paint.
   */
  #stale = true;
  /** The rows the current layout was built from, to notice a scene swapped
      under the renderer without an invalidate. */
  #rows: readonly unknown[] | null = null;

  /** Pan, in device-independent pixels, and the zoom it is applied under. */
  #panX = 0;
  #panY = 0;
  #scale = 1;

  #map: Layout = {
    boxes: [],
    byId: new Map(),
    branches: [],
    width: 0,
    height: 0,
    home: { x: 0, y: 0 },
  };
  /**
   * The depth a cluster head sits at, which is 0 unless the board found a
   * subject to put in the middle -- and then it is 1.
   *
   * Derived from the layout rather than tracked here: the shallowest thing
   * that belongs to a branch is that branch's head, and a head is drawn on the
   * full-strength paper while everything under it gets the pale one.
   */
  #headDepth = 0;

  /**
   * Measured text, kept between frames.
   *
   * `measureText` is not free, and wrapping asks for the same words again on
   * every repaint of a document that has not changed -- which is what a pan is.
   * The font is part of the key because the same word is a different width at
   * every tier. Dropped wholesale rather than by age: the cost of being wrong
   * is one frame of re-measuring, and an LRU here would be more code than the
   * thing it manages.
   */
  #widths = new Map<string, number>();

  readonly #measure: Measure = (text, font) => {
    const key = `${font}\u0000${text}`;
    const known = this.#widths.get(key);
    if (known !== undefined) return known;
    this.#ctx.font = font;
    const width = this.#ctx.measureText(text).width;
    if (this.#widths.size >= MEASURE_CACHE) this.#widths.clear();
    this.#widths.set(key, width);
    return width;
  };

  /**
   * What is being dragged, and where the pointer is over it.
   *
   * `under` is the dragged node's descendants and how many generations down
   * each one is, so they can be drawn following it and excluded from being
   * dropped onto. The parent is deliberately not in here: dropping a branch
   * somewhere is a statement about the branch, and moving the thing it hangs
   * off would make it a statement about half the map.
   *
   * `trail` is where each follower currently is relative to its laid-out
   * place, eased toward the dragged note's own offset. It is state rather than
   * a function of the pointer because that is what makes it lag.
   */
  #dragging: {
    id: string;
    dx: number;
    dy: number;
    x: number;
    y: number;
    under: Map<string, number>;
    trail: Map<string, { x: number; y: number }>;
  } | null = null;
  /**
   * Where a just-dropped branch is drawn until the document catches up.
   *
   * A drop writes fields; the layout that honours them is built from the
   * document, which arrives a frame or two later. Between the two there is a
   * paint whose layout still has the cards where they were, and dropping the
   * offsets at that moment is a flash: the branch snaps home and then jumps
   * back out to where it was let go.
   *
   * So the offsets outlive the drag. They are frozen at the distance the drop
   * reported -- every card in the branch by the same amount, which is what was
   * written down -- and cleared on the first paint whose layout has actually
   * moved, at which point the offset and the layout are saying the same thing
   * and letting go of it changes nothing on screen.
   */
  #landing: {
    id: string;
    /** Where the layout had the dragged card when the hand let go. */
    from: { x: number; y: number };
    offsets: Map<string, { x: number; y: number }>;
    /** Paints since the drop, so a write that never lands still lets go. */
    frames: number;
  } | null = null;
  /** What the canvas is showing, in map space. Set at the top of every paint
      and read by everything that culls; see #view. */
  #visible = { left: 0, top: 0, right: 0, bottom: 0 };

  /** Panning the background rather than moving a node. */
  #panning: { x: number; y: number } | null = null;

  /** Frames painted, which is what a test counts. */
  painted = 0;

  /**
   * Whether the view has been put somewhere sensible yet.
   *
   * A board is built around a point in the middle of it, so opening one at the
   * top left of map space shows a corner of empty paper with the subject off
   * the right-hand edge. It cannot be done in the constructor -- there is no
   * document and no canvas size yet -- so it happens on the first paint that
   * has both, and once.
   */
  #homed = false;

  /**
   * True where dragging a box would change a document nobody may change.
   *
   * The read-only viewer draws this same map from a document it has no key to
   * write to, so a drag there would be a gesture that appears to work and then
   * silently does not. Pan and zoom stay: those are about looking, and looking
   * is the whole of what the viewer is for.
   */
  #readonly = false;

  /**
   * The colours, read off the canvas rather than hard-coded.
   *
   * Refreshed when the element is measured and when the colour scheme changes,
   * never per frame: getComputedStyle forces layout, and doing it on a repaint
   * would turn every drag into a reflow.
   */
  #palette: Palette = DARK;

  /**
   * What a click landed on, which is not the same question as what a drag did.
   *
   * The map is the only place lines are edited now, so a press on a box has to
   * mean "edit this" as well as "start dragging this" -- and which of the two
   * it was is only known when the pointer comes up. Under a few pixels of
   * movement it was a click.
   */
  #onPick: (id: string | null) => void;
  /** Where the pointer went down, to tell a click from a drag. */
  #from: { x: number; y: number } | null = null;

  constructor(
    canvas: HTMLCanvasElement,
    scene: () => Scene,
    onDrop: (drop: Drop) => void,
    onView: (view: { scale: number }) => void = () => {},
    options: { readonly?: boolean; onPick?: (id: string | null) => void } = {},
  ) {
    this.#readonly = options.readonly === true;
    this.#onPick = options.onPick ?? (() => {});
    this.#canvas = canvas;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("mindmap: no 2d context");
    this.#ctx = ctx;

    this.#scene = scene;
    this.#onDrop = onDrop;
    this.#onView = onView;

    canvas.addEventListener("pointerdown", this.#down);
    canvas.addEventListener("pointermove", this.#move);
    canvas.addEventListener("pointerup", this.#up);
    canvas.addEventListener("pointercancel", this.#up);
    canvas.addEventListener("wheel", this.#wheel, { passive: false });

    this.#palette = readPalette(canvas);
    // A viewer who flips their system theme with the page open should not have
    // to reload to get a readable map.
    this.#scheme = window.matchMedia("(prefers-color-scheme: light)");
    this.#scheme.addEventListener("change", this.#themed);
  }

  #scheme: MediaQueryList;

  readonly #themed = () => {
    this.#palette = readPalette(this.#canvas);
    this.#dirty = true;
  };

  start(): void {
    if (this.#running) return;
    this.#running = true;
    this.#resume();
  }

  stop(): void {
    this.#running = false;
    this.#pause();
    this.#canvas.removeEventListener("pointerdown", this.#down);
    this.#canvas.removeEventListener("pointermove", this.#move);
    this.#canvas.removeEventListener("pointerup", this.#up);
    this.#canvas.removeEventListener("pointercancel", this.#up);
    this.#canvas.removeEventListener("wheel", this.#wheel);
    this.#scheme.removeEventListener("change", this.#themed);
  }

  /**
   * Hidden, it stops drawing. Shown, it picks up where it was.
   *
   * The same rule the office follows, and for the same reason: a canvas torn
   * down and rebuilt on a mode switch loses everything it had -- here, where
   * somebody had panned to and how far in they were.
   */
  setShown(shown: boolean): void {
    if (this.#shown === shown) return;
    this.#shown = shown;
    if (shown) {
      this.#dirty = true;
      this.#resume();
    } else {
      this.#pause();
    }
  }

  get drawing(): boolean {
    return this.#raf !== 0;
  }

  /** How far in the map is, for the chip that says so. */
  get scale(): number {
    return this.#scale;
  }

  /**
   * The middle of a box, in CSS pixels relative to the canvas.
   *
   * For .verify/harness-board.ts, which drives a drag with real pointer events
   * and has to aim them at something. The renderer is the only thing that
   * knows where a node has ended up on screen -- it owns the pan and the zoom
   * -- and a harness recomputing that would be the same arithmetic written a
   * second time and wrong the first time either changed.
   */
  screenPoint(id: string): { x: number; y: number } | null {
    const box = this.#map.byId.get(id);
    if (!box) return null;
    return {
      x: (box.x + box.width / 2) * this.#scale + this.#panX,
      y: (box.y + box.height / 2) * this.#scale + this.#panY,
    };
  }

  /**
   * Brings a box into view, if it is not already.
   *
   * Called when the caret moves to a line the map is not showing -- pressing
   * Down past the bottom of the screen has to follow the caret, or the editor
   * opens somewhere nobody can see and the next keystroke goes into a box that
   * is not on screen.
   */
  reveal(id: string): void {
    const box = this.#map.byId.get(id);
    if (!box) return;
    const ratio = window.devicePixelRatio || 1;
    const width = this.#canvas.width / ratio;
    const height = this.#canvas.height / ratio;
    if (width === 0 || height === 0) return;

    const margin = 24;
    const left = box.x * this.#scale + this.#panX;
    const top = box.y * this.#scale + this.#panY;
    const right = left + box.width * this.#scale;
    const bottom = top + box.height * this.#scale;

    if (left < margin) this.#panX += margin - left;
    else if (right > width - margin) this.#panX -= right - (width - margin);
    if (top < margin) this.#panY += margin - top;
    else if (bottom > height - margin) this.#panY -= bottom - (height - margin);
    this.#dirty = true;
  }

  /** The document moved. Lay it out again and repaint on the next frame. */
  invalidate(): void {
    this.#dirty = true;
    this.#stale = true;
  }

  /**
   * Back to 100%, and to the top left.
   *
   * Both together, deliberately. Zooming out from a corner of a large map and
   * then resetting only the scale leaves you looking at empty space with no
   * clue which way the document is, which is a worse place than where you
   * started.
   */
  reset(): void {
    this.#scale = 1;
    this.#home();
    this.#dirty = true;
    this.#onView({ scale: this.#scale });
  }

  /**
   * Puts the document where it can be seen.
   *
   * The layout says which point to look at -- the middle of the board -- and
   * this centres that point. A document smaller than the window is centred
   * whole instead, because centring a point inside something that already fits
   * just moves it off-centre.
   *
   * Then it is clamped, so a map larger than the window never shows blank
   * paper past its own edge.
   */
  #home(): void {
    const ratio = window.devicePixelRatio || 1;
    const width = this.#canvas.width / ratio;
    const height = this.#canvas.height / ratio;
    if (width === 0 || height === 0) {
      this.#panX = 0;
      this.#panY = 0;
      return;
    }
    const map = this.#map;
    const across = map.width * this.#scale;
    const down = map.height * this.#scale;
    this.#panX =
      across <= width
        ? (width - across) / 2
        : clamp(width / 2 - map.home.x * this.#scale, width - across, 0);
    this.#panY =
      down <= height
        ? (height - down) / 2
        : clamp(height / 2 - map.home.y * this.#scale, height - down, 0);
  }

  resize(): void {
    const ratio = window.devicePixelRatio || 1;
    const rect = this.#canvas.getBoundingClientRect();
    // A hidden canvas measures zero, and resizing to that throws away the
    // backing store for nothing. Keep whatever it last had.
    if (rect.width === 0 || rect.height === 0) return;
    this.#canvas.width = Math.round(rect.width * ratio);
    this.#canvas.height = Math.round(rect.height * ratio);
    // A resize is the one moment that already costs a measure, so the palette
    // is re-read here as well: it catches a canvas that was mounted before its
    // stylesheet finished loading, which is otherwise a map drawn once in the
    // fallback colours and never corrected.
    this.#palette = readPalette(this.#canvas);
    this.#dirty = true;
  }

  #pause(): void {
    if (!this.#raf) return;
    cancelAnimationFrame(this.#raf);
    this.#raf = 0;
  }

  #resume(): void {
    if (!this.#running || this.#raf || !this.#shown) return;
    this.#raf = requestAnimationFrame(this.#tick);
  }

  readonly #tick = () => {
    if (!this.#running) return;
    this.#raf = requestAnimationFrame(this.#tick);
    // A branch that is still gathering itself behind the hand is movement the
    // pointer is not producing, so it has to ask for its own frames. Checked
    // before the dirty test below, which would otherwise stop the animation
    // the moment somebody held still.
    if (this.#dragging && this.#settle()) this.#dirty = true;
    // Only when something moved. A map is still most of the time, and painting
    // an unchanged one sixty times a second is the office's old bug.
    if (!this.#dirty) return;
    this.#dirty = false;
    this.#paint();
  };

  #paint(): void {
    const scene = this.#scene();
    // Laid out again only when the document moved. A pan, a zoom and every
    // frame of a drag redraw the same arrangement: rebuilding it per frame
    // wraps every note's text, clusters the whole tree and relaxes it, which
    // on a board of a few hundred notes is milliseconds thrown away sixty
    // times a second.
    //
    // Two things say it moved: invalidate(), which is what the document's
    // owner calls, and a different `rows` array, which catches a caller that
    // changed the scene without saying so. The second is cheap enough to keep
    // as the backstop it is -- a stale board is a bug nobody can see the
    // cause of.
    if (this.#stale || this.#rows !== scene.rows) {
      this.#rows = scene.rows;
      this.#stale = false;
      this.#map = layout(scene.rows, this.#measure);
      this.#headDepth = shallowest(this.#map.boxes);
    }
    this.#land();
    // The first frame that has something to show is the one that decides where
    // the view starts. Before that there is nothing to centre on.
    if (!this.#homed && this.#map.boxes.length > 0) {
      this.#homed = true;
      this.#home();
    }

    const ctx = this.#ctx;
    const ratio = window.devicePixelRatio || 1;
    ctx.save();
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, this.#canvas.width / ratio, this.#canvas.height / ratio);
    // Pan is in screen pixels and zoom is about the map, so the translate goes
    // first: panning stays a one-to-one drag at every zoom, which is what a
    // hand on a piece of paper does.
    ctx.translate(this.#panX, this.#panY);
    ctx.scale(this.#scale, this.#scale);

    this.#visible = this.#view();

    this.#paintBoard(ctx);
    this.#paintRegions(ctx, scene);
    this.#paintBranches(ctx);
    this.#paintBoxes(ctx, scene);

    ctx.restore();
    this.painted++;
  }

  /**
   * The part of map space the canvas is showing.
   *
   * What everything below culls against. A board spreads out as soon as
   * anybody drags a note, so most of it is off screen most of the time, and
   * paper, a shadow, a tilt and four lines of text drawn outside the window
   * cost exactly as much as one drawn inside it.
   *
   * Worked out once per paint and held in `#visible`: pan, zoom and canvas
   * size do not move while a frame is being drawn.
   */
  #view(): { left: number; top: number; right: number; bottom: number } {
    const ratio = window.devicePixelRatio || 1;
    const left = -this.#panX / this.#scale;
    const top = -this.#panY / this.#scale;
    return {
      left,
      top,
      right: left + this.#canvas.width / ratio / this.#scale,
      bottom: top + this.#canvas.height / ratio / this.#scale,
    };
  }

  /**
   * The surface the notes lie on.
   *
   * Drawn for what is on screen rather than for the whole map: the extent of a
   * board is unbounded in practice, and filling all of it to show the tenth of
   * it somebody is looking at is work thrown away every frame of a pan.
   *
   * The dots are in map space, so they pan and zoom with the notes. A grid that
   * stayed in screen space would slide underneath them, and the board would
   * read as a window onto a surface rather than as the surface.
   */
  #paintBoard(ctx: CanvasRenderingContext2D): void {
    const ratio = window.devicePixelRatio || 1;
    const width = this.#canvas.width / ratio;
    const height = this.#canvas.height / ratio;
    if (width === 0 || height === 0) return;

    const left = -this.#panX / this.#scale;
    const top = -this.#panY / this.#scale;
    const right = left + width / this.#scale;
    const bottom = top + height / this.#scale;

    ctx.fillStyle = this.#palette.board;
    ctx.fillRect(left, top, right - left, bottom - top);

    if (GRID * this.#scale < GRID_FLOOR) return;
    const radius = 1.1 / this.#scale;
    ctx.fillStyle = this.#palette.grid;
    ctx.beginPath();
    for (let x = Math.floor(left / GRID) * GRID; x < right; x += GRID) {
      for (let y = Math.floor(top / GRID) * GRID; y < bottom; y += GRID) {
        ctx.moveTo(x + radius, y);
        ctx.arc(x, y, radius, 0, Math.PI * 2);
      }
    }
    ctx.fill();
  }

  #paintRegions(ctx: CanvasRenderingContext2D, scene: Scene): void {
    // Behind everything, because a region is the ground a set of lines sits on
    // rather than a thing in front of them.
    const grouped = new Map<string, { name: string; boxes: Box[] }>();
    for (const box of this.#map.boxes) {
      const region = scene.regionOf(box.id);
      if (!region) continue;
      const entry = grouped.get(region.id) ?? { name: region.name, boxes: [] };
      // Where the members are being drawn, so the ground stretches with a
      // branch somebody is dragging out of it. Drawn from the layout instead,
      // it stayed behind as an empty dashed box while the notes it was around
      // walked off -- a region is "these, together", and it cannot say that
      // from a position none of them is at any more.
      entry.boxes.push(this.#shifted(box.id) ?? box);
      grouped.set(region.id, entry);
    }

    for (const { name, boxes } of grouped.values()) {
      const area = hull(boxes, 18);
      if (!area) continue;
      /*
       * Drawn in the colour of what is in it, not in the one blue every region
       * used to share. With the notes themselves now coloured by branch, a
       * blue outline around a set of amber ones is a third thing on the board
       * claiming a meaning of its own -- where a region is only ever "these,
       * together", and the cheapest way to say that is to borrow their paper.
       *
       * A region whose members are spread across two branches takes the first
       * of them, which is honest: it looks like what it is.
       */
      const paint = paperOf(this.#palette, boxes[0].cluster, true);
      ctx.fillStyle = this.#palette.region;
      ctx.strokeStyle = paint.edge;
      ctx.lineWidth = 1;
      ctx.setLineDash([2, 5]);
      ctx.lineCap = "round";
      round(ctx, area.x, area.y, area.width, area.height, 18);
      ctx.fill();
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.lineCap = "butt";

      ctx.fillStyle = paint.text;
      ctx.font = "11px Inter, system-ui, sans-serif";
      ctx.textAlign = "left";
      ctx.textBaseline = "bottom";
      ctx.fillText(name || "unnamed", area.x + 10, area.y - 5);
    }
  }

  /**
   * The tree, as curves from one box's edge to the next one's.
   *
   * Curved rather than the elbows this used to draw. An elbow puts a corner at
   * every junction, and a column of forty of them turns into a grid of right
   * angles that reads as a circuit diagram -- the eye stops at each corner
   * instead of running along the line. A cubic with both handles horizontal
   * leaves the parent flat, arrives at the child flat, and has nothing in
   * between to stop at.
   *
   * Deeper branches are drawn a shade back, so the trunk is what the eye finds
   * first and the twigs are what it finds when it looks for them.
   */
  #paintBranches(ctx: CanvasRenderingContext2D): void {
    ctx.lineWidth = 1;
    const view = this.#visible;
    for (const branch of this.#map.branches) {
      const from = this.#shifted(branch.from);
      const to = this.#shifted(branch.to);
      if (!from || !to) continue;

      ctx.strokeStyle = to.depth >= 3 ? this.#palette.twig : this.#palette.branch;

      /*
       * On a board a child can be in any direction, so the curve leaves and
       * arrives through whichever side faces the other note. A fixed pair of
       * horizontal handles -- which is what this used to draw -- makes a branch
       * that goes straight up leave the right-hand edge, loop out and come
       * back, which reads as a link rather than as a parent.
       */
      const exit = faceOf(from, to);
      const enter = faceOf(to, from);
      const span = Math.hypot(enter.x - exit.x, enter.y - exit.y);
      const reach = Math.max(16, span / 2.4);

      // A cubic stays inside the box around its four control points, so that
      // box is what decides whether the curve is worth drawing. Two notes on
      // opposite sides of the window still get their branch drawn, which is
      // right: the line crosses what is on screen even though neither end is.
      const ax = exit.x + exit.ux * reach;
      const ay = exit.y + exit.uy * reach;
      const bx = enter.x + enter.ux * reach;
      const by = enter.y + enter.uy * reach;
      if (
        Math.max(exit.x, enter.x, ax, bx) < view.left ||
        Math.min(exit.x, enter.x, ax, bx) > view.right ||
        Math.max(exit.y, enter.y, ay, by) < view.top ||
        Math.min(exit.y, enter.y, ay, by) > view.bottom
      ) {
        continue;
      }

      ctx.beginPath();
      ctx.moveTo(exit.x, exit.y);
      ctx.bezierCurveTo(ax, ay, bx, by, enter.x, enter.y);
      ctx.stroke();
      // Pointing the way the curve arrived, which is the way the tree runs.
      arrow(ctx, enter.x, enter.y, -enter.ux, -enter.uy, ctx.strokeStyle as string);
    }
  }

  #paintBoxes(ctx: CanvasRenderingContext2D, scene: Scene): void {
    const focused = scene.focused();
    const drag = this.#dragging;

    // The branch in hand goes last, so it passes over the board rather than
    // under it. Two passes rather than a sort: the order of everything else is
    // the layout's, and re-sorting the whole map on every frame of a drag to
    // move three boxes to the end would be paying for it per frame.
    const held = (box: Box) => drag !== null && (box.id === drag.id || drag.under.has(box.id));
    for (const box of this.#map.boxes) {
      if (held(box)) continue;
      this.#paintOne(ctx, scene, box, focused);
    }
    if (!drag) return;
    for (const box of this.#map.boxes) {
      if (held(box) && box.id !== drag.id) this.#paintOne(ctx, scene, box, focused);
    }
    const top = this.#map.byId.get(drag.id);
    if (top) this.#paintOne(ctx, scene, top, focused);
  }

  #paintOne(ctx: CanvasRenderingContext2D, scene: Scene, box: Box, focused: string | null): void {
    // Where the drag has put it, which is the one place on this canvas a
    // position is not a function of the tree -- and it stops being one the
    // moment it is dropped, because the drop is a move rather than a
    // placement.
    const at = this.#offsetOf(box.id);
    const x = box.x + at.x;
    const y = box.y + at.y;

    // Off the window, so nothing about it is worth drawing. The margin is the
    // room the shadow, the lift and the focus ring take outside the box
    // itself, so a note just past the edge still throws its shadow into view.
    const view = this.#visible;
    const margin = LIFT_BLUR;
    if (
      x + box.width < view.left - margin ||
      x > view.right + margin ||
      y + box.height < view.top - margin ||
      y > view.bottom + margin
    ) {
      return;
    }

    this.#paintNote(ctx, scene, box, x, y, box.id === focused);
  }

  /**
   * One note, on paper, pinned at its own angle.
   *
   * Everything inside is drawn about the note's centre with the rotation
   * already applied, so nothing below has to know the note is tilted.
   */
  #paintNote(
    ctx: CanvasRenderingContext2D,
    scene: Scene,
    box: Box,
    x: number,
    y: number,
    isFocused: boolean,
  ): void {
    const metrics = tier(box.depth);
    const head = box.depth === this.#headDepth;
    const paint = paperOf(this.#palette, box.cluster, head);

    // Picked up, and drawn like it: a longer shadow and a shade larger. The
    // scale goes on before anything is drawn, including the focus ring -- a
    // ring at one size around paper at another is a note with a halo that has
    // come loose.
    const lifted = this.#dragging?.id === box.id;

    ctx.save();
    ctx.translate(x + box.width / 2, y + box.height / 2);
    ctx.rotate(box.angle);
    if (lifted) ctx.scale(LIFT_SCALE, LIFT_SCALE);
    const left = -box.width / 2;
    const top = -box.height / 2;

    // The focus ring first and underneath, as a second rounded rect rather
    // than a shadow: canvas shadows are blurred on every draw and this is a
    // flat ring, which is cheaper and is what the design draws.
    if (isFocused) {
      ctx.fillStyle = this.#palette.focusGlow;
      round(ctx, left - 3, top - 3, box.width + 6, box.height + 6, metrics.radius + 3);
      ctx.fill();
    }

    // The paper, with the shadow that makes it paper. Turned off again before
    // the edge and the text, or every stroke is drawn twice -- once as itself
    // and once as a blur under whatever comes next.
    ctx.save();
    ctx.shadowColor = SHADOW;
    ctx.shadowBlur = lifted ? LIFT_BLUR : head ? 16 : 10;
    ctx.shadowOffsetY = lifted ? LIFT_OFFSET : head ? 5 : 3;
    ctx.fillStyle = isFocused ? this.#palette.focusFill : paint.fill;
    round(ctx, left, top, box.width, box.height, metrics.radius);
    ctx.fill();
    ctx.restore();

    ctx.strokeStyle = isFocused ? this.#palette.focusEdge : paint.edge;
    ctx.lineWidth = isFocused ? 1.5 : 1;
    round(ctx, left, top, box.width, box.height, metrics.radius);
    ctx.stroke();

    if (head) this.#paintTape(ctx, top);

    let at = top + TEXT_PAD_Y;
    if (box.icon !== "") {
      this.#paintIcon(ctx, box.icon, 0, at + ICON_SIZE / 2, paint.text);
      // The same reservation layout.ts made when it sized this note. If the two
      // ever disagree the text runs off the paper, which is why ICON_ROOM is
      // one constant imported here rather than two that happen to match.
      at += ICON_ROOM;
    }

    const leading = sizeOf(metrics.font) * LINE_HEIGHT;
    ctx.font = metrics.font;
    ctx.textAlign = head ? "center" : "left";
    ctx.textBaseline = "top";
    if (box.lines.length === 0) {
      ctx.fillStyle = this.#palette.muted;
      ctx.fillText("(empty line)", head ? 0 : left + TEXT_PAD_X, at);
    } else {
      ctx.fillStyle = paint.text;
      for (const line of box.lines) {
        ctx.fillText(line, head ? 0 : left + TEXT_PAD_X, at);
        at += leading;
      }
    }
    ctx.textAlign = "left";

    // A line that has been broken into work, said in the corner rather than on
    // the line: it is a fact about the line's history, not part of what it
    // says. Bottom right because the text now wraps and there is no longer a
    // spare half of a single row to put it in.
    const tasks = scene.tasksOf(box.id);
    if (tasks > 0) {
      ctx.font = "10.5px Inter, system-ui, sans-serif";
      ctx.fillStyle = this.#palette.count;
      ctx.textAlign = "right";
      ctx.textBaseline = "bottom";
      ctx.fillText(
        `${tasks} ${tasks === 1 ? "task" : "tasks"}`,
        left + box.width - 8,
        top + box.height - 5,
      );
      ctx.textAlign = "left";
    }

    ctx.restore();
  }

  /**
   * Steps every follower toward where the hand is, and says whether any of
   * them is still moving.
   *
   * The dragged note itself is not eased -- it is under the pointer and has to
   * be exactly there, or the whole thing feels like lag rather than weight.
   * Everything below it closes a share of the remaining distance each frame,
   * and the share gets smaller with every generation, which is what makes a
   * deep branch stream out behind rather than move as a slab.
   */
  #settle(): boolean {
    const drag = this.#dragging;
    if (!drag) return false;
    const held = this.#map.byId.get(drag.id);
    if (!held) {
      // The node went while it was being held -- deleted elsewhere, or the
      // tree changed under it. Nothing to carry and nowhere to put it back.
      this.#dragging = null;
      return false;
    }

    // Following the hand. There is nowhere else to go: a drop writes where
    // everything landed, so the layout the next frame is built puts them
    // there rather than back.
    const targetX = drag.x - drag.dx - held.x;
    const targetY = drag.y - drag.dy - held.y;

    let moving = false;
    const ease = (id: string, k: number) => {
      const at = drag.trail.get(id) ?? { x: 0, y: 0 };
      at.x += (targetX - at.x) * k;
      at.y += (targetY - at.y) * k;
      if (Math.abs(targetX - at.x) > STICK_REST || Math.abs(targetY - at.y) > STICK_REST) {
        moving = true;
      } else {
        // Snapped the last fraction of a pixel, so a branch that has caught up
        // stops asking for frames instead of creeping forever.
        at.x = targetX;
        at.y = targetY;
      }
      drag.trail.set(id, at);
    };

    // The held note is never eased: under the hand it is exactly where the hand
    // is, or the whole thing feels like lag rather than weight. Only what it
    // is carrying trails.
    for (const [id, generation] of drag.under) {
      ease(id, STICK * STICK_FALLOFF ** (generation - 1));
    }

    return moving;
  }

  /**
   * Lets go of a drop's offsets once the layout agrees with them.
   *
   * Agreement is the card having moved at all: the write is rounded and the
   * whole board is shifted into positive space around it, so the laid-out
   * position is near where the drop asked for rather than exactly on it, and
   * comparing against the target would hold the offsets forever.
   *
   * The frame count is the way out when the write did not take -- a refused
   * op, a card deleted from another machine mid-drag. Half a second of a card
   * sitting where it was dropped and then going back is a worse answer than
   * the flash this exists to remove, but it is an answer, and it happens to
   * nobody in the ordinary case.
   */
  #land(): void {
    const landing = this.#landing;
    if (!landing) return;
    const box = this.#map.byId.get(landing.id);
    if (!box) {
      this.#landing = null;
      return;
    }
    const moved = Math.abs(box.x - landing.from.x) > 0.5 || Math.abs(box.y - landing.from.y) > 0.5;
    if (moved || ++landing.frames > LANDING_FRAMES) this.#landing = null;
  }

  /**
   * A box where it is being drawn, offset and all, or null if it is not on the
   * map.
   *
   * A copy rather than a mutation: the layout is rebuilt from the tree on
   * every paint and moving its boxes would be editing something the next frame
   * throws away, which is the kind of thing that works until somebody caches
   * the layout.
   */
  #shifted(id: string): Box | null {
    const box = this.#map.byId.get(id);
    if (!box) return null;
    const at = this.#offsetOf(id);
    if (at.x === 0 && at.y === 0) return box;
    return { ...box, x: box.x + at.x, y: box.y + at.y };
  }

  /**
   * Where a node is drawn relative to where the layout put it.
   *
   * Zero for everything but the branch in hand. Asked by the boxes, the
   * branches and the links alike, because a curve drawn to a box's laid-out
   * place while the box is somewhere else is a line hanging in space -- which
   * is most of what made dragging look wrong.
   */
  #offsetOf(id: string): { x: number; y: number } {
    const drag = this.#dragging;
    if (!drag) return this.#landing?.offsets.get(id) ?? NO_OFFSET;
    if (id === drag.id) {
      const held = this.#map.byId.get(id);
      if (!held) return NO_OFFSET;
      return { x: drag.x - drag.dx - held.x, y: drag.y - drag.dy - held.y };
    }
    return drag.trail.get(id) ?? NO_OFFSET;
  }

  /** The strip across the top of a cluster head. */
  #paintTape(ctx: CanvasRenderingContext2D, top: number): void {
    ctx.save();
    ctx.translate(0, top);
    ctx.rotate(-0.055);
    ctx.fillStyle = this.#palette.tape;
    ctx.strokeStyle = this.#palette.tapeEdge;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.rect(-TAPE_WIDTH / 2, -TAPE_HEIGHT / 2, TAPE_WIDTH, TAPE_HEIGHT);
    ctx.fill();
    ctx.stroke();
    ctx.restore();
  }

  /**
   * One glyph, centred on a point.
   *
   * The line width is divided back out of the scale, so a glyph is the same
   * weight as the text beside it rather than a fraction of it.
   */
  #paintIcon(
    ctx: CanvasRenderingContext2D,
    name: string,
    cx: number,
    cy: number,
    colour: string,
  ): void {
    const path = iconPath(name);
    if (!path) return;
    const factor = ICON_SIZE / ICON_BOX;
    ctx.save();
    ctx.translate(cx - ICON_SIZE / 2, cy - ICON_SIZE / 2);
    ctx.scale(factor, factor);
    ctx.strokeStyle = colour;
    ctx.lineWidth = 1.6 / factor;
    ctx.lineJoin = "round";
    ctx.lineCap = "round";
    ctx.stroke(path);
    ctx.restore();
  }

  /* -- pointer ------------------------------------------------------------ */

  #at(event: PointerEvent): { x: number; y: number } {
    const rect = this.#canvas.getBoundingClientRect();
    return {
      x: (event.clientX - rect.left - this.#panX) / this.#scale,
      y: (event.clientY - rect.top - this.#panY) / this.#scale,
    };
  }

  readonly #down = (event: PointerEvent) => {
    const at = this.#at(event);
    const box = boxAt(this.#map, at.x, at.y);
    // Capture is a convenience, not a requirement: it keeps a drag alive when
    // the pointer leaves the canvas. It throws for a pointer the browser does
    // not consider active, and a drag that cannot be captured is still a drag.
    try {
      this.#canvas.setPointerCapture(event.pointerId);
    } catch {
      // Nothing to do, and nothing lost.
    }
    this.#from = { x: event.clientX, y: event.clientY };
    if (box && !this.#readonly) {
      this.#dragging = {
        id: box.id,
        dx: at.x - box.x,
        dy: at.y - box.y,
        x: at.x,
        y: at.y,
        under: descendantsOf(this.#map, box.id),
        trail: new Map(),
      };
    } else {
      this.#panning = { x: event.clientX - this.#panX, y: event.clientY - this.#panY };
    }
    this.#dirty = true;
  };

  readonly #move = (event: PointerEvent) => {
    if (this.#dragging) {
      const at = this.#at(event);
      this.#dragging.x = at.x;
      this.#dragging.y = at.y;
      // Nothing is highlighted underneath. A drop lands where the hand is
      // rather than on something, so a target would be a promise about a
      // gesture this no longer makes.
      this.#dirty = true;
      return;
    }
    if (this.#panning) {
      this.#panX = event.clientX - this.#panning.x;
      this.#panY = event.clientY - this.#panning.y;
      this.#dirty = true;
    }
  };

  readonly #up = (event: PointerEvent) => {
    try {
      if (this.#canvas.hasPointerCapture(event.pointerId)) {
        this.#canvas.releasePointerCapture(event.pointerId);
      }
    } catch {
      // See #down: never captured, nothing to release.
    }
    const dragging = this.#dragging;
    const from = this.#from;
    this.#from = null;
    const still =
      from !== null &&
      Math.abs(event.clientX - from.x) < CLICK_SLOP &&
      Math.abs(event.clientY - from.y) < CLICK_SLOP;

    this.#panning = null;
    this.#dirty = true;

    // A press that went nowhere is a click: on a box it opens the editor, on
    // the background it closes it. Checked before the drop below, because a
    // click on a box is also a drag of zero distance onto itself.
    if (still) {
      this.#dragging = null;
      const at = this.#at(event);
      const box = boxAt(this.#map, at.x, at.y);
      this.#onPick(box?.id ?? null);
      return;
    }
    if (!dragging) {
      this.#dragging = null;
      return;
    }

    /*
     * A drop is where everything that moved ended up.
     *
     * The card in hand is at the hand, and everything under it has travelled
     * the same distance -- the trail is a lag on the way there, not a
     * different destination, so the whole branch is written down at one
     * offset. Reported in map space, which is the space the layout works in
     * and the space the fields are read back out of.
     *
     * Nothing is reported for a card that did not move. A drag of half a pixel
     * is the pointer shaking, and writing an op for it would put a card in the
     * log every time somebody rested a hand on the board.
     */
    this.#dragging = null;
    const held = this.#map.byId.get(dragging.id);
    if (!held) return;
    const dx = dragging.x - dragging.dx - held.x;
    const dy = dragging.y - dragging.dy - held.y;
    if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;

    const moves = [{ id: dragging.id, x: held.x + dx, y: held.y + dy }];
    for (const id of dragging.under.keys()) {
      const box = this.#map.byId.get(id);
      if (box) moves.push({ id, x: box.x + dx, y: box.y + dy });
    }

    // Everything in the branch by the same distance, which is what was
    // written down -- so the followers finish their lag here rather than
    // easing on into a layout that has already answered. See #landing.
    const offsets = new Map(moves.map((move) => [move.id, { x: dx, y: dy }]));
    this.#landing = { id: dragging.id, from: { x: held.x, y: held.y }, offsets, frames: 0 };

    this.#onDrop({ moves });
  };

  /**
   * Zoom, about the pointer.
   *
   * About the pointer rather than the centre because zooming in on a map is
   * always zooming in on *something*, and that something is under the cursor.
   * A centre-anchored zoom sends whatever you were looking at off the edge and
   * makes the next gesture a pan to find it again.
   */
  readonly #wheel = (event: WheelEvent) => {
    event.preventDefault();
    const rect = this.#canvas.getBoundingClientRect();
    const px = event.clientX - rect.left;
    const py = event.clientY - rect.top;

    const next = clamp(this.#scale * Math.exp(-event.deltaY / 400), MIN_SCALE, MAX_SCALE);
    if (next === this.#scale) return;
    // Hold the map point under the pointer still: solve for the pan that keeps
    // (px - pan) / scale equal on both sides of the change.
    this.#panX = px - ((px - this.#panX) / this.#scale) * next;
    this.#panY = py - ((py - this.#panY) / this.#scale) * next;
    this.#scale = next;
    this.#dirty = true;
    this.#onView({ scale: next });
  };
}

/** Nothing is being dragged, so nothing has moved. Shared, never written to. */
const NO_OFFSET = { x: 0, y: 0 } as const;

/**
 * Everything under a node, and how many generations down each one is.
 *
 * Read off the branches the layout produced rather than the document, because
 * the branches are what is on screen: a folded branch has no boxes and nothing
 * to carry, and a node whose parent is off this map is not under anything here.
 *
 * Breadth-first, so the generation count is the real one rather than whichever
 * path a depth-first walk happened to arrive by.
 */
function descendantsOf(map: Layout, root: string): Map<string, number> {
  const children = new Map<string, string[]>();
  for (const branch of map.branches) {
    const list = children.get(branch.from);
    if (list) list.push(branch.to);
    else children.set(branch.from, [branch.to]);
  }

  const out = new Map<string, number>();
  let front = children.get(root) ?? [];
  for (let generation = 1; front.length > 0; generation++) {
    const next: string[] = [];
    for (const id of front) {
      // A tree cannot cycle and this map is built from one -- but it is built
      // from data that arrived over a network, and a walk that can loop
      // forever is not worth the line it saves.
      if (out.has(id) || id === root) continue;
      out.set(id, generation);
      next.push(...(children.get(id) ?? []));
    }
    front = next;
  }
  return out;
}

/**
 * The depth a cluster head sits at.
 *
 * The shallowest box that belongs to a branch at all. With a subject in the
 * middle that is 1, without one it is 0, and asking the layout is cheaper than
 * threading the answer out of it.
 */
function shallowest(boxes: readonly Box[]): number {
  let found = -1;
  for (const box of boxes) {
    if (box.cluster < 0) continue;
    if (found < 0 || box.depth < found) found = box.depth;
  }
  return found < 0 ? 0 : found;
}

/**
 * The point on `from` that faces `to`, and the way out of it.
 *
 * The side, not the corner: a curve that leaves a note's corner looks like it
 * missed. Which side is decided by the larger of the two gaps, so a note almost
 * directly above another leaves through the top rather than through whichever
 * edge happens to be a pixel closer.
 */
function faceOf(from: Box, to: Box): { x: number; y: number; ux: number; uy: number } {
  const fx = from.x + from.width / 2;
  const fy = from.y + from.height / 2;
  const dx = to.x + to.width / 2 - fx;
  const dy = to.y + to.height / 2 - fy;
  if (Math.abs(dx) >= Math.abs(dy)) {
    const right = dx > 0;
    return { x: right ? from.x + from.width : from.x, y: fy, ux: right ? 1 : -1, uy: 0 };
  }
  const down = dy > 0;
  return { x: fx, y: down ? from.y + from.height : from.y, ux: 0, uy: down ? 1 : -1 };
}

/** A small filled head at the end of a branch, pointing along (ux, uy). */
function arrow(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  ux: number,
  uy: number,
  colour: string,
): void {
  // The perpendicular, for the two back corners.
  const px = -uy;
  const py = ux;
  ctx.fillStyle = colour;
  ctx.beginPath();
  ctx.moveTo(x, y);
  ctx.lineTo(x - ux * ARROW + px * ARROW * 0.45, y - uy * ARROW + py * ARROW * 0.45);
  ctx.lineTo(x - ux * ARROW - px * ARROW * 0.45, y - uy * ARROW - py * ARROW * 0.45);
  ctx.closePath();
  ctx.fill();
}

function clamp(value: number, low: number, high: number): number {
  return Math.min(Math.max(value, low), high);
}

function round(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  width: number,
  height: number,
  radius: number,
): void {
  ctx.beginPath();
  ctx.moveTo(x + radius, y);
  ctx.arcTo(x + width, y, x + width, y + height, radius);
  ctx.arcTo(x + width, y + height, x, y + height, radius);
  ctx.arcTo(x, y + height, x, y, radius);
  ctx.arcTo(x, y, x + width, y, radius);
  ctx.closePath();
}
