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
import { boxAt, hull, layout, type Box, type Layout } from "./layout";
import type { Row } from "../workspace/model";

/** What the renderer needs to know, fetched fresh each frame it repaints. */
export interface Scene {
  rows: readonly Row[];
  /** Links touching a node, both directions, as the workspace reports them. */
  linksOf(id: string): { edge: string; other: string; text: string; dangling: boolean }[];
  /** The region a node is in, or null. */
  regionOf(id: string): { id: string; name: string } | null;
  /** The line the caret is on, drawn as selected. */
  focused(): string | null;
}

/** What a drag resolved to, for the caller to turn into an op. */
export interface Drop {
  node: string;
  /** The node to put it under, or "" for the top level. */
  onto: string;
}

const COLOURS = {
  branch: "rgba(148, 163, 184, 0.55)",
  link: "rgba(96, 165, 250, 0.85)",
  dangling: "rgba(148, 163, 184, 0.4)",
  region: "rgba(96, 165, 250, 0.10)",
  regionEdge: "rgba(96, 165, 250, 0.35)",
  box: "#1e2430",
  boxEdge: "rgba(148, 163, 184, 0.35)",
  boxFocused: "rgba(96, 165, 250, 0.9)",
  boxDragging: "rgba(96, 165, 250, 0.25)",
  text: "#e6e9ef",
  muted: "rgba(230, 233, 239, 0.6)",
};

export class MindmapRenderer {
  #canvas: HTMLCanvasElement;
  #ctx: CanvasRenderingContext2D;
  #scene: () => Scene;
  #onDrop: (drop: Drop) => void;

  #raf = 0;
  #running = false;
  #shown = true;
  /** Set when something changed and the next frame must actually paint. */
  #dirty = true;

  /** Pan, in device-independent pixels. */
  #panX = 0;
  #panY = 0;

  #map: Layout = { boxes: [], byId: new Map(), branches: [], width: 0, height: 0 };

  /** What is being dragged, and where the pointer is over it. */
  #dragging: { id: string; dx: number; dy: number; x: number; y: number } | null = null;
  /** Panning the background rather than moving a node. */
  #panning: { x: number; y: number } | null = null;
  /** The box a drop would land on, for a hint before it happens. */
  #over: string | null = null;

  /** Frames painted, which is what a test counts. */
  painted = 0;

  constructor(
    canvas: HTMLCanvasElement,
    scene: () => Scene,
    onDrop: (drop: Drop) => void,
  ) {
    this.#canvas = canvas;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("mindmap: no 2d context");
    this.#ctx = ctx;

    this.#scene = scene;
    this.#onDrop = onDrop;

    canvas.addEventListener("pointerdown", this.#down);
    canvas.addEventListener("pointermove", this.#move);
    canvas.addEventListener("pointerup", this.#up);
    canvas.addEventListener("pointercancel", this.#up);
  }

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
  }

  /**
   * Hidden, it stops drawing. Shown, it picks up where it was.
   *
   * The same rule the office follows, and for the same reason: a canvas torn
   * down and rebuilt on a mode switch loses everything it had -- here, where
   * somebody had panned to.
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

  /** The document moved. Repaint on the next frame. */
  invalidate(): void {
    this.#dirty = true;
  }

  resize(): void {
    const ratio = window.devicePixelRatio || 1;
    const rect = this.#canvas.getBoundingClientRect();
    // A hidden canvas measures zero, and resizing to that throws away the
    // backing store for nothing. Keep whatever it last had.
    if (rect.width === 0 || rect.height === 0) return;
    this.#canvas.width = Math.round(rect.width * ratio);
    this.#canvas.height = Math.round(rect.height * ratio);
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
    this.#map = layout(scene.rows);

    const ctx = this.#ctx;
    const ratio = window.devicePixelRatio || 1;
    ctx.save();
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, this.#canvas.width / ratio, this.#canvas.height / ratio);
    ctx.translate(this.#panX, this.#panY);

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
      ctx.fillStyle = COLOURS.region;
      ctx.strokeStyle = COLOURS.regionEdge;
      ctx.lineWidth = 1;
      round(ctx, area.x, area.y, area.width, area.height, 10);
      ctx.fill();
      ctx.stroke();

      ctx.fillStyle = COLOURS.muted;
      ctx.font = "11px system-ui, sans-serif";
      ctx.textBaseline = "bottom";
      ctx.fillText(name || "Unnamed region", area.x + 8, area.y - 2);
    }
  }

  #paintBranches(ctx: CanvasRenderingContext2D): void {
    ctx.strokeStyle = COLOURS.branch;
    ctx.lineWidth = 1;
    for (const branch of this.#map.branches) {
      const from = this.#map.byId.get(branch.from);
      const to = this.#map.byId.get(branch.to);
      if (!from || !to) continue;
      ctx.beginPath();
      ctx.moveTo(from.x + 12, from.y + from.height);
      ctx.lineTo(from.x + 12, to.y + to.height / 2);
      ctx.lineTo(to.x, to.y + to.height / 2);
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

        ctx.strokeStyle = link.dangling ? COLOURS.dangling : COLOURS.link;
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
    ctx.font = "12px system-ui, sans-serif";
    ctx.textBaseline = "middle";

    for (const box of this.#map.boxes) {
      const dragged = this.#dragging?.id === box.id;
      const x = dragged ? this.#dragging!.x - this.#dragging!.dx : box.x;
      const y = dragged ? this.#dragging!.y - this.#dragging!.dy : box.y;

      ctx.fillStyle = this.#over === box.id ? COLOURS.boxDragging : COLOURS.box;
      ctx.strokeStyle = box.id === focused ? COLOURS.boxFocused : COLOURS.boxEdge;
      ctx.lineWidth = box.id === focused ? 1.5 : 1;
      round(ctx, x, y, box.width, box.height, 6);
      ctx.fill();
      ctx.stroke();

      ctx.fillStyle = COLOURS.text;
      ctx.save();
      ctx.beginPath();
      ctx.rect(x + 8, y, box.width - 16, box.height);
      ctx.clip();
      ctx.fillText(box.text.trim() || "(empty line)", x + 8, y + box.height / 2);
      ctx.restore();
    }
  }

  /* -- pointer ------------------------------------------------------------ */

  #at(event: PointerEvent): { x: number; y: number } {
    const rect = this.#canvas.getBoundingClientRect();
    return {
      x: event.clientX - rect.left - this.#panX,
      y: event.clientY - rect.top - this.#panY,
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
    if (box) {
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
    this.#dragging = null;
    this.#panning = null;
    this.#dirty = true;
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
