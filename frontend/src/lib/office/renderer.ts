import { OfficeAgent, type VisualState } from "./agent";
import { PerfSampler, type FrameStats } from "./perf";
import {
  AGENT_RADIUS,
  DESK_HEIGHT,
  DESK_WIDTH,
  IDLE_FPS,
  TARGET_FPS,
  WALL_Y,
  WORLD_HEIGHT,
  WORLD_WIDTH,
  deskObstacle,
  deskRect,
  type Rect,
} from "./world";

/** What the bridge hands the renderer: one agent, one new coarse state. */
export interface AgentCommand {
  agentId: string;
  state: VisualState;
}

export interface AgentSpec {
  id: string;
  name: string;
  colour: string;
  deskX: number;
  deskY: number;
  seatX: number;
  seatY: number;
}

const FRAME_INTERVAL_MS = 1000 / TARGET_FPS;
const IDLE_INTERVAL_MS = 1000 / IDLE_FPS;

/** Longest delta we integrate. Protects the sim after the tab is unhidden. */
const MAX_DT = 0.1;

/** Highest device pixel ratio the canvas will render at. */
const MAX_DPR = 2;

/**
 * Above this share of the canvas, repainting everything beats bookkeeping.
 * Reached only when the office is crowded or the agents are spread wide.
 */
const FULL_REPAINT_RATIO = 0.6;

/** Rects are pooled: the loop must not allocate. */
const MAX_DIRTY = 24;

/**
 * Draws the office with one requestAnimationFrame loop and nothing else.
 *
 * The floor, grid, wall and desks are painted once into an offscreen canvas and
 * then blitted back as the background. Each frame repaints only the rectangles
 * that actually changed -- where an agent was, where it now is, and any monitor
 * that lit up -- because measurement showed the cost of this renderer is not
 * its drawing but the webview compositing a full-canvas repaint every frame.
 *
 * The renderer is deliberately not reactive. Semantic state changes arrive
 * through `push`, are queued as plain objects, and are drained at the top of a
 * frame. Nothing here reaches back into Svelte, so agent movement costs zero
 * component updates.
 */
export class OfficeRenderer {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private readonly sampler = new PerfSampler();

  /** The static background: floor, grid, wall and unlit desks. */
  private readonly layer: HTMLCanvasElement;
  private readonly layerCtx: CanvasRenderingContext2D;

  private agents: OfficeAgent[] = [];
  private byId = new Map<string, OfficeAgent>();
  private obstacles: Rect[] = [];

  /** Pending commands, drained each frame. Reused, never reallocated. */
  private queue: AgentCommand[] = [];

  private raf = 0;
  private running = false;
  private lastTime = 0;
  private lastDraw = 0;

  /** Device-space size and the world-to-device transform. */
  private dpr = 1;
  private cssWidth = 0;
  private cssHeight = 0;
  private scale = 1;
  private offsetX = 0;
  private offsetY = 0;

  /** The canvas corners in world coordinates. */
  private viewMinX = 0;
  private viewMinY = 0;
  private viewMaxX = WORLD_WIDTH;
  private viewMaxY = WORLD_HEIGHT;

  /** Set when the next frame must repaint everything. */
  private needsFullRepaint = true;

  /** True while any agent is doing something more interesting than strolling. */
  private busy = false;

  /** Dirty rectangles for this frame, in world units. Pooled. */
  private readonly dirty: Rect[] = Array.from({ length: MAX_DIRTY }, () => ({
    x: 0,
    y: 0,
    w: 0,
    h: 0,
  }));
  private dirtyCount = 0;

  /** Scratch rectangles, so boxAt need not allocate. */
  private readonly scratchA: Rect = { x: 0, y: 0, w: 0, h: 0 };
  private readonly scratchB: Rect = { x: 0, y: 0, w: 0, h: 0 };

  private readonly onVisibility = () => {
    if (document.hidden) this.pause();
    else this.resume();
  };

  constructor(canvas: HTMLCanvasElement) {
    const ctx = canvas.getContext("2d", { alpha: false });
    if (!ctx) throw new Error("Canvas 2D is unavailable");
    this.canvas = canvas;
    this.ctx = ctx;

    this.layer = document.createElement("canvas");
    const layerCtx = this.layer.getContext("2d", { alpha: false });
    if (!layerCtx) throw new Error("Canvas 2D is unavailable");
    this.layerCtx = layerCtx;
  }

  get stats(): FrameStats {
    return this.sampler.stats;
  }

  /** Replaces the cast. Positions are chosen by the agents themselves. */
  setAgents(specs: readonly AgentSpec[]): void {
    this.agents = specs.map((s) => new OfficeAgent(s));
    this.byId = new Map(this.agents.map((a) => [a.id, a]));
    this.obstacles = specs.map((s) => deskObstacle(s.deskX, s.deskY));
    for (const agent of this.agents) agent.setObstacles(this.obstacles);

    this.paintLayer();
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  /** Queues a coarse state change. Cheap enough to call from an event handler. */
  push(agentId: string, state: VisualState): void {
    this.queue.push({ agentId, state });
  }

  /** Recomputes the world-to-device transform. Call on size changes. */
  resize(cssWidth: number, cssHeight: number): void {
    // Fill cost scales with the square of the ratio, and a 3x panel buys
    // nothing visible on flat shapes, so the office pays for 2x at most.
    const dpr = Math.min(window.devicePixelRatio || 1, MAX_DPR);
    const w = Math.max(1, Math.floor(cssWidth));
    const h = Math.max(1, Math.floor(cssHeight));
    if (w === this.cssWidth && h === this.cssHeight && dpr === this.dpr) return;

    this.cssWidth = w;
    this.cssHeight = h;
    this.dpr = dpr;

    const deviceW = Math.floor(w * dpr);
    const deviceH = Math.floor(h * dpr);
    this.canvas.width = deviceW;
    this.canvas.height = deviceH;
    this.canvas.style.width = `${w}px`;
    this.canvas.style.height = `${h}px`;
    this.layer.width = deviceW;
    this.layer.height = deviceH;

    // Fit the whole world, preserving aspect, and centre the remainder.
    this.scale = Math.min(w / WORLD_WIDTH, h / WORLD_HEIGHT);
    this.offsetX = (w - WORLD_WIDTH * this.scale) / 2;
    this.offsetY = (h - WORLD_HEIGHT * this.scale) / 2;

    this.viewMinX = -this.offsetX / this.scale;
    this.viewMinY = -this.offsetY / this.scale;
    this.viewMaxX = (w - this.offsetX) / this.scale;
    this.viewMaxY = (h - this.offsetY) / this.scale;

    this.paintLayer();
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.lastTime = performance.now();
    this.lastDraw = 0;
    document.addEventListener("visibilitychange", this.onVisibility);
    this.raf = requestAnimationFrame(this.tick);
  }

  stop(): void {
    this.running = false;
    if (this.raf) cancelAnimationFrame(this.raf);
    this.raf = 0;
    document.removeEventListener("visibilitychange", this.onVisibility);
  }

  /** Stops drawing without tearing anything down. Used when the window hides. */
  private pause(): void {
    if (!this.running) return;
    if (this.raf) cancelAnimationFrame(this.raf);
    this.raf = 0;
  }

  private resume(): void {
    if (!this.running || this.raf) return;
    this.lastTime = performance.now();
    this.sampler.reset();
    this.needsFullRepaint = true;
    this.raf = requestAnimationFrame(this.tick);
  }

  private readonly tick = (now: number) => {
    if (!this.running) return;
    this.raf = requestAnimationFrame(this.tick);

    // Frame cap. Cheaper than drawing at panel rate, and invisible at 30fps.
    // A calm office drops to half rate, which the eye cannot pick up on a
    // slow stroll but the compositor certainly can.
    const interval = this.busy ? FRAME_INTERVAL_MS : IDLE_INTERVAL_MS;
    if (now - this.lastDraw < interval - 0.5) return;

    const dt = Math.min((now - this.lastTime) / 1000, MAX_DT);
    this.lastTime = now;
    this.lastDraw = now;

    this.drainQueue();

    const started = performance.now();
    this.update(dt);
    this.draw(now);
    this.sampler.sample(dt, performance.now() - started, this.agents.length, now);
  };

  private drainQueue(): void {
    const q = this.queue;
    for (let i = 0; i < q.length; i++) {
      const cmd = q[i];
      const agent = this.byId.get(cmd.agentId);
      if (!agent) continue;
      switch (cmd.state) {
        case "walking":
          agent.assign();
          break;
        case "working":
          agent.work();
          break;
        case "finished":
          agent.finish();
          break;
        case "error":
          agent.fail();
          break;
        case "idle":
          agent.idle();
          break;
      }
    }
    q.length = 0;
  }

  private update(dt: number): void {
    const list = this.agents;
    let busy = false;
    for (let i = 0; i < list.length; i++) {
      list[i].update(dt);
      if (list[i].wantsSmoothFrames) busy = true;
    }
    this.busy = busy;

    // Painter's order by depth. Insertion sort in place: no allocation, and
    // the list is nearly sorted every frame.
    for (let i = 1; i < list.length; i++) {
      const a = list[i];
      let j = i - 1;
      while (j >= 0 && list[j].y > a.y) {
        list[j + 1] = list[j];
        j--;
      }
      list[j + 1] = a;
    }
  }

  /** Paints the static background: floor, grid, wall and unlit desks. */
  private paintLayer(): void {
    if (this.layer.width === 0 || this.layer.height === 0) return;

    const ctx = this.layerCtx;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = "#0b0d11";
    ctx.fillRect(0, 0, this.layer.width, this.layer.height);

    const s = this.scale * this.dpr;
    ctx.setTransform(s, 0, 0, s, this.offsetX * this.dpr, this.offsetY * this.dpr);

    const x = this.viewMinX;
    const y = this.viewMinY;
    const w = this.viewMaxX - x;
    const h = this.viewMaxY - y;

    ctx.fillStyle = "#171b22";
    ctx.fillRect(x, y, w, h);

    // Floor grid.
    ctx.strokeStyle = "#1e242e";
    ctx.lineWidth = 1 / this.scale;
    ctx.beginPath();
    const step = 50;
    const top = Math.max(y, WALL_Y);
    for (let gx = Math.ceil(x / step) * step; gx < this.viewMaxX; gx += step) {
      ctx.moveTo(gx, top);
      ctx.lineTo(gx, this.viewMaxY);
    }
    for (let gy = Math.ceil(top / step) * step; gy < this.viewMaxY; gy += step) {
      ctx.moveTo(x, gy);
      ctx.lineTo(this.viewMaxX, gy);
    }
    ctx.stroke();

    // Back wall, to give the room a floor/wall split.
    ctx.fillStyle = "#12151b";
    ctx.fillRect(x, y, w, WALL_Y - y);
    ctx.fillStyle = "#232a35";
    ctx.fillRect(x, WALL_Y - 2, w, 2);

    ctx.font = "600 12px Inter, system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    for (const agent of this.agents) this.drawDesk(ctx, agent, false);
  }

  private draw(now: number): void {
    const ctx = this.ctx;
    if (this.canvas.width === 0) return;

    this.collectDirty();

    const full =
      this.needsFullRepaint ||
      this.dirtyCount === 0 ||
      this.dirtyArea() > (this.viewMaxX - this.viewMinX) * (this.viewMaxY - this.viewMinY) * FULL_REPAINT_RATIO;

    if (full) {
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.drawImage(this.layer, 0, 0);
      this.setWorldTransform();
      this.drawScene(ctx, now);
      this.needsFullRepaint = false;
    } else {
      for (let i = 0; i < this.dirtyCount; i++) {
        const r = this.dirty[i];
        // Restore the background for this patch, in device space so the blit
        // is a straight copy with no resampling.
        const dx = Math.floor(this.toDeviceX(r.x));
        const dy = Math.floor(this.toDeviceY(r.y));
        const dw = Math.ceil(r.w * this.scale * this.dpr) + 2;
        const dh = Math.ceil(r.h * this.scale * this.dpr) + 2;
        ctx.setTransform(1, 0, 0, 1, 0, 0);
        ctx.drawImage(this.layer, dx, dy, dw, dh, dx, dy, dw, dh);

        this.setWorldTransform();
        ctx.save();
        ctx.beginPath();
        ctx.rect(r.x, r.y, r.w, r.h);
        ctx.clip();
        this.drawScene(ctx, now);
        ctx.restore();
      }
    }

    // Remember where everyone was drawn, so the next frame knows what to erase.
    for (const agent of this.agents) {
      agent.drawnX = agent.x;
      agent.drawnY = agent.y;
      agent.drawnState = agent.state;
      agent.neverDrawn = false;
    }
  }

  /** Draws everything that moves: lit monitors and the agents themselves. */
  private drawScene(ctx: CanvasRenderingContext2D, now: number): void {
    ctx.font = "600 12px Inter, system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";

    const list = this.agents;
    for (let i = 0; i < list.length; i++) {
      if (isLit(list[i].state)) this.drawDesk(ctx, list[i], true);
    }
    for (let i = 0; i < list.length; i++) this.drawAgent(ctx, list[i], now);
  }

  /**
   * Works out which rectangles changed since the last frame: where each agent
   * was, where it is now, and the desk of any agent whose monitor just lit up
   * or went dark.
   */
  private collectDirty(): void {
    this.dirtyCount = 0;
    const list = this.agents;

    for (let i = 0; i < list.length; i++) {
      const agent = list[i];
      const moved = agent.x !== agent.drawnX || agent.y !== agent.drawnY;
      const restyled = agent.state !== agent.drawnState;
      // A working or erroring agent animates in place -- typing dots, a
      // pulsing ring, a breathing bob -- so it is dirty even when still.
      const animating = agent.state !== "idle";

      if (!moved && !restyled && !animating && !agent.neverDrawn) continue;

      this.addDirty(agent.boxAt(agent.drawnX, agent.drawnY, this.scratchA));
      if (moved) this.addDirty(agent.boxAt(agent.x, agent.y, this.scratchB));

      if (restyled || isLit(agent.state)) {
        const desk = deskRect(agent.deskX, agent.deskY);
        this.addDirty(desk);
      }
    }

    this.mergeDirty();
  }

  private addDirty(r: Rect): void {
    if (this.dirtyCount >= MAX_DIRTY) {
      // Out of slots: fall back to one full repaint rather than dropping a
      // patch and leaving a smear on screen.
      this.needsFullRepaint = true;
      return;
    }
    const slot = this.dirty[this.dirtyCount++];
    slot.x = r.x;
    slot.y = r.y;
    slot.w = r.w;
    slot.h = r.h;
  }

  /** Collapses overlapping rectangles, so no pixel is painted twice. */
  private mergeDirty(): void {
    let merged = true;
    while (merged) {
      merged = false;
      for (let i = 0; i < this.dirtyCount; i++) {
        for (let j = i + 1; j < this.dirtyCount; j++) {
          if (!overlaps(this.dirty[i], this.dirty[j])) continue;
          union(this.dirty[i], this.dirty[j]);
          // Remove j by swapping the last rect into its place.
          const last = this.dirty[this.dirtyCount - 1];
          const target = this.dirty[j];
          target.x = last.x;
          target.y = last.y;
          target.w = last.w;
          target.h = last.h;
          this.dirtyCount--;
          merged = true;
          j--;
        }
      }
    }
  }

  private dirtyArea(): number {
    let area = 0;
    for (let i = 0; i < this.dirtyCount; i++) {
      area += this.dirty[i].w * this.dirty[i].h;
    }
    return area;
  }

  private setWorldTransform(): void {
    const s = this.scale * this.dpr;
    this.ctx.setTransform(s, 0, 0, s, this.offsetX * this.dpr, this.offsetY * this.dpr);
  }

  private toDeviceX(worldX: number): number {
    return worldX * this.scale * this.dpr + this.offsetX * this.dpr;
  }

  private toDeviceY(worldY: number): number {
    return worldY * this.scale * this.dpr + this.offsetY * this.dpr;
  }

  /**
   * Draws one desk. `lit` draws only the parts that change with state, so the
   * static layer can hold the rest.
   */
  private drawDesk(ctx: CanvasRenderingContext2D, a: OfficeAgent, lit: boolean): void {
    const x = a.deskX - DESK_WIDTH / 2;
    const y = a.deskY - DESK_HEIGHT / 2;

    if (!lit) {
      // Surface.
      ctx.fillStyle = "#2a313d";
      ctx.beginPath();
      ctx.roundRect(x, y, DESK_WIDTH, DESK_HEIGHT, 7);
      ctx.fill();

      // Front edge, for a hint of thickness.
      ctx.fillStyle = "#222833";
      ctx.beginPath();
      ctx.roundRect(x, y + DESK_HEIGHT - 9, DESK_WIDTH, 9, 5);
      ctx.fill();
    }

    // Monitor, tinted with the owner's colour when they are working.
    ctx.fillStyle = "#151920";
    ctx.beginPath();
    ctx.roundRect(a.deskX - 30, y - 26, 60, 30, 4);
    ctx.fill();
    ctx.fillStyle = lit ? a.colourDim : "#1d232c";
    ctx.beginPath();
    ctx.roundRect(a.deskX - 26, y - 22, 52, 22, 3);
    ctx.fill();

    if (!lit) {
      // Nameplate.
      ctx.fillStyle = "#5f6878";
      ctx.fillText(a.name, a.deskX, a.deskY + 2);
    }
  }

  private drawAgent(ctx: CanvasRenderingContext2D, a: OfficeAgent, now: number): void {
    const seated = a.state === "working" || a.state === "finished";
    // Working agents sit lower and behind the desk edge; everyone else stands.
    const bodyY = seated ? a.y - 6 : a.y;
    const bob = seated
      ? Math.sin(a.clock * 2.4) * 0.9
      : a.state === "walking"
        ? Math.abs(Math.sin(a.step)) * 2.2
        : Math.sin(a.clock * 1.4) * 0.8;

    // Contact shadow.
    ctx.fillStyle = "rgba(0,0,0,0.28)";
    ctx.beginPath();
    ctx.ellipse(a.x, a.y + AGENT_RADIUS * 0.95, AGENT_RADIUS * 0.9, 4.5, 0, 0, Math.PI * 2);
    ctx.fill();

    // Body.
    ctx.fillStyle = a.colour;
    ctx.beginPath();
    ctx.roundRect(
      a.x - AGENT_RADIUS * 0.78,
      bodyY - bob,
      AGENT_RADIUS * 1.56,
      AGENT_RADIUS * 1.25,
      5,
    );
    ctx.fill();

    // Head.
    ctx.fillStyle = a.colourSoft;
    ctx.beginPath();
    ctx.arc(
      a.x + a.facing * 1.5,
      bodyY - bob - AGENT_RADIUS * 0.42,
      AGENT_RADIUS * 0.52,
      0,
      Math.PI * 2,
    );
    ctx.fill();

    // Name tag, so a wandering agent is still identifiable away from the desk.
    if (!seated) {
      ctx.fillStyle = "#7d8798";
      ctx.fillText(a.name, a.x, a.y + AGENT_RADIUS * 2.1);
    }

    switch (a.state) {
      case "working":
        this.drawTypingDots(ctx, a, now);
        break;
      case "finished":
        this.drawRing(ctx, a.x, bodyY - bob, "#5bc8a0", 1);
        break;
      case "error":
        this.drawRing(
          ctx,
          a.x,
          bodyY - bob,
          "#ef6f6c",
          0.45 + 0.55 * Math.abs(Math.sin(a.clock * 4)),
        );
        break;
    }
  }

  private drawTypingDots(ctx: CanvasRenderingContext2D, a: OfficeAgent, now: number): void {
    const baseY = a.y - AGENT_RADIUS * 1.9;
    const phase = now / 220;
    for (let i = 0; i < 3; i++) {
      const lift = Math.max(0, Math.sin(phase - i * 0.7)) * 3;
      ctx.fillStyle = i === Math.floor(phase % 3) ? a.colourSoft : a.colourDim;
      ctx.beginPath();
      ctx.arc(a.x - 7 + i * 7, baseY - lift, 2, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  private drawRing(
    ctx: CanvasRenderingContext2D,
    x: number,
    y: number,
    colour: string,
    alpha: number,
  ): void {
    ctx.globalAlpha = alpha;
    ctx.strokeStyle = colour;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(x, y + 2, AGENT_RADIUS * 1.5, 0, Math.PI * 2);
    ctx.stroke();
    ctx.globalAlpha = 1;
  }
}

/** True when the agent's monitor should be tinted. */
function isLit(state: VisualState): boolean {
  return state === "working" || state === "finished";
}

function overlaps(a: Rect, b: Rect): boolean {
  return (
    a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
  );
}

/** Grows a to cover b. */
function union(a: Rect, b: Rect): void {
  const x = Math.min(a.x, b.x);
  const y = Math.min(a.y, b.y);
  const right = Math.max(a.x + a.w, b.x + b.w);
  const bottom = Math.max(a.y + a.h, b.y + b.h);
  a.x = x;
  a.y = y;
  a.w = right - x;
  a.h = bottom - y;
}
