import { OfficeAgent, type VisualState } from "./agent";
import { PerfSampler, type FrameStats } from "./perf";
import {
  AGENT_RADIUS,
  DESK_HEIGHT,
  DESK_WIDTH,
  IDLE_FPS,
  MONITOR_BEZEL,
  MONITOR_HEIGHT,
  MONITOR_LINE_HEIGHT,
  MONITOR_TEXT_LINES,
  MONITOR_TEXT_PAD,
  MONITOR_TEXT_SIZE,
  MONITOR_WIDTH,
  TARGET_FPS,
  WALL_Y,
  WORLD_HEIGHT,
  WORLD_WIDTH,
  deskObstacle,
  deskRect,
  monitorRect,
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
  /**
   * What the agent was already doing when the roster was read. Events carry
   * every change after that, but a page that loads mid-run has missed the ones
   * before it, so the first state comes with the roster.
   */
  state?: VisualState;
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
 * Slop around a monitor when hit-testing a pointer, in world units.
 *
 * A monitor is 60x30 units, which is a small target once the world is scaled
 * into a window, and clicking one is a deliberate act -- missing it by two
 * pixels should not read as "nothing there".
 */
const HIT_PADDING = 8;

/** Nameplates and agent labels. */
const LABEL_FONT = "600 12px Inter, system-ui, sans-serif";

/**
 * The monitor readout. Monospace is not decoration here: a fixed advance is
 * what lets the wrap width be a character count computed once, so the loop can
 * draw a line without ever measuring one.
 */
const MONO_FONT = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";

/** Measured once to learn how wide one monospace character is. */
const SAMPLE = "0123456789abcdefghij";

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
  /** Separate scratch for monitors: drawing and hit-testing must not share. */
  private readonly scratchMonitor: Rect = { x: 0, y: 0, w: 0, h: 0 };
  private readonly scratchHit: Rect = { x: 0, y: 0, w: 0, h: 0 };

  /** The agent whose monitor the pointer is over, if any. */
  private hoverId: string | null = null;

  /**
   * The monitor readout's metrics, in world units, measured once.
   *
   * Both are constants of the world, not of the window: a monitor is always 60
   * units wide whatever the canvas is, so the wrap width is fixed and resizing
   * re-scales the readout rather than re-wrapping it. Knowing the advance also
   * means the caret can be placed by multiplication instead of measurement.
   */
  private readonly monitorAdvance: number;
  private readonly monitorColumns: number;

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

    // Measured from a long sample at a large size, so rounding in the metrics
    // cannot skew the ratio. This is the only text measurement the renderer
    // ever does.
    ctx.font = `100px ${MONO_FONT}`;
    this.monitorAdvance = (ctx.measureText(SAMPLE).width / SAMPLE.length / 100) * MONITOR_TEXT_SIZE;
    const usable = MONITOR_WIDTH - (MONITOR_BEZEL + MONITOR_TEXT_PAD) * 2;
    this.monitorColumns = Math.max(1, Math.floor(usable / this.monitorAdvance));
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
    // Whoever was already at work gets put back at their desk rather than
    // walked there: the office should open in the state the backend is in, not
    // in the state a fresh page would be in.
    for (const spec of specs) if (spec.state) this.byId.get(spec.id)?.resume(spec.state);

    this.paintLayer();
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  /** Queues a coarse state change. Cheap enough to call from an event handler. */
  push(agentId: string, state: VisualState): void {
    this.queue.push({ agentId, state });
  }

  /**
   * Hands an agent the prose they are writing right now, so their monitor can
   * show it. `sourceId` identifies the run of text, which is how a few more
   * characters on the end are told from a new run after a tool call; an empty
   * one blanks the monitor.
   *
   * Called at most once per agent per frame, from the one place that watches
   * the console. The wrapping happens here rather than while drawing, and only
   * when the text actually grew -- an unchanged call costs one comparison.
   *
   * Nothing is marked dirty on the way out, deliberately. A working agent's
   * desk is already repainted every frame, so new characters land for free;
   * for anyone else the readout is not drawn at all, so a growing tail changes
   * no pixels and forcing a repaint for it would throw away the dirty-rect
   * pass on exactly the frames where an agent is hurrying to their desk. The
   * frame they stop working, `collectDirty` redraws the desk anyway, because
   * the state changed.
   */
  setMonitorText(agentId: string, sourceId: string, text: string): void {
    const agent = this.byId.get(agentId);
    if (!agent) return;
    if (sourceId) agent.tail.update(sourceId, text, this.monitorColumns);
    else agent.tail.clear();
  }

  /** The visual state an agent is in, or null if there is no such agent. */
  stateOf(agentId: string): VisualState | null {
    return this.byId.get(agentId)?.state ?? null;
  }

  /**
   * Turns a point in CSS pixels relative to the canvas into world coordinates.
   * The inverse of the transform `resize` builds.
   */
  toWorldX(cssX: number): number {
    return (cssX - this.offsetX) / this.scale;
  }

  toWorldY(cssY: number): number {
    return (cssY - this.offsetY) / this.scale;
  }

  /**
   * The agent whose monitor sits under a point given in CSS pixels relative to
   * the canvas, or null. Monitors never overlap, so the first hit is the hit.
   */
  hitTestMonitor(cssX: number, cssY: number): string | null {
    const x = this.toWorldX(cssX);
    const y = this.toWorldY(cssY);
    for (let i = 0; i < this.agents.length; i++) {
      const a = this.agents[i];
      const r = monitorRect(a.deskX, a.deskY, this.scratchHit);
      if (
        x >= r.x - HIT_PADDING &&
        x <= r.x + r.w + HIT_PADDING &&
        y >= r.y - HIT_PADDING &&
        y <= r.y + r.h + HIT_PADDING
      ) {
        return a.id;
      }
    }
    return null;
  }

  /**
   * Marks one agent's monitor as hovered, or none. The caller decides what is
   * worth highlighting; the renderer only draws it.
   *
   * A change forces one full repaint instead of growing the dirty-rect
   * bookkeeping. It happens when the pointer crosses a monitor's edge, which is
   * rare next to the fifteen to thirty frames a second already being drawn.
   */
  setHover(agentId: string | null): void {
    if (agentId === this.hoverId) return;
    this.hoverId = agentId;
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
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

    ctx.font = LABEL_FONT;
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
    ctx.font = LABEL_FONT;
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

    // Monitor, tinted with the owner's colour when they are working, and
    // brightened further while the pointer is over it. The highlight stays
    // inside the monitor's own outline so it needs no extra dirty rectangle.
    const m = monitorRect(a.deskX, a.deskY, this.scratchMonitor);
    const hot = lit && a.id === this.hoverId;
    ctx.fillStyle = hot ? a.colour : "#151920";
    ctx.beginPath();
    ctx.roundRect(m.x, m.y, m.w, m.h, 4);
    ctx.fill();
    ctx.fillStyle = lit ? (hot ? a.colourSoft : a.colourDim) : "#1d232c";
    ctx.beginPath();
    ctx.roundRect(m.x + MONITOR_BEZEL, m.y + MONITOR_BEZEL, m.w - MONITOR_BEZEL * 2, m.h - MONITOR_BEZEL * 2, 3);
    ctx.fill();

    // The readout belongs to a turn in progress rather than to a lit monitor:
    // a finished desk keeps its glow but stops reporting, the same as today.
    // `lit` also keeps it out of the static layer, which would bake it in.
    if (lit && a.state === "working" && a.tail.active) this.drawReadout(ctx, a, m, hot);

    if (!lit) {
      // Nameplate.
      ctx.fillStyle = "#5f6878";
      ctx.fillText(a.name, a.deskX, a.deskY + 2);
    }
  }

  /**
   * The live terminal on a working agent's monitor: the tail of what they are
   * writing, oldest line at the top, the line being typed at the bottom.
   *
   * Everything expensive already happened when the text arrived. The lines are
   * wrapped, they are trimmed to the height of the glass, and the caret sits at
   * a known multiple of the character advance -- so this is three fillText
   * calls and a rectangle, with nothing measured and nothing allocated.
   */
  private drawReadout(
    ctx: CanvasRenderingContext2D,
    a: OfficeAgent,
    m: Rect,
    hot: boolean,
  ): void {
    const lines = a.tail.lines;
    const glassX = m.x + MONITOR_BEZEL;
    const glassY = m.y + MONITOR_BEZEL;
    const glassW = MONITOR_WIDTH - MONITOR_BEZEL * 2;
    const glassH = MONITOR_HEIGHT - MONITOR_BEZEL * 2;

    // Type this small only survives on a dark ground. The lit tint and the
    // agent's ink are the same hue a shade apart, which is a fine way to show
    // a monitor is on and a hopeless one to read six units of text off, so a
    // readout drops the glass to near-black first. Hover lets more of the
    // colour through instead of flipping the ink, which keeps the highlight
    // visible without costing contrast.
    ctx.fillStyle = hot ? "rgba(10,12,17,0.7)" : "rgba(10,12,17,0.88)";
    ctx.beginPath();
    ctx.roundRect(glassX, glassY, glassW, glassH, 3);
    ctx.fill();

    // Centre the full block of rows, so the readout does not shift downwards
    // as the first lines fill in.
    const left = glassX + MONITOR_TEXT_PAD;
    let y =
      glassY +
      (glassH - MONITOR_LINE_HEIGHT * MONITOR_TEXT_LINES) / 2 +
      MONITOR_LINE_HEIGHT / 2;

    ctx.font = `${MONITOR_TEXT_SIZE}px ${MONO_FONT}`;
    ctx.textAlign = "left";
    ctx.fillStyle = a.colourSoft;

    // Scrollback sits back a little, which puts the eye on the line still
    // being written without having to animate anything to say so.
    ctx.globalAlpha = 0.72;
    for (let i = 0; i < lines.length; i++) {
      ctx.fillText(lines[i], left, y);
      y += MONITOR_LINE_HEIGHT;
    }

    ctx.globalAlpha = 1;
    ctx.fillText(a.tail.current, left, y);

    // Block caret, blinking on the agent's own clock so the desks are not all
    // winking in unison. Skipped on a full line, where it would overhang.
    const column = a.tail.current.length;
    if (column < this.monitorColumns && Math.sin(a.clock * 5) > -0.2) {
      ctx.fillRect(
        left + column * this.monitorAdvance,
        y - MONITOR_TEXT_SIZE * 0.42,
        this.monitorAdvance * 0.85,
        MONITOR_TEXT_SIZE * 0.84,
      );
    }

    // Hand the context back the way the rest of the scene expects it.
    ctx.font = LABEL_FONT;
    ctx.textAlign = "center";
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
