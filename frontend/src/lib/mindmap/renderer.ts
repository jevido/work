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
import { DARK, readPalette, type Palette } from "./palette";
import {
  boxAt,
  hull,
  layout,
  tier,
  type Box,
  type Layout,
  type LayoutKind,
  type MapRow,
} from "./layout";

/** What the renderer needs to know, fetched fresh each frame it repaints. */
export interface Scene {
  rows: readonly MapRow[];
  /** Links touching a node, both directions, as the workspace reports them. */
  linksOf(id: string): { edge: string; other: string; text: string; dangling: boolean }[];
  /** The region a node is in, or null. */
  regionOf(id: string): { id: string; name: string } | null;
  /** How many tasks were extracted from a line. Zero for most of them. */
  tasksOf(id: string): number;
  /** The line the caret is on, drawn as selected. */
  focused(): string | null;
  /** Which shape to draw the same tree in. */
  shape(): LayoutKind;
}

/** What a drag resolved to, for the caller to turn into an op. */
export interface Drop {
  node: string;
  /** The node to put it under, or "" for the top level. */
  onto: string;
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

  /** Pan, in device-independent pixels, and the zoom it is applied under. */
  #panX = 0;
  #panY = 0;
  #scale = 1;

  #map: Layout = { boxes: [], byId: new Map(), branches: [], width: 0, height: 0 };
  /** The shape the current #map was built in, so a change to it repaints. */
  #shape: LayoutKind = "tidy";

  /** What is being dragged, and where the pointer is over it. */
  #dragging: { id: string; dx: number; dy: number; x: number; y: number } | null = null;
  /** Panning the background rather than moving a node. */
  #panning: { x: number; y: number } | null = null;
  /** The box a drop would land on, for a hint before it happens. */
  #over: string | null = null;

  /** Frames painted, which is what a test counts. */
  painted = 0;

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
   * Where a box is on screen, in CSS pixels relative to the canvas.
   *
   * What the editor is positioned with. The map's own coordinates mean nothing
   * to an element in the DOM, and the two are related by a pan and a zoom that
   * only this class knows about -- so it is asked rather than recomputed
   * outside, where it would be the same arithmetic written a second time and
   * wrong the first time either of them changed.
   */
  screenOf(id: string): { x: number; y: number; width: number; height: number } | null {
    const box = this.#map.byId.get(id);
    if (!box) return null;
    return {
      x: box.x * this.#scale + this.#panX,
      y: box.y * this.#scale + this.#panY,
      width: box.width * this.#scale,
      height: box.height * this.#scale,
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

  /** The document moved. Repaint on the next frame. */
  invalidate(): void {
    this.#dirty = true;
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
    this.#panX = 0;
    this.#panY = 0;
    this.#dirty = true;
    this.#onView({ scale: this.#scale });
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
    // Only when something moved. A map is still most of the time, and painting
    // an unchanged one sixty times a second is the office's old bug.
    if (!this.#dirty) return;
    this.#dirty = false;
    this.#paint();
  };

  #paint(): void {
    const scene = this.#scene();
    this.#shape = scene.shape();
    this.#map = layout(scene.rows, this.#shape);

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

    this.#paintRegions(ctx, scene);
    this.#paintBranches(ctx);
    this.#paintLinks(ctx, scene);
    this.#paintBoxes(ctx, scene);

    ctx.restore();
    this.painted++;
  }

  #paintRegions(ctx: CanvasRenderingContext2D, scene: Scene): void {
    // Behind everything, because a region is the ground a set of lines sits on
    // rather than a thing in front of them.
    const grouped = new Map<string, { name: string; boxes: Box[] }>();
    for (const box of this.#map.boxes) {
      const region = scene.regionOf(box.id);
      if (!region) continue;
      const entry = grouped.get(region.id) ?? { name: region.name, boxes: [] };
      entry.boxes.push(box);
      grouped.set(region.id, entry);
    }

    for (const { name, boxes } of grouped.values()) {
      const area = hull(boxes);
      if (!area) continue;
      ctx.fillStyle = this.#palette.region;
      ctx.strokeStyle = this.#palette.regionEdge;
      ctx.lineWidth = 1;
      ctx.setLineDash([5, 4]);
      round(ctx, area.x, area.y, area.width, area.height, 10);
      ctx.fill();
      ctx.stroke();
      ctx.setLineDash([]);

      ctx.fillStyle = this.#palette.muted;
      ctx.font = "11px Inter, system-ui, sans-serif";
      ctx.textBaseline = "bottom";
      ctx.fillText(`Region · ${name || "unnamed"}`, area.x + 8, area.y - 4);
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
    for (const branch of this.#map.branches) {
      const from = this.#map.byId.get(branch.from);
      const to = this.#map.byId.get(branch.to);
      if (!from || !to) continue;

      ctx.strokeStyle = to.depth >= 3 ? this.#palette.twig : this.#palette.branch;
      const ax = from.x + from.width;
      const ay = from.y + from.height / 2;
      const bx = to.x;
      const by = to.y + to.height / 2;
      // Half the horizontal distance on each side. Handles that are a fixed
      // length make a short hop look kinked and a long one look slack; a
      // proportion of the gap looks the same at every span.
      const reach = Math.max(12, (bx - ax) / 2);
      ctx.beginPath();
      ctx.moveTo(ax, ay);
      ctx.bezierCurveTo(ax + reach, ay, bx - reach, by, bx, by);
      ctx.stroke();
    }
  }

  #paintLinks(ctx: CanvasRenderingContext2D, scene: Scene): void {
    // The reason this view exists. A link is the one thing the outline cannot
    // show as a shape, and the canvas can.
    const drawn = new Set<string>();
    ctx.lineWidth = 1.5;
    for (const box of this.#map.boxes) {
      for (const link of scene.linksOf(box.id)) {
        if (drawn.has(link.edge)) continue;
        drawn.add(link.edge);
        const other = this.#map.byId.get(link.other);
        if (!other) continue;

        ctx.strokeStyle = link.dangling ? this.#palette.dangling : this.#palette.link;
        ctx.setLineDash(link.dangling ? [3, 4] : [6, 4]);
        ctx.beginPath();
        const ax = box.x + box.width;
        const ay = box.y + box.height / 2;
        const bx = other.x + other.width;
        const by = other.y + other.height / 2;
        const bow = Math.min(120, 30 + Math.abs(by - ay) / 3);
        ctx.moveTo(ax, ay);
        ctx.bezierCurveTo(ax + bow, ay, bx + bow, by, bx, by);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }
  }

  #paintBoxes(ctx: CanvasRenderingContext2D, scene: Scene): void {
    const focused = scene.focused();
    ctx.textBaseline = "middle";

    for (const box of this.#map.boxes) {
      const dragged = this.#dragging?.id === box.id;
      const x = dragged ? this.#dragging!.x - this.#dragging!.dx : box.x;
      const y = dragged ? this.#dragging!.y - this.#dragging!.dy : box.y;
      const shape = tier(box.depth);
      const paint = this.#palette.tiers[Math.min(box.depth, this.#palette.tiers.length - 1)];
      const isFocused = box.id === focused;

      // The glow first and underneath, as a second rounded rect rather than a
      // shadow: canvas shadows are blurred on every draw and this is a flat
      // ring, which is cheaper and is what the design draws.
      if (isFocused) {
        ctx.fillStyle = this.#palette.focusGlow;
        round(ctx, x - 3, y - 3, box.width + 6, box.height + 6, shape.radius + 3);
        ctx.fill();
      }

      ctx.fillStyle = this.#over === box.id ? this.#palette.dropFill : isFocused ? this.#palette.focusFill : paint.fill;
      ctx.strokeStyle = isFocused ? this.#palette.focusEdge : paint.edge;
      ctx.lineWidth = isFocused ? 1.5 : 1;
      round(ctx, x, y, box.width, box.height, shape.radius);
      ctx.fill();
      ctx.stroke();

      // The task count, right-aligned, and measured before the text is clipped
      // so the two never overlap. A line that has been broken into work is the
      // one fact about a line the map can show that the outline cannot fit.
      const tasks = scene.tasksOf(box.id);
      let room = box.width - 20;
      if (tasks > 0) {
        const label = `${tasks} ${tasks === 1 ? "task" : "tasks"}`;
        ctx.font = "10.5px Inter, system-ui, sans-serif";
        ctx.fillStyle = this.#palette.count;
        ctx.textAlign = "right";
        ctx.fillText(label, x + box.width - 10, y + box.height / 2);
        ctx.textAlign = "left";
        room -= ctx.measureText(label).width + 8;
      }

      ctx.font = shape.font;
      ctx.fillStyle = paint.text;
      ctx.save();
      ctx.beginPath();
      ctx.rect(x + 10, y, Math.max(room, 10), box.height);
      ctx.clip();
      ctx.fillText(box.text.trim() || "(empty line)", x + 10, y + box.height / 2);
      ctx.restore();
    }
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
      this.#dragging = { id: box.id, dx: at.x - box.x, dy: at.y - box.y, x: at.x, y: at.y };
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
      const under = boxAt(this.#map, at.x, at.y);
      this.#over = under && under.id !== this.#dragging.id ? under.id : null;
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

    this.#dragging = null;
    this.#panning = null;
    this.#dirty = true;

    // A press that went nowhere is a click: on a box it opens the editor, on
    // the background it closes it. Checked before the drop below, because a
    // click on a box is also a drag of zero distance onto itself.
    if (still) {
      this.#over = null;
      const at = this.#at(event);
      const box = boxAt(this.#map, at.x, at.y);
      this.#onPick(box?.id ?? null);
      return;
    }
    if (!dragging) return;

    // A drop is a move-node and nothing else. The map has no coordinates to
    // write: dropping onto a line puts this one under it, and dropping on the
    // background puts it at the top level. Anything else would mean storing a
    // position, which is a second thing to merge.
    const onto = this.#over;
    this.#over = null;
    if (onto === dragging.id) return;
    this.#onDrop({ node: dragging.id, onto: onto ?? "" });
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
