import { OfficeAgent, type VisualState } from "./agent";
import { PerfSampler, type FrameStats } from "./perf";
import {
  AGENT_RADIUS,
  DESK_HEIGHT,
  DESK_WIDTH,
  TARGET_FPS,
  WORLD_HEIGHT,
  WORLD_WIDTH,
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

/** World y where the back wall meets the floor. */
const WALL_Y = 56;

/** Longest delta we integrate. Protects the sim after the tab is unhidden. */
const MAX_DT = 0.1;

/**
 * Draws the office with one requestAnimationFrame loop and nothing else.
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

  private agents: OfficeAgent[] = [];
  private byId = new Map<string, OfficeAgent>();

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

  /**
   * The floor is drawn across the whole canvas rather than only the world
   * rectangle, so a window of any aspect ratio shows a room instead of black
   * bars. These are the canvas corners expressed in world coordinates.
   */
  private viewMinX = 0;
  private viewMinY = 0;
  private viewMaxX = WORLD_WIDTH;
  private viewMaxY = WORLD_HEIGHT;

  /** Floor detail, rebuilt on resize and reused every frame. */
  private floorGrid = new Path2D();

  private readonly onVisibility = () => {
    if (document.hidden) this.pause();
    else this.resume();
  };

  constructor(canvas: HTMLCanvasElement) {
    const ctx = canvas.getContext("2d", { alpha: false });
    if (!ctx) throw new Error("Canvas 2D is unavailable");
    this.canvas = canvas;
    this.ctx = ctx;
  }

  get stats(): FrameStats {
    return this.sampler.stats;
  }

  /** Replaces the cast. Positions are chosen by the agents themselves. */
  setAgents(specs: readonly AgentSpec[]): void {
    this.agents = specs.map((s) => new OfficeAgent(s));
    this.byId = new Map(this.agents.map((a) => [a.id, a]));
  }

  /** Queues a coarse state change. Cheap enough to call from an event handler. */
  push(agentId: string, state: VisualState): void {
    this.queue.push({ agentId, state });
  }

  /** Recomputes the world-to-device transform. Call on size changes. */
  resize(cssWidth: number, cssHeight: number): void {
    const dpr = window.devicePixelRatio || 1;
    const w = Math.max(1, Math.floor(cssWidth));
    const h = Math.max(1, Math.floor(cssHeight));
    if (w === this.cssWidth && h === this.cssHeight && dpr === this.dpr) return;

    this.cssWidth = w;
    this.cssHeight = h;
    this.dpr = dpr;

    this.canvas.width = Math.floor(w * dpr);
    this.canvas.height = Math.floor(h * dpr);
    this.canvas.style.width = `${w}px`;
    this.canvas.style.height = `${h}px`;

    // Fit the whole world, preserving aspect, and centre the remainder.
    this.scale = Math.min(w / WORLD_WIDTH, h / WORLD_HEIGHT);
    this.offsetX = (w - WORLD_WIDTH * this.scale) / 2;
    this.offsetY = (h - WORLD_HEIGHT * this.scale) / 2;

    this.viewMinX = -this.offsetX / this.scale;
    this.viewMinY = -this.offsetY / this.scale;
    this.viewMaxX = (w - this.offsetX) / this.scale;
    this.viewMaxY = (h - this.offsetY) / this.scale;
    this.floorGrid = buildFloorGrid(
      this.viewMinX,
      this.viewMinY,
      this.viewMaxX,
      this.viewMaxY,
    );

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
    this.raf = requestAnimationFrame(this.tick);
  }

  private readonly tick = (now: number) => {
    if (!this.running) return;
    this.raf = requestAnimationFrame(this.tick);

    // Frame cap. Cheaper than drawing at panel rate, and invisible at 48fps.
    if (now - this.lastDraw < FRAME_INTERVAL_MS - 0.5) return;

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
    for (let i = 0; i < list.length; i++) list[i].update(dt);

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

  private draw(now: number): void {
    const ctx = this.ctx;
    const dpr = this.dpr;

    // Surround, in device space.
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = "#0b0d11";
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

    const s = this.scale * dpr;
    ctx.setTransform(s, 0, 0, s, this.offsetX * dpr, this.offsetY * dpr);

    this.drawFloor(ctx);

    // Text settings are the same for every label, so set them once per frame
    // rather than once per agent.
    ctx.font = "600 12px Inter, system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";

    const list = this.agents;
    for (let i = 0; i < list.length; i++) this.drawDesk(ctx, list[i]);
    for (let i = 0; i < list.length; i++) this.drawAgent(ctx, list[i], now);
  }

  private drawFloor(ctx: CanvasRenderingContext2D): void {
    const x = this.viewMinX;
    const y = this.viewMinY;
    const w = this.viewMaxX - x;
    const h = this.viewMaxY - y;

    ctx.fillStyle = "#171b22";
    ctx.fillRect(x, y, w, h);

    ctx.strokeStyle = "#1e242e";
    ctx.lineWidth = 1 / this.scale;
    ctx.stroke(this.floorGrid);

    // Back wall, to give the room a floor/wall split.
    ctx.fillStyle = "#12151b";
    ctx.fillRect(x, y, w, WALL_Y - y);
    ctx.fillStyle = "#232a35";
    ctx.fillRect(x, WALL_Y - 2, w, 2);
  }

  private drawDesk(ctx: CanvasRenderingContext2D, a: OfficeAgent): void {
    const x = a.deskX - DESK_WIDTH / 2;
    const y = a.deskY - DESK_HEIGHT / 2;

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

    // Monitor, tinted with the owner's colour when they are working.
    const lit = a.state === "working";
    ctx.fillStyle = "#151920";
    ctx.beginPath();
    ctx.roundRect(a.deskX - 30, y - 26, 60, 30, 4);
    ctx.fill();
    ctx.fillStyle = lit ? a.colourDim : "#1d232c";
    ctx.beginPath();
    ctx.roundRect(a.deskX - 26, y - 22, 52, 22, 3);
    ctx.fill();

    // Nameplate.
    ctx.fillStyle = "#5f6878";
    ctx.fillText(a.name, a.deskX, a.deskY + 2);
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
    ctx.arc(a.x + a.facing * 1.5, bodyY - bob - AGENT_RADIUS * 0.42, AGENT_RADIUS * 0.52, 0, Math.PI * 2);
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

/**
 * Floor grid as a single reusable path, covering the visible world rectangle.
 * Rebuilt on resize only; stroking it costs one call per frame.
 */
function buildFloorGrid(minX: number, minY: number, maxX: number, maxY: number): Path2D {
  const p = new Path2D();
  const step = 50;
  const top = Math.max(minY, WALL_Y);
  for (let x = Math.ceil(minX / step) * step; x < maxX; x += step) {
    p.moveTo(x, top);
    p.lineTo(x, maxY);
  }
  for (let y = Math.ceil(top / step) * step; y < maxY; y += step) {
    p.moveTo(minX, y);
    p.lineTo(maxX, y);
  }
  return p;
}
