import { OfficeAgent, type VisualState } from "./agent";
import { avatarImage } from "./avatars";
import { IdleDirector } from "./idle";
import { PerfSampler, type FrameStats } from "./perf";
import {
  COFFEE_HEIGHT,
  COFFEE_MACHINE_HEIGHT,
  COFFEE_MACHINE_WIDTH,
  COFFEE_WIDTH,
  FRIDGE_HEIGHT,
  LUNCH_HEIGHT,
  MEETING_HEIGHT,
  MEETING_WIDTH,
  PLANTS,
  PLANT_RADIUS,
  PONG_HEIGHT,
  PRINTER,
  PRINTER_HEIGHT,
  SHELF,
  SHELF_HEIGHT,
  SHELF_WIDTH,
  coffeeRect,
  fridgeRect,
  furnitureObstacles,
  lunchRect,
  lunchSeats,
  meetingRect,
  placeProps,
  pongRect,
  printerRect,
  shelfRect,
  type Prop,
  type Props,
} from "./props";
import {
  dispatchLine,
  speechRect,
  SPEECH_FONT,
  SPEECH_HEIGHT,
  SPEECH_PAD_X,
  SPEECH_SECONDS,
} from "./speech";
import {
  AGENT_RADIUS,
  DESK_HEIGHT,
  DESK_WIDTH,
  DOORWAYS,
  IDLE_FPS,
  MONITOR_BEZEL,
  MONITOR_HEIGHT,
  MONITOR_LINE_HEIGHT,
  MONITOR_TEXT_LINES,
  MONITOR_TEXT_PAD,
  MONITOR_TEXT_SIZE,
  MONITOR_WIDTH,
  ROOMS,
  TARGET_FPS,
  WALL,
  WALLS,
  WORLD_HEIGHT,
  WORLD_WIDTH,
  deskObstacle,
  deskRect,
  monitorRect,
  prefersReducedMotion,
  rectsOverlap,
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
   * Where to fetch this agent's picture, if they have one. Absent is the
   * ordinary case: an agent whose folder holds no avatar is drawn as the
   * coloured figure they always were.
   */
  avatar?: string;
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

/**
 * Rects are pooled: the loop must not allocate. There is room for every agent
 * to be moving with a bubble up and a ball in the air, because running out
 * costs a full repaint.
 */
const MAX_DIRTY = 32;

/**
 * Slop around a monitor when working out its target box, in world units.
 *
 * A monitor is 60x30 units, which is a small target once the world is scaled
 * into a window, and opening a desk is a deliberate act -- missing it by two
 * pixels should not read as "nothing there".
 */
const HIT_PADDING = 8;

/**
 * One desk's box on screen, in CSS pixels relative to the canvas.
 *
 * The office is a canvas, so nothing in it can be focused, labelled or reached
 * with a keyboard. These are what the component puts a real button over: the
 * renderer owns where a desk is in pixels, the component owns what it is
 * called and what happens when it is pressed.
 */
export interface DeskTarget {
  id: string;
  left: number;
  top: number;
  width: number;
  height: number;
}

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
 * The floorplan -- room floors, walls, doorways, desks and every piece of
 * furniture -- is painted once into an offscreen canvas and then blitted back
 * as the background. Each frame repaints only the rectangles that actually
 * changed -- where an agent was, where it now is, a bubble that appeared, a
 * ball in flight, any monitor that lit up -- because measurement showed the
 * cost of this renderer is not its drawing but the webview compositing a
 * full-canvas repaint every frame. An office with three rooms of furniture in
 * it therefore costs exactly what the empty floor did.
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

  /** The static background: rooms, walls, furniture and unlit desks. */
  private readonly layer: HTMLCanvasElement;
  private readonly layerCtx: CanvasRenderingContext2D;

  private agents: OfficeAgent[] = [];
  private byId = new Map<string, OfficeAgent>();
  private obstacles: Rect[] = [];

  /**
   * The furniture. Fixed addresses in the wing, plus a ping pong table
   * wherever the bullpen had space for one.
   */
  private props: Props = placeProps([]);

  /**
   * What the office does with itself between tasks.
   *
   * Given the callback it cannot write itself: a bubble's width has to be
   * measured, and this is the only object that owns a canvas context.
   */
  private readonly director = new IdleDirector((agent, text, seconds) =>
    this.speak(agent, text, seconds),
  );

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

  /**
   * True while the person watching has asked for less movement.
   *
   * Read once a frame rather than per oscillator, and every oscillator in the
   * renderer is guarded by it: a bob, a blinking caret, hopping typing dots
   * and drifting steam are all decoration, and the office has to be able to
   * hold still.
   */
  private still = false;

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
  /** Separate scratch for monitors: drawing and measuring must not share. */
  private readonly scratchMonitor: Rect = { x: 0, y: 0, w: 0, h: 0 };
  private readonly scratchHit: Rect = { x: 0, y: 0, w: 0, h: 0 };
  /** Scratch for speech bubbles, ball rects and the furniture. */
  private readonly scratchSpeech: Rect = { x: 0, y: 0, w: 0, h: 0 };
  private readonly scratchBall: Rect = { x: 0, y: 0, w: 0, h: 0 };
  private readonly scratchProp: Rect = { x: 0, y: 0, w: 0, h: 0 };

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
    // The desks are laid out from the roster, so where a ping pong table fits
    // is only knowable once they are placed. Everything solid goes in before
    // the agents are told what to walk around, and the router works from the
    // same list, so nobody plans a route through drawn furniture.
    this.props = placeProps(this.obstacles);
    this.obstacles.push(...furnitureObstacles(this.props));
    for (const agent of this.agents) agent.setObstacles(this.obstacles);
    this.director.setScene(this.agents, this.obstacles, this.props);
    // Whoever was already at work gets put back at their desk rather than
    // walked there: the office should open in the state the backend is in, not
    // in the state a fresh page would be in.
    for (const spec of specs) if (spec.state) this.byId.get(spec.id)?.resume(spec.state);
    // A rebuilt cast keeps its faces: the images are cached by URL, so this
    // usually costs a map lookup and no fetch.
    for (let i = 0; i < specs.length; i++) this.applyAvatar(this.agents[i], specs[i].avatar ?? "");

    this.paintLayer();
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  /**
   * Renames one agent, without rebuilding the office around them.
   *
   * Deliberately not part of setAgents: that allocates a new cast and puts
   * everybody back where they started, which for a rename would stop a walk
   * mid-stride and blank a monitor that is still being typed on. The name is
   * baked into the static layer as a nameplate, so that is repainted -- once,
   * and only when the name actually changed.
   */
  setAgentName(agentId: string, name: string): void {
    const agent = this.byId.get(agentId);
    if (!agent || agent.name === name) return;
    agent.name = name;
    this.paintLayer();
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  /**
   * Points one agent at their picture, or at nothing.
   *
   * Deliberately not part of setAgents, for the same reason a rename is not:
   * an avatar appearing is not a reason to rebuild the office around it and
   * put everybody back at their opening position. An empty url is an agent
   * with no avatar, which is most of them.
   *
   * The picture on screen is kept until a new one has actually decoded, so
   * re-reading an unchanged file -- which is what every Reload config does --
   * does not blink the office through the fallback and back.
   */
  setAgentAvatar(agentId: string, url: string | null): void {
    const agent = this.byId.get(agentId);
    if (agent) this.applyAvatar(agent, url ?? "");
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
   * Every desk's box on screen, in CSS pixels relative to the canvas.
   *
   * Ordered by where the desk is in the room -- back to front, left to right --
   * and deliberately not in the order the agents are drawn in, which is sorted
   * by depth every frame. A tab order that reshuffled itself as people walked
   * about would be worse than no tab order.
   *
   * Recomputed by the component on a resize or a change of cast, which is the
   * only time it can change; the walking about does not move a desk.
   */
  deskTargets(): DeskTarget[] {
    const targets = this.agents.map((a): DeskTarget => {
      const r = monitorRect(a.deskX, a.deskY, this.scratchHit);
      return {
        id: a.id,
        left: (r.x - HIT_PADDING) * this.scale + this.offsetX,
        top: (r.y - HIT_PADDING) * this.scale + this.offsetY,
        width: (r.w + HIT_PADDING * 2) * this.scale,
        height: (r.h + HIT_PADDING * 2) * this.scale,
      };
    });
    return targets.sort((a, b) => a.top - b.top || a.left - b.left);
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
          // Anton has just handed them the task. They answer before they sit
          // down -- the line is canned, so this costs nothing but a bubble.
          this.speak(agent, dispatchLine());
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
    // After the agents, so a bit reads the standing-still that this frame
    // produced rather than waiting for the next one.
    this.director.update(dt);
    // A ball in the air is the one idle thing small and fast enough to strobe
    // at the idle rate. It lasts a few seconds, twice a minute at worst.
    this.busy = busy || this.director.wantsSmoothFrames;

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
  /**
   * Resolves a url into something drawable for one agent.
   *
   * Everything asynchronous about an avatar stops here: by the time a frame
   * runs, an agent either holds a decoded image or it does not.
   */
  private applyAvatar(agent: OfficeAgent, url: string): void {
    if (agent.avatarUrl === url) return;
    agent.avatarUrl = url;

    if (!url) {
      if (agent.avatar) {
        agent.avatar = null;
        this.repaint();
      }
      return;
    }

    const ready = avatarImage(url, (image) => {
      // The agent may have been pointed somewhere else, or dropped from the
      // cast, while this was in flight.
      if (agent.avatarUrl !== url) return;
      agent.avatar = image;
      this.repaint();
    });
    // undefined is "still loading", and leaves whatever is on screen alone.
    if (ready !== undefined) {
      agent.avatar = ready;
      this.repaint();
    }
  }

  /**
   * Redraws the whole canvas at the next opportunity.
   *
   * For rare, non-motion changes -- a name, a new avatar -- where working out
   * the affected rectangle is more code than repainting one frame is cost.
   */
  private repaint(): void {
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  private paintLayer(): void {
    if (this.layer.width === 0 || this.layer.height === 0) return;

    const ctx = this.layerCtx;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = "#0b0d11";
    ctx.fillRect(0, 0, this.layer.width, this.layer.height);

    const s = this.scale * this.dpr;
    ctx.setTransform(s, 0, 0, s, this.offsetX * this.dpr, this.offsetY * this.dpr);

    for (const room of ROOMS) this.drawRoomFloor(ctx, room);
    this.drawWalls(ctx);
    this.drawDoorways(ctx);

    ctx.font = LABEL_FONT;
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    // Never hot: a highlight baked into the layer would outlive the pointer
    // that caused it, and only another paintLayer would take it off again.
    for (const agent of this.agents) this.drawDesk(ctx, agent, false, false);

    // The furniture nobody works at. Static shapes with nothing that changes,
    // so the office pays for these once and never again.
    this.drawCoffeeStation(ctx, this.props.coffee);
    this.drawFridge(ctx, this.props.fridge);
    this.drawLunchTable(ctx, this.props.lunch);
    this.drawMeetingTable(ctx, this.props.meeting);
    this.drawWhiteboard(ctx);
    this.drawPrinter(ctx, PRINTER);
    this.drawShelf(ctx, SHELF);
    for (const plant of PLANTS) this.drawPlant(ctx, plant);
    if (this.props.pong) this.drawPongTable(ctx, this.props.pong);

    this.drawRoomLabels(ctx);
  }

  /**
   * One room's floor.
   *
   * Each room gets its own surface, because that is most of what tells them
   * apart from across the office: the bullpen keeps the grid it always had,
   * the break room is warmer and tiled tighter, the meeting room is carpet
   * with a rug under the table. Walls alone would read as lines on one floor.
   */
  private drawRoomFloor(ctx: CanvasRenderingContext2D, room: (typeof ROOMS)[number]): void {
    const carpet = room.id === "meeting";
    ctx.fillStyle = floorColour(room.id);
    ctx.fillRect(room.x, room.y, room.w, room.h);

    if (carpet) {
      // A rug rather than a grid: nothing is tiled in a meeting room.
      ctx.fillStyle = "#1b2029";
      ctx.beginPath();
      ctx.roundRect(room.x + 26, room.y + 34, room.w - 52, room.h - 68, 8);
      ctx.fill();
      return;
    }

    const step = room.id === "break" ? 34 : 50;
    ctx.strokeStyle = room.id === "break" ? "#26211f" : "#1e242e";
    ctx.lineWidth = 1 / this.scale;
    ctx.beginPath();
    for (let gx = Math.ceil(room.x / step) * step; gx < room.x + room.w; gx += step) {
      ctx.moveTo(gx, room.y);
      ctx.lineTo(gx, room.y + room.h);
    }
    for (let gy = Math.ceil(room.y / step) * step; gy < room.y + room.h; gy += step) {
      ctx.moveTo(room.x, gy);
      ctx.lineTo(room.x + room.w, gy);
    }
    ctx.stroke();
  }

  /**
   * The walls.
   *
   * Three tones each: a body, a lit face along the top and left, and a shadow
   * where the wall meets the floor. The first attempt at this drew walls
   * darker than the floor, which from any distance read as a seam between two
   * carpets rather than as something you cannot walk through. A wall has to be
   * the lightest thing in the room's outline to read as standing up.
   *
   * Walls are drawing only. What keeps an agent out of one is that the space it
   * occupies is not floor -- see `onFloor` in world.ts.
   */
  private drawWalls(ctx: CanvasRenderingContext2D): void {
    for (const wall of WALLS) {
      const horizontal = wall.w >= wall.h;
      ctx.fillStyle = "#39424f";
      ctx.fillRect(wall.x, wall.y, wall.w, wall.h);
      ctx.fillStyle = "#4e5868";
      if (horizontal) ctx.fillRect(wall.x, wall.y, wall.w, 3);
      else ctx.fillRect(wall.x, wall.y, 3, wall.h);
      ctx.fillStyle = "#0e1116";
      if (horizontal) ctx.fillRect(wall.x, wall.y + wall.h - 2, wall.w, 2);
      else ctx.fillRect(wall.x + wall.w - 2, wall.y, 2, wall.h);
    }
  }

  /**
   * The doorways: a threshold across the opening and a jamb either side of it.
   *
   * Without the jambs an opening reads as a wall somebody forgot to finish.
   * They are the two short blocks that turn a gap into a frame, and they are
   * drawn over the wall rather than cut out of it, which keeps the wall
   * geometry -- the thing agents route through -- one rectangle per stretch.
   */
  private drawDoorways(ctx: CanvasRenderingContext2D): void {
    const post = 9;
    for (const door of DOORWAYS) {
      const half = door.span / 2;
      const near = floorColour(door.a);
      const far = floorColour(door.b);

      if (door.axis === "v") {
        // The floor of each room carried through to the middle of the wall, so
        // the opening reads as somewhere you can walk rather than as a recess.
        // Filling it dark was the first attempt and looked like a niche.
        ctx.fillStyle = near;
        ctx.fillRect(door.at - WALL / 2, door.centre - half, WALL / 2, door.span);
        ctx.fillStyle = far;
        ctx.fillRect(door.at, door.centre - half, WALL / 2, door.span);
        // Jamb posts, framing the opening at both ends.
        ctx.fillStyle = "#1b2027";
        ctx.fillRect(door.at - WALL / 2, door.centre - half, WALL, post);
        ctx.fillRect(door.at - WALL / 2, door.centre + half - post, WALL, post);
        // The saddle, one pale line where the floors meet.
        ctx.fillStyle = "#5a6575";
        ctx.fillRect(door.at - 1, door.centre - half + post, 2, door.span - post * 2);
      } else {
        ctx.fillStyle = near;
        ctx.fillRect(door.centre - half, door.at - WALL / 2, door.span, WALL / 2);
        ctx.fillStyle = far;
        ctx.fillRect(door.centre - half, door.at, door.span, WALL / 2);
        ctx.fillStyle = "#1b2027";
        ctx.fillRect(door.centre - half, door.at - WALL / 2, post, WALL);
        ctx.fillRect(door.centre + half - post, door.at - WALL / 2, post, WALL);
        ctx.fillStyle = "#5a6575";
        ctx.fillRect(door.centre - half + post, door.at - 1, door.span - post * 2, 2);
      }
    }
  }

  /**
   * What each room is called, small and dim.
   *
   * In the bottom corner rather than the top, because the top of every room in
   * this office has something against the wall: a coffee counter, a bookshelf,
   * the coordinator's desk.
   */
  private drawRoomLabels(ctx: CanvasRenderingContext2D): void {
    ctx.font = "600 11px Inter, system-ui, sans-serif";
    ctx.textAlign = "left";
    ctx.textBaseline = "middle";
    ctx.fillStyle = "#333b47";
    for (const room of ROOMS) {
      const right = room.labelCorner === "top-right";
      ctx.textAlign = right ? "right" : "left";
      ctx.fillText(
        room.label,
        right ? room.x + room.w - 16 : room.x + 16,
        room.labelCorner === "bottom-left" ? room.y + room.h - 14 : room.y + 16,
      );
    }
    ctx.font = LABEL_FONT;
    ctx.textAlign = "center";
  }

  /**
   * The coffee station: a counter, a pale worktop, and an espresso machine
   * standing on it with cups beside it.
   *
   * The old version was a small dark box against a dark wall, which at the
   * scale this world is drawn read as nothing at all. What fixes that is not
   * more detail, it is silhouette and contrast: a wide warm counter, one pale
   * horizontal worktop line, and a steel machine standing above that line with
   * a black group head under it. Three shapes, recognisable at half size, and
   * the only warm colours in an office of blue-grey.
   */
  private drawCoffeeStation(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = coffeeRect(p, this.scratchProp);
    const topY = r.y;

    // Cabinet.
    ctx.fillStyle = "#3b2f27";
    ctx.beginPath();
    ctx.roundRect(r.x, topY, r.w, r.h, 4);
    ctx.fill();
    // Cupboard doors, so the front is not a flat slab.
    ctx.strokeStyle = "#2c231d";
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    for (let i = 1; i < 3; i++) {
      const x = r.x + (r.w / 3) * i;
      ctx.moveTo(x, topY + 12);
      ctx.lineTo(x, topY + r.h - 4);
    }
    ctx.stroke();
    // Toe kick, for a little thickness at the bottom.
    ctx.fillStyle = "#241d18";
    ctx.beginPath();
    ctx.roundRect(r.x, topY + r.h - 7, r.w, 7, 3);
    ctx.fill();

    // The worktop: the single strongest cue in the whole prop, so it overhangs
    // the cabinet at both ends and carries everything standing on it.
    ctx.fillStyle = "#c2bbac";
    ctx.beginPath();
    ctx.roundRect(r.x - 5, topY - 7, r.w + 10, 15, 3);
    ctx.fill();
    ctx.fillStyle = "#8d8779";
    ctx.fillRect(r.x - 5, topY + 5, r.w + 10, 3);

    // Espresso machine, standing on the worktop.
    const machineX = r.x + 14;
    const machineY = topY - 7 - COFFEE_MACHINE_HEIGHT;
    // Dark body with a steel top. It was pale to begin with, which next to a
    // pale fridge two feet away made the break room read as two fridges.
    ctx.fillStyle = "#262c36";
    ctx.beginPath();
    ctx.roundRect(machineX, machineY, COFFEE_MACHINE_WIDTH, COFFEE_MACHINE_HEIGHT, 3);
    ctx.fill();
    ctx.fillStyle = "#aab4c2";
    ctx.beginPath();
    ctx.roundRect(machineX, machineY, COFFEE_MACHINE_WIDTH, 9, 3);
    ctx.fill();
    // Bean hopper on top, a badge, and two buttons.
    ctx.fillStyle = "#3a2f27";
    ctx.beginPath();
    ctx.roundRect(machineX + COFFEE_MACHINE_WIDTH - 22, machineY - 9, 16, 10, 2);
    ctx.fill();
    ctx.fillStyle = "#c0392b";
    ctx.fillRect(machineX + 7, machineY + 14, 9, 3);
    ctx.fillStyle = "#8d97a6";
    ctx.beginPath();
    ctx.arc(machineX + 10, machineY + 24, 2.4, 0, Math.PI * 2);
    ctx.arc(machineX + 18, machineY + 24, 2.4, 0, Math.PI * 2);
    ctx.fill();
    // Group head and spout, hanging over the worktop, with a cup under it.
    ctx.fillStyle = "#151a20";
    ctx.fillRect(machineX + 22, machineY + COFFEE_MACHINE_HEIGHT - 12, 28, 12);
    ctx.fillRect(machineX + 34, machineY + COFFEE_MACHINE_HEIGHT, 5, 6);
    ctx.fillStyle = "#e6e9ee";
    ctx.beginPath();
    ctx.roundRect(machineX + 31, topY - 14, 11, 8, 2);
    ctx.fill();

    // A stack of cups, and the carafe at the far end.
    ctx.fillStyle = "#dfe3ea";
    for (let i = 0; i < 2; i++) {
      ctx.beginPath();
      ctx.roundRect(r.x + 86 + i * 13, topY - 18, 11, 11, 2);
      ctx.fill();
    }
    // The carafe on its hotplate at the end of the run.
    ctx.fillStyle = "#2f3540";
    ctx.beginPath();
    ctx.roundRect(r.x + r.w - 26, topY - 25, 19, 18, 3);
    ctx.fill();
    ctx.strokeStyle = "#2f3540";
    ctx.lineWidth = 2.4;
    ctx.beginPath();
    ctx.arc(r.x + r.w - 5, topY - 16, 4.5, -Math.PI / 2, Math.PI / 2);
    ctx.stroke();
    ctx.fillStyle = "#6b4a35";
    ctx.fillRect(r.x + r.w - 24, topY - 14, 15, 6);
  }

  /** The fridge, standing against the same wall. */
  private drawFridge(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = fridgeRect(p, this.scratchProp);
    ctx.fillStyle = "#c3c9d2";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 4);
    ctx.fill();
    // The freezer split and two handles: what makes a white box a fridge.
    ctx.strokeStyle = "#9aa2ae";
    ctx.lineWidth = 1.5;
    ctx.beginPath();
    ctx.moveTo(r.x, r.y + FRIDGE_HEIGHT * 0.34);
    ctx.lineTo(r.x + r.w, r.y + FRIDGE_HEIGHT * 0.34);
    ctx.stroke();
    ctx.fillStyle = "#7d8492";
    ctx.fillRect(r.x + r.w - 10, r.y + 8, 3, 12);
    ctx.fillRect(r.x + r.w - 10, r.y + FRIDGE_HEIGHT * 0.34 + 8, 3, 16);
  }

  /**
   * The lunch table: a long surface with a place setting per bench spot.
   *
   * The settings are what make it a lunch table rather than a desk, and they
   * are drawn at the seats agents actually sit in, so a diner lines up with a
   * plate instead of with the middle of the wood.
   */
  private drawLunchTable(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = lunchRect(p, this.scratchProp);

    ctx.fillStyle = "#5c4835";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 6);
    ctx.fill();
    ctx.fillStyle = "#42331f";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y + r.h - 8, r.w, 8, 4);
    ctx.fill();
    // Boards along the length, drawn in the lighter grain colour.
    ctx.strokeStyle = "#6b5540";
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let i = 1; i < 3; i++) {
      const y = r.y + (LUNCH_HEIGHT / 3) * i;
      ctx.moveTo(r.x + 6, y);
      ctx.lineTo(r.x + r.w - 6, y);
    }
    ctx.stroke();

    // A lighter strip along the back edge: the difference between a table top
    // and a slab is that you can see the far edge of it.
    ctx.fillStyle = "#6e5741";
    ctx.fillRect(r.x + 4, r.y + 3, r.w - 8, 3);
    // Something in the middle of the table to eat off of.
    ctx.fillStyle = "#d9b26a";
    ctx.beginPath();
    ctx.roundRect(p.x - 26, r.y + 13, 52, 14, 3);
    ctx.fill();
    ctx.fillStyle = "#3f7a52";
    ctx.beginPath();
    ctx.roundRect(p.x + 40, r.y + 11, 9, 20, 3);
    ctx.fill();

    for (const seat of lunchSeats(p)) {
      // Chair, which the diner is then drawn sitting on.
      ctx.fillStyle = "#2a2f38";
      ctx.beginPath();
      ctx.roundRect(seat.x - 17, seat.y - 2, 34, 21, 5);
      ctx.fill();

      // Plate and cutlery, on the near edge of the table.
      const plateY = r.y + r.h - 17;
      ctx.fillStyle = "#dfe3ea";
      ctx.beginPath();
      ctx.arc(seat.x, plateY, 8, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = "#b9bfc9";
      ctx.beginPath();
      ctx.arc(seat.x, plateY, 4.5, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = "#9aa2ae";
      ctx.fillRect(seat.x + 11, plateY - 5, 2, 10);
    }
  }

  /** The meeting table, with chairs round it. */
  private drawMeetingTable(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = meetingRect(p, this.scratchProp);

    // Chairs first: they sit behind the table on the far side and are drawn
    // over by it, which is what puts the table between them.
    ctx.fillStyle = "#2a2f38";
    for (let i = 0; i < 3; i++) {
      const x = r.x + 30 + i * ((MEETING_WIDTH - 60) / 2);
      ctx.beginPath();
      ctx.roundRect(x - 15, r.y - 14, 30, 20, 4);
      ctx.fill();
      ctx.beginPath();
      ctx.roundRect(x - 15, r.y + r.h - 6, 30, 20, 4);
      ctx.fill();
    }

    ctx.fillStyle = "#33404f";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 26);
    ctx.fill();
    ctx.fillStyle = "#2a3542";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y + r.h - 10, r.w, 10, 10);
    ctx.fill();
    // A laptop and a couple of cups left on it.
    ctx.fillStyle = "#1c222b";
    ctx.beginPath();
    ctx.roundRect(p.x - 16, p.y - 12, 32, 22, 3);
    ctx.fill();
    ctx.fillStyle = "#4d5a6b";
    ctx.fillRect(p.x - 13, p.y - 9, 26, 13);
    ctx.fillStyle = "#dfe3ea";
    ctx.beginPath();
    ctx.arc(p.x - 34, p.y + 14, 5, 0, Math.PI * 2);
    ctx.arc(p.x + 36, p.y - 16, 5, 0, Math.PI * 2);
    ctx.fill();
  }

  /**
   * A whiteboard, hung on the meeting room's far wall.
   *
   * Wide enough to be a board: the first version was fourteen units across and
   * read as a scrollbar. It is drawn overlapping the wall, because that is
   * where a board hangs, and it is not an obstacle -- nobody can stand in a
   * wall to begin with.
   */
  private drawWhiteboard(ctx: CanvasRenderingContext2D): void {
    const room = ROOMS.find((r) => r.id === "meeting");
    if (!room) return;
    const w = 26;
    const h = 150;
    const x = room.x + room.w - w + 6;
    const y = room.y + 68;

    ctx.fillStyle = "#8b949f";
    ctx.beginPath();
    ctx.roundRect(x - 2, y - 2, w + 4, h + 4, 3);
    ctx.fill();
    ctx.fillStyle = "#eef1f6";
    ctx.fillRect(x, y, w, h);
    // Scribbles, which is what stops it looking like a window.
    ctx.strokeStyle = "#8892a0";
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    for (let i = 0; i < 6; i++) {
      const ly = y + 16 + i * 22;
      ctx.moveTo(x + 5, ly);
      ctx.lineTo(x + w - 6 - (i % 3) * 5, ly);
    }
    ctx.stroke();
    // A marker on the tray.
    ctx.fillStyle = "#c0392b";
    ctx.fillRect(x + 6, y + h + 1, 10, 3);
  }

  /** The printer, with a paper tray and a stack of output on top. */
  private drawPrinter(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = printerRect(p, this.scratchProp);
    ctx.fillStyle = "#333b47";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 4);
    ctx.fill();
    ctx.fillStyle = "#20262f";
    ctx.fillRect(r.x + 5, r.y + PRINTER_HEIGHT * 0.5, r.w - 10, 5);
    ctx.fillStyle = "#4fd1c5";
    ctx.fillRect(r.x + 6, r.y + 6, 6, 3);
    // Paper on the out-tray, which is what says printer and not bin.
    ctx.fillStyle = "#e8ebf0";
    ctx.beginPath();
    ctx.roundRect(r.x + 8, r.y - 5, r.w - 16, 7, 1);
    ctx.fill();
  }

  /** A low bookshelf along the back wall, with a row of spines. */
  private drawShelf(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = shelfRect(p, this.scratchProp);
    ctx.fillStyle = "#3b2f27";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 3);
    ctx.fill();
    const spines = ["#6ea8fe", "#5bc8a0", "#ef6f6c", "#c9a227", "#b18cf0", "#4fd1c5"];
    for (let i = 0; i < spines.length; i++) {
      ctx.fillStyle = spines[i];
      ctx.fillRect(r.x + 7 + i * 19, r.y + 5, 13, SHELF_HEIGHT - 12);
    }
    ctx.fillStyle = "#241d18";
    ctx.fillRect(r.x, r.y + r.h - 5, r.w, 5);
  }

  /** A potted plant. Cheap, and the corners stop looking like a warehouse. */
  private drawPlant(ctx: CanvasRenderingContext2D, p: Prop): void {
    ctx.fillStyle = "#6b4a35";
    ctx.beginPath();
    ctx.moveTo(p.x - 10, p.y + 2);
    ctx.lineTo(p.x + 10, p.y + 2);
    ctx.lineTo(p.x + 7, p.y + PLANT_RADIUS);
    ctx.lineTo(p.x - 7, p.y + PLANT_RADIUS);
    ctx.closePath();
    ctx.fill();

    ctx.fillStyle = "#3f7a52";
    for (let i = 0; i < 3; i++) {
      const angle = -Math.PI / 2 + (i - 1) * 0.7;
      ctx.beginPath();
      ctx.ellipse(
        p.x + Math.cos(angle) * 7,
        p.y - 6 + Math.sin(angle) * 6,
        6,
        10,
        angle + Math.PI / 2,
        0,
        Math.PI * 2,
      );
      ctx.fill();
    }
  }

  /** The ping pong table: a surface, a centre line and a net across it. */
  private drawPongTable(ctx: CanvasRenderingContext2D, p: Prop): void {
    const r = pongRect(p, this.scratchProp);

    ctx.fillStyle = "#1f4a58";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 4);
    ctx.fill();

    ctx.strokeStyle = "#3d7d8e";
    ctx.lineWidth = 1.2;
    ctx.beginPath();
    ctx.roundRect(r.x + 3, r.y + 3, r.w - 6, r.h - 6, 3);
    ctx.moveTo(r.x + 3, p.y);
    ctx.lineTo(r.x + r.w - 3, p.y);
    ctx.stroke();

    // The net, standing across the middle. Drawn light so it reads as mesh.
    ctx.fillStyle = "rgba(226,232,240,0.55)";
    ctx.fillRect(p.x - 1, r.y - 5, 2, PONG_HEIGHT + 10);
  }

  private draw(now: number): void {
    const ctx = this.ctx;
    if (this.canvas.width === 0) return;

    this.still = prefersReducedMotion();
    this.collectDirty();

    // Nothing changed, so nothing is drawn -- not even a blit. An empty dirty
    // list used to mean "repaint everything", which is the wrong way round: it
    // made the cheapest frame the most expensive one, and with movement turned
    // off every frame is that frame.
    if (!this.needsFullRepaint && this.dirtyCount === 0) {
      this.recordDrawn();
      return;
    }

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

    this.recordDrawn();
  }

  /** Remembers where everyone was drawn, so the next frame knows what to erase. */
  private recordDrawn(): void {
    for (const agent of this.agents) {
      agent.drawnX = agent.x;
      agent.drawnY = agent.y;
      agent.drawnState = agent.state;
      agent.drawnSpeech = agent.speech.text;
      agent.drawnSpeechWidth = agent.speech.width;
      agent.neverDrawn = false;
    }
    for (const bit of this.director.bits) bit.markDrawn();
  }

  /** Draws everything that moves: lit monitors and the agents themselves. */
  private drawScene(ctx: CanvasRenderingContext2D, now: number): void {
    ctx.font = LABEL_FONT;
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";

    const list = this.agents;
    for (let i = 0; i < list.length; i++) {
      const a = list[i];
      const hot = a.id === this.hoverId;
      if (isLit(a.state)) this.drawDesk(ctx, a, true, hot);
      // A dark desk is part of the static layer, so hovering one means drawing
      // it again on top -- the same desk, with the highlight added. Every desk
      // opens its owner's panel now, whether or not they are busy, so every
      // desk answers the pointer.
      else if (hot) this.drawDesk(ctx, a, false, true);
    }
    for (let i = 0; i < list.length; i++) this.drawAgent(ctx, list[i], now);

    // A ball in flight belongs over both players, whichever way the depth
    // sort put them.
    for (const bit of this.director.bits) {
      if (bit.showsBall) this.drawBall(ctx, bit.ballX, bit.ballY);
    }

    // Bubbles last, so a line is never half-covered by whoever walks in front
    // of the person saying it.
    for (let i = 0; i < list.length; i++) {
      if (list[i].speech.active) this.drawSpeech(ctx, list[i]);
    }
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
      // pulsing ring, a breathing bob, steam off a mug -- so it is dirty even
      // when still. Two agents talking are not: static bubbles change no
      // pixels once they are up.
      const animating = agent.animates;
      const spoke = agent.speech.text !== agent.drawnSpeech;

      if (!moved && !restyled && !animating && !spoke && !agent.neverDrawn) continue;

      this.addDirty(agent.boxAt(agent.drawnX, agent.drawnY, this.scratchA));
      if (moved) this.addDirty(agent.boxAt(agent.x, agent.y, this.scratchB));

      // A bubble is wider than the box under it, and the one being erased is
      // the one that was drawn -- at the width it was drawn at.
      if (agent.drawnSpeechWidth > 0) {
        this.addDirty(
          speechRect(agent.drawnX, agent.drawnY, agent.drawnSpeechWidth, this.scratchSpeech),
        );
      }
      if (agent.speech.active) {
        this.addDirty(speechRect(agent.x, agent.y, agent.speech.width, this.scratchSpeech));
      }

      if (restyled || isLit(agent.state)) {
        const desk = deskRect(agent.deskX, agent.deskY);
        this.addDirty(desk);
      }
    }

    // The ball moves every frame it exists, so its old and new positions are
    // both dirty; one rect covering the flight would be most of the table.
    for (const bit of this.director.bits) {
      if (bit.ballIsDirty) this.addDirty(bit.ballRect(this.scratchBall));
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
          if (!rectsOverlap(this.dirty[i], this.dirty[j])) continue;
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
   *
   * `hot` comes from the caller rather than being read off `hoverId` here,
   * because the same routine paints the static layer, which must never be told
   * about a pointer that will have moved on by the time it is next painted.
   */
  private drawDesk(
    ctx: CanvasRenderingContext2D,
    a: OfficeAgent,
    lit: boolean,
    hot: boolean,
  ): void {
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

    // Monitor, tinted with the owner's colour when they are working and
    // brightened further while the pointer is over it. A dark monitor lifts to
    // a neutral grey instead: colour in this office means somebody is doing
    // something, and a hover is not that. The highlight stays inside the
    // monitor's own outline so it needs no extra dirty rectangle.
    const m = monitorRect(a.deskX, a.deskY, this.scratchMonitor);
    ctx.fillStyle = hot ? (lit ? a.colour : "#2f3744") : "#151920";
    ctx.beginPath();
    ctx.roundRect(m.x, m.y, m.w, m.h, 4);
    ctx.fill();
    ctx.fillStyle = lit ? (hot ? a.colourSoft : a.colourDim) : hot ? "#252c37" : "#1d232c";
    ctx.beginPath();
    ctx.roundRect(m.x + MONITOR_BEZEL, m.y + MONITOR_BEZEL, m.w - MONITOR_BEZEL * 2, m.h - MONITOR_BEZEL * 2, 3);
    ctx.fill();

    // The readout belongs to a turn in progress rather than to a lit monitor:
    // a finished desk keeps its glow but stops reporting, the same as today.
    // `lit` also keeps it out of the static layer, which would bake it in.
    if (lit && a.state === "working" && a.tail.active) this.drawReadout(ctx, a, m, hot);

    if (!lit) {
      // Nameplate, which comes up to full strength under the pointer: the
      // thing a click opens is a person, so their name is the affordance.
      ctx.fillStyle = hot ? "#c2c9d6" : "#5f6878";
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
    if (column < this.monitorColumns && (this.still || Math.sin(a.clock * 5) > -0.2)) {
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
    // Sitting covers two different reasons for the same posture: at a desk
    // because there is work, and at the lunch table because there is not.
    const seated = a.state === "working" || a.state === "finished" || a.sitting;
    // Seated agents sit lower and behind the furniture's edge; others stand.
    const bodyY = seated ? a.y - 6 : a.y;
    const bob = this.still
      ? 0
      : seated
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

    // Head, or the agent's own face where their folder holds one.
    const headX = a.x + a.facing * 1.5;
    const headY = bodyY - bob - AGENT_RADIUS * 0.42;
    if (a.avatar) {
      this.drawPortrait(ctx, a, headX, headY);
    } else {
      ctx.fillStyle = a.colourSoft;
      ctx.beginPath();
      ctx.arc(headX, headY, AGENT_RADIUS * 0.52, 0, Math.PI * 2);
      ctx.fill();
    }

    // Whatever an off-duty agent picked up on the way.
    if (a.holding === "mug") this.drawMug(ctx, a, bodyY - bob);
    else if (a.holding === "paddle") this.drawPaddle(ctx, a, bodyY - bob);

    if (a.activity === "lunch" && a.sitting) this.drawEating(ctx, a, bodyY - bob);

    // Name tag, so an agent away from their own desk is still identifiable --
    // which at the lunch table they very much are.
    if (!seated || a.sitting) {
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
          this.still ? 1 : 0.45 + 0.55 * Math.abs(Math.sin(a.clock * 4)),
        );
        break;
    }
  }

  /**
   * An agent's avatar, in place of the head.
   *
   * A little larger than the head it replaces, because a face at head size is
   * twenty pixels of nothing, and clipped to a circle with a ring in the
   * agent's colour: the office is read by colour at a glance, and a portrait
   * has to stay the same person as the nameplate and the desk panel dot.
   *
   * Pictures are centre-cropped to a square rather than squashed into one, so
   * an avatar somebody grabbed at whatever aspect ratio still shows a face.
   */
  private drawPortrait(
    ctx: CanvasRenderingContext2D,
    a: OfficeAgent,
    x: number,
    y: number,
  ): void {
    const image = a.avatar;
    if (!image) return;
    const radius = AGENT_RADIUS * 0.66;
    const side = Math.min(image.naturalWidth, image.naturalHeight);

    ctx.save();
    ctx.beginPath();
    ctx.arc(x, y, radius, 0, Math.PI * 2);
    ctx.clip();
    ctx.drawImage(
      image,
      (image.naturalWidth - side) / 2,
      (image.naturalHeight - side) / 2,
      side,
      side,
      x - radius,
      y - radius,
      radius * 2,
      radius * 2,
    );
    ctx.restore();

    ctx.strokeStyle = a.colourSoft;
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    ctx.arc(x, y, radius, 0, Math.PI * 2);
    ctx.stroke();
  }

  /**
   * A mug in the hand of whoever is on a coffee run, with steam off it.
   *
   * The steam is the only reason a standing agent with a mug is repainted at
   * all (see OfficeAgent's `animates`), and it is two short strokes on the
   * agent's own clock so the office is not breathing in unison.
   */
  private drawMug(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    const x = a.x + a.facing * AGENT_RADIUS * 1.05;
    const y = top + AGENT_RADIUS * 0.6;

    // Bigger than it was. A mug is the whole visible result of a coffee run
    // and it has to read at a glance from across a room drawn at half scale,
    // so it gets a pale body, a dark rim and a handle that sticks out.
    ctx.fillStyle = "#eef1f6";
    ctx.beginPath();
    ctx.roundRect(x - 4.5, y - 6, 9, 11, 2);
    ctx.fill();
    ctx.fillStyle = "#3b2f27";
    ctx.fillRect(x - 4.5, y - 6, 9, 2.5);
    ctx.strokeStyle = "#eef1f6";
    ctx.lineWidth = 1.6;
    ctx.beginPath();
    ctx.arc(x + a.facing * 5.5, y - 1, 2.6, -Math.PI / 2, Math.PI / 2);
    ctx.stroke();

    ctx.strokeStyle = "rgba(238,241,246,0.45)";
    ctx.lineWidth = 1.1;
    ctx.beginPath();
    for (let i = 0; i < 2; i++) {
      const drift = this.still ? 0 : Math.sin(a.clock * 2.2 + i * 1.7) * 1.8;
      ctx.moveTo(x - 2 + i * 4, y - 8);
      ctx.lineTo(x - 2 + i * 4 + drift, y - 14);
    }
    ctx.stroke();
  }

  /**
   * Eating: a forkful going up to the mouth and back down to the plate.
   *
   * One stroke and one dot. The plate is already painted into the table under
   * them, so the only thing that has to move is the hand -- which is the whole
   * difference between somebody sat at a table and somebody stood still.
   */
  private drawEating(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    // Slow, and paused at the plate: a smooth sine reads as a metronome.
    const swing = this.still ? 0 : Math.max(0, Math.sin(a.clock * 2.1));
    const handX = a.x + a.facing * (AGENT_RADIUS * 0.55);
    const plateY = top + AGENT_RADIUS * 1.15;
    const mouthY = top - AGENT_RADIUS * 0.2;
    const handY = plateY + (mouthY - plateY) * swing;

    ctx.strokeStyle = a.colourSoft;
    ctx.lineWidth = 1.6;
    ctx.beginPath();
    ctx.moveTo(a.x + a.facing * (AGENT_RADIUS * 0.5), top + AGENT_RADIUS * 0.7);
    ctx.lineTo(handX, handY);
    ctx.stroke();

    ctx.fillStyle = "#d9b26a";
    ctx.beginPath();
    ctx.arc(handX, handY - 1, 2, 0, Math.PI * 2);
    ctx.fill();
  }

  /** A bat, held up and waiting, for whoever is at the table. */
  private drawPaddle(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    const x = a.x + a.facing * AGENT_RADIUS * 1.05;
    // Small ready-position bob, so a player waiting for the ball is not a
    // statue holding a bat.
    const y = top + AGENT_RADIUS * 0.35 + (this.still ? 0 : Math.sin(a.clock * 6) * 1.4);

    ctx.strokeStyle = "#c9a227";
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    ctx.moveTo(x - a.facing * 2, y + 4);
    ctx.lineTo(x, y + 1);
    ctx.stroke();

    ctx.fillStyle = "#c0392b";
    ctx.beginPath();
    ctx.ellipse(x, y - 2.5, 3.2, 4, 0, 0, Math.PI * 2);
    ctx.fill();
  }

  /** The ball, mid-flight. */
  private drawBall(ctx: CanvasRenderingContext2D, x: number, y: number): void {
    ctx.fillStyle = "#f4f6fa";
    ctx.beginPath();
    ctx.arc(x, y, 2.4, 0, Math.PI * 2);
    ctx.fill();
  }

  /**
   * Says a line over an agent's head.
   *
   * The width is measured here, once, because measuring text is the one thing
   * the draw loop may not do -- and because the frame a bubble vanishes has to
   * erase it at the width it was drawn.
   */
  private speak(agent: OfficeAgent, text: string, seconds = SPEECH_SECONDS): void {
    agent.speech.say(text, this.speechWidth(text), seconds);
  }

  private speechWidth(text: string): number {
    const ctx = this.ctx;
    ctx.font = SPEECH_FONT;
    const width = ctx.measureText(text).width + SPEECH_PAD_X * 2;
    // Handed back the way the rest of the scene expects it.
    ctx.font = LABEL_FONT;
    return width;
  }

  /**
   * One spoken line: a light bubble with a tail, outlined in the speaker's own
   * colour so it is obvious who said it without a second nameplate.
   *
   * Light on dark deliberately. The office is nearly black and the type is
   * eleven world units, which is a handful of pixels once the room is scaled
   * into a window -- dark text on a pale bubble is the only version of this
   * that stays legible at that size.
   */
  private drawSpeech(ctx: CanvasRenderingContext2D, a: OfficeAgent): void {
    const width = a.speech.width;
    const r = speechRect(a.x, a.y, width, this.scratchSpeech);

    ctx.fillStyle = "#e9edf5";
    ctx.strokeStyle = a.colourSoft;
    ctx.lineWidth = 1.2;
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, width, SPEECH_HEIGHT, 6);
    ctx.fill();
    ctx.stroke();

    // The tail, pointing back down at whoever is talking.
    const tailY = r.y + SPEECH_HEIGHT;
    ctx.fillStyle = "#e9edf5";
    ctx.beginPath();
    ctx.moveTo(a.x - 4, tailY - 1);
    ctx.lineTo(a.x + 4, tailY - 1);
    ctx.lineTo(a.x + a.facing * 2, tailY + 5);
    ctx.closePath();
    ctx.fill();

    ctx.font = SPEECH_FONT;
    ctx.fillStyle = "#161a21";
    ctx.fillText(a.speech.text, a.x, r.y + SPEECH_HEIGHT / 2 + 0.5);
    ctx.font = LABEL_FONT;
  }

  private drawTypingDots(ctx: CanvasRenderingContext2D, a: OfficeAgent, now: number): void {
    const baseY = a.y - AGENT_RADIUS * 1.9;
    const phase = now / 220;
    for (let i = 0; i < 3; i++) {
      // Held still, the dots stay a row of three: the sign that somebody is
      // mid-turn, without the hopping.
      const lift = this.still ? 0 : Math.max(0, Math.sin(phase - i * 0.7)) * 3;
      ctx.fillStyle = !this.still && i === Math.floor(phase % 3) ? a.colourSoft : a.colourDim;
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
 * A room's floor colour.
 *
 * Shared by the floors and the doorways, because a doorway is floor carried
 * through a wall and the two must not drift apart.
 */
function floorColour(room: (typeof ROOMS)[number]["id"]): string {
  if (room === "break") return "#1e1b19";
  if (room === "meeting") return "#171a21";
  return "#171b22";
}

/** True when the agent's monitor should be tinted. */
function isLit(state: VisualState): boolean {
  return state === "working" || state === "finished";
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
