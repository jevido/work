import { OfficeAgent, type VisualState } from "./agent";
import { avatarImage } from "./avatars";
import { HandoffDirector, TRAY_MAX, type TaskPhase } from "./handoff";
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
  chairRect,
  coffeeRect,
  fridgeRect,
  furnitureObstacles,
  lunchRect,
  lunchSeats,
  meetingRect,
  placeProps,
  pongRect,
  printerRect,
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
  DOORWAYS,
  FLOOR_TOP,
  IDLE_FPS,
  MONITOR_BEZEL,
  MONITOR_HEIGHT,
  MONITOR_LINE_HEIGHT,
  MONITOR_RISE,
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
  deskHeight,
  deskObstacle,
  deskRect,
  deskWidth,
  monitorRect,
  prefersReducedMotion,
  rectsOverlap,
  type Rect,
} from "./world";

/** What the bridge hands the renderer: one agent, one new coarse state. */
export interface AgentCommand {
  agentId: string;
  state: VisualState;
  /**
   * Which part of a run the change belongs to, when the event carried one.
   *
   * The only thing the office does with it is decide whether an assignment is
   * work somebody hands over -- Anton's own planning and synthesis turns are
   * his own, and nobody brings you your own paperwork. See handoff.ts.
   */
  phase?: TaskPhase;
}

/**
 * One card from the task board, reduced to what a note on a wall can hold.
 *
 * The office does not get the board itself. A card carries a run id, an
 * assignee, a body and an id you can quote at Anton, none of which survives
 * being drawn sixty-four units wide -- so the component picks the cards worth
 * pinning up and hands over the two fields that fit.
 */
export interface BoardNote {
  title: string;
  status: "todo" | "doing" | "done" | "blocked";
}

/** How many notes fit on the board behind the coordinator's desk. */
export const BOARD_NOTES = 8;

/** The colour a note is written on. Same language as the board in the panel. */
const NOTE_COLOURS: Record<BoardNote["status"], string> = {
  todo: "#cfd6e2",
  doing: "#f2b544",
  done: "#5bc8a0",
  blocked: "#ef6f6c",
};

export interface AgentSpec {
  id: string;
  name: string;
  colour: string;
  deskX: number;
  deskY: number;
  seatX: number;
  seatY: number;
  /**
   * True for the coordinator. The office draws them a desk of their own --
   * see BOSS_DESK_WIDTH in world.ts -- and hangs the task board behind it.
   */
  boss?: boolean;
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

/**
 * Where finished folders stack on the coordinator's in-tray, relative to the
 * centre of his desk.
 *
 * The tray itself is painted into the desk (see drawBossSurface), so these are
 * that prop's numbers read off it: a folder lands on the paper already there
 * and each one after sits a little higher.
 */
const TRAY_DX = 62;
const TRAY_DY = -30;
const TRAY_FOLDER_W = 30;
const TRAY_FOLDER_H = 9;
const TRAY_STEP = 3.5;

/** Left and right, for the symmetric limbs. Module-level: the loop allocates nothing. */
const SIDES = [-1, 1] as const;

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
  /**
   * The coordinator, or null in an office without one.
   *
   * Held rather than searched for because the draw path wants it -- the board
   * over his desk, the chair in front of it, the folders on his tray -- and
   * `find` in there means a closure allocated on every dirty patch of every
   * frame. It changes when the cast does and never otherwise.
   */
  private boss: OfficeAgent | null = null;
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

  /**
   * How work moves through the office: folders carried to desks and back, and
   * Anton's chair pulled round while he waits for them.
   *
   * Given the same speaking callback as the idle director, and for the same
   * reason -- a bubble's width has to be measured, and this is the only object
   * that owns a canvas context.
   */
  private readonly handoff = new HandoffDirector((agent, text, seconds) =>
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
  /** Scratch for the coordinator's chair and the folders on his in-tray. */
  private readonly scratchChair: Rect = { x: 0, y: 0, w: 0, h: 0 };
  private readonly scratchTray: Rect = { x: 0, y: 0, w: 0, h: 0 };

  /** The agent whose monitor the pointer is over, if any. */
  private hoverId: string | null = null;

  /**
   * What is pinned to the board behind the coordinator's desk, and a key of
   * what was last drawn.
   *
   * The board is part of the static layer -- it changes a handful of times a
   * run, not a handful of times a second -- so a change repaints that layer
   * rather than joining the per-frame dirty-rect work. The key is what keeps
   * an unchanged snapshot from doing so: the backend republishes the whole
   * board on every edit, including the edits that touch nothing we draw.
   */
  private boardNotes: readonly BoardNote[] = [];
  private boardKey = "";

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
    this.boss = this.agents.find((a) => a.boss) ?? null;
    this.obstacles = specs.map((s) => deskObstacle(s.deskX, s.deskY, s.boss ?? false));
    // The desks are laid out from the roster, so where a ping pong table fits
    // is only knowable once they are placed. Everything solid goes in before
    // the agents are told what to walk around, and the router works from the
    // same list, so nobody plans a route through drawn furniture.
    this.props = placeProps(this.obstacles);
    this.obstacles.push(...furnitureObstacles(this.props));
    for (const agent of this.agents) agent.setObstacles(this.obstacles);
    this.director.setScene(this.agents, this.obstacles, this.props);
    this.handoff.setScene(this.agents, this.obstacles);
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

  /**
   * Pins the board's cards up behind the coordinator's desk.
   *
   * Given in the order they should hang, and trimmed here to what the cork
   * holds -- which of a long board's cards are worth showing is a decision
   * about the work, so the component that has the board makes it.
   */
  setBoardNotes(notes: readonly BoardNote[]): void {
    const trimmed = notes.slice(0, BOARD_NOTES);
    const key = trimmed.map((n) => `${n.status}\u0001${n.title}`).join("\u0000");
    if (key === this.boardKey) return;
    this.boardKey = key;
    this.boardNotes = trimmed;
    this.paintLayer();
    this.needsFullRepaint = true;
    if (!this.running) this.draw(performance.now());
  }

  /** Queues a coarse state change. Cheap enough to call from an event handler. */
  push(agentId: string, state: VisualState, phase?: TaskPhase): void {
    this.queue.push({ agentId, state, phase });
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
      const r = monitorRect(a.deskX, a.deskY, a.boss, this.scratchHit);
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
          // Work Anton hands over in person is not a walk to a desk yet: the
          // agent stands by while he brings it, and says their line when the
          // folder is actually in their hands. Everything else -- his own
          // turns, an office with no coordinator, movement turned off -- falls
          // through to the walk this always was.
          if (this.handoff.claim(agent, cmd.phase)) break;
          // Anton's own turn, while folders are still coming back to him. He
          // stays in his chair until they land: the alternative is a
          // coordinator writing up reports he has visibly not been handed.
          if (this.handoff.defer(agent, "assign")) break;
          agent.assign();
          // Anton has just handed them the task. They answer before they sit
          // down -- the line is canned, so this costs nothing but a bubble.
          this.speak(agent, dispatchLine());
          break;
        case "working":
          // Held back while the work is still crossing the room, and paid out
          // the moment it changes hands. Nobody starts on a brief they have
          // not been given.
          if (this.handoff.defer(agent, "work")) break;
          agent.work();
          break;
        case "finished":
          this.handoff.cancelDeferred(agent);
          agent.finish();
          break;
        case "error":
          this.handoff.cancelDeferred(agent);
          agent.fail();
          break;
        case "idle":
          this.handoff.cancelDeferred(agent);
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
    // After the agents, so a director reads the standing-still that this
    // frame produced rather than waiting for the next one. The handoff goes
    // first: the frame a task ends is the frame the agent becomes claimable by
    // both directors, and somebody with Anton's paperwork still under their
    // arm is not free for a game of ping pong.
    this.handoff.update(dt);
    this.director.update(dt);
    // A ball in the air is the one idle thing small and fast enough to strobe
    // at the idle rate. It lasts a few seconds, twice a minute at worst. A
    // chair being pushed round a desk is the same case.
    this.busy = busy || this.director.wantsSmoothFrames || this.handoff.wantsSmoothFrames;

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
    // Before the desks, because it hangs on the wall behind one of them.
    const boss = this.agents.find((a) => a.boss);
    if (boss) this.drawTaskBoard(ctx, boss);
    // Never hot: a highlight baked into the layer would outlive the pointer
    // that caused it, and only another paintLayer would take it off again.
    for (const agent of this.agents) this.drawDesk(ctx, agent, false, false);

    // The furniture nobody works at. Static shapes with nothing that changes,
    // so the office pays for these once and never again.
    this.drawCoffeeStation(ctx, this.props.coffee);
    this.drawFridge(ctx, this.props.fridge);
    this.drawLunchTable(ctx, this.props.lunch);
    this.drawMeetingTable(ctx, this.props.meeting);
    this.drawTv(ctx);
    this.drawPrinter(ctx, PRINTER);
    for (const plant of PLANTS) this.drawPlant(ctx, plant);
    if (this.props.pong) this.drawPongTable(ctx, this.props.pong);
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
   * The task board, on the wall behind the coordinator's desk.
   *
   * This is the board the panel at the top of the window shows, pinned up in
   * the room the work happens in. It replaces the shelf that used to stand on
   * this wall: a row of coloured spines that read as cards on a board anyway,
   * which is a poor thing for a piece of scenery to do next to a real one.
   *
   * Cork and paper on purpose. Every other surface in this office is blue-grey
   * and every note here is a light colour on a warm ground, which is what makes
   * it findable from across the room before a single title is legible -- and
   * the colours are the four the board in the panel already uses, so the two
   * agree on what amber means.
   *
   * It hangs over the back wall and stops above the monitor. If the desk ever
   * moves close enough to the wall that it would not, the board shrinks to fit
   * and then gives up, rather than being drawn through the screen in front of
   * it.
   */
  private drawTaskBoard(ctx: CanvasRenderingContext2D, boss: OfficeAgent): void {
    const w = 300;
    const x = boss.deskX - w / 2;
    const top = FLOOR_TOP - 12;
    const monitorTop = boss.deskY - deskHeight(true) / 2 - MONITOR_RISE;
    const h = Math.min(62, monitorTop - 4 - top);
    if (h < 34) return;

    ctx.fillStyle = "rgba(0,0,0,0.35)";
    ctx.beginPath();
    ctx.roundRect(x + 2, top + 3, w, h, 4);
    ctx.fill();

    // Frame.
    ctx.fillStyle = "#4a3a2a";
    ctx.beginPath();
    ctx.roundRect(x, top, w, h, 4);
    ctx.fill();
    ctx.fillStyle = "#634d36";
    ctx.beginPath();
    ctx.roundRect(x, top, w, 2, 2);
    ctx.fill();

    // Cork, with a fixed speckle. Fixed because this is repainted whenever the
    // board changes, and a random one would crawl.
    const fx = x + 5;
    const fy = top + 5;
    const fw = w - 10;
    const fh = h - 10;
    ctx.fillStyle = "#a9784a";
    ctx.fillRect(fx, fy, fw, fh);
    ctx.fillStyle = "rgba(90,58,32,0.35)";
    for (let i = 0; i < 40; i++) {
      const px = fx + ((i * 137) % (fw - 4)) + 2;
      const py = fy + ((i * 61) % (fh - 4)) + 2;
      ctx.fillRect(px, py, 1.6, 1.6);
    }

    if (this.boardNotes.length === 0) {
      // An empty board says so. The alternative -- bare cork -- looks the same
      // as a board whose notes failed to arrive.
      ctx.font = "600 10px Inter, system-ui, sans-serif";
      ctx.textAlign = "center";
      ctx.fillStyle = "#5b3a16";
      ctx.fillText("No tasks yet", x + w / 2, top + h / 2);
      ctx.font = LABEL_FONT;
      return;
    }

    // Four across, two down, which is what fits at a size the titles survive.
    const cols = 4;
    const cellW = fw / cols;
    const cellH = fh / 2;
    const noteW = cellW - 6;
    const noteH = cellH - 5;
    const chars = Math.max(3, Math.floor((noteW - 8) / this.monitorAdvance));

    ctx.font = `${MONITOR_TEXT_SIZE}px ${MONO_FONT}`;
    ctx.textAlign = "left";
    ctx.textBaseline = "middle";

    for (let i = 0; i < this.boardNotes.length; i++) {
      const note = this.boardNotes[i];
      const nx = fx + (i % cols) * cellW + 3;
      const ny = fy + Math.floor(i / cols) * cellH + 2.5;

      ctx.fillStyle = "rgba(0,0,0,0.25)";
      ctx.beginPath();
      ctx.roundRect(nx + 1, ny + 1.5, noteW, noteH, 1);
      ctx.fill();

      ctx.fillStyle = NOTE_COLOURS[note.status];
      ctx.beginPath();
      ctx.roundRect(nx, ny, noteW, noteH, 1);
      ctx.fill();

      // The pin, which is the difference between a note and a swatch.
      ctx.fillStyle = "rgba(0,0,0,0.3)";
      ctx.beginPath();
      ctx.arc(nx + noteW / 2, ny + 2.6, 1.3, 0, Math.PI * 2);
      ctx.fill();

      // Dark ink on every one of the four note colours, all of which are light.
      ctx.fillStyle = "#23292f";
      ctx.fillText(clip(note.title, chars), nx + 4, ny + noteH * 0.68);
    }

    // Two screws, so it reads as mounted rather than floating.
    ctx.fillStyle = "#2b2118";
    ctx.beginPath();
    ctx.arc(x + 6, top + h / 2, 1.6, 0, Math.PI * 2);
    ctx.arc(x + w - 6, top + h / 2, 1.6, 0, Math.PI * 2);
    ctx.fill();

    ctx.font = LABEL_FONT;
    ctx.textAlign = "center";
  }

  /**
   * The meeting room's screen, hung on the wall the seats face.
   *
   * It used to be a whiteboard on the side wall: a tall pale slab, seen almost
   * edge-on, which at this scale read as a stripe of nothing. Two things fix
   * that and they are both about where it is rather than how it is shaded. It
   * moved to the partition wall, which is the one the meeting spots look at and
   * the only wall in that room whose face you can see; and it is landscape now,
   * because the silhouette is most of what says "screen" before any detail is
   * legible.
   *
   * The rest is what a dark panel needs to read as glass rather than as a hole:
   * a thick bezel with a lit top edge, glass that is darkest at the bottom, one
   * diagonal reflection across it, and a standby light. It hangs over the wall
   * and a little over the room, so anybody standing in front of it is drawn on
   * top -- which is the right way round, and free, because the agents are drawn
   * after this layer.
   */
  private drawTv(ctx: CanvasRenderingContext2D): void {
    const room = ROOMS.find((r) => r.id === "meeting");
    if (!room) return;

    const w = 132;
    const h = 54;
    const x = room.x + room.w / 2 - w / 2;
    // Straddling the partition: the top of the frame sits on the wall, the rest
    // hangs into the room it is watched from.
    const y = room.y - WALL / 2 + 3;

    // Bezel.
    ctx.fillStyle = "#15181e";
    ctx.beginPath();
    ctx.roundRect(x, y, w, h, 4);
    ctx.fill();
    // The lit top edge, which is what stops the bezel merging into the wall.
    ctx.fillStyle = "#3d4552";
    ctx.beginPath();
    ctx.roundRect(x, y, w, 2.5, 2);
    ctx.fill();

    // Glass. A vertical gradient: brighter at the top where a screen catches
    // the room's light, near-black at the bottom.
    const bezel = 5;
    const gx = x + bezel;
    const gy = y + bezel;
    const gw = w - bezel * 2;
    const gh = h - bezel * 2 - 3;
    const glass = ctx.createLinearGradient(0, gy, 0, gy + gh);
    glass.addColorStop(0, "#2b4056");
    glass.addColorStop(1, "#0c1016");
    ctx.fillStyle = glass;
    ctx.beginPath();
    ctx.roundRect(gx, gy, gw, gh, 2);
    ctx.fill();

    // One diagonal highlight, clipped to the glass. A sheet of reflection is
    // the cheapest thing that reads as a hard shiny surface.
    ctx.save();
    ctx.beginPath();
    ctx.roundRect(gx, gy, gw, gh, 2);
    ctx.clip();
    ctx.fillStyle = "rgba(226,232,240,0.07)";
    ctx.beginPath();
    ctx.moveTo(gx, gy + gh);
    ctx.lineTo(gx + gw * 0.42, gy);
    ctx.lineTo(gx + gw * 0.66, gy);
    ctx.lineTo(gx + gw * 0.24, gy + gh);
    ctx.closePath();
    ctx.fill();
    ctx.restore();

    // The chin, and the standby light on it. A single warm dot is the last
    // thing that settles which way round the panel is.
    ctx.fillStyle = "#1b1f26";
    ctx.fillRect(x + bezel, y + h - bezel - 3, gw, 3);
    ctx.fillStyle = "#4fd1c5";
    ctx.beginPath();
    ctx.arc(x + w / 2, y + h - 5.5, 1.1, 0, Math.PI * 2);
    ctx.fill();
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

        // Clipped to the patch the blit actually erased, in the same device
        // pixels, rather than to the world rectangle it came from. The two are
        // not the same: the blit is floored and rounded up with two pixels of
        // slop, so a world-space clip leaves a hairline border that has been
        // wiped and is then not allowed to be redrawn. Anything static under
        // that border loses a column of itself, and a rectangle that travels --
        // the ball across the pong table, the coordinator's chair along his
        // desk -- takes one column per frame and leaves whatever it passed over
        // drawn as a picket fence. Clipping first and setting the world
        // transform after is safe: a clip is resolved against the transform in
        // force when it is declared, so this is a device-space region either
        // way.
        ctx.save();
        ctx.beginPath();
        ctx.rect(dx, dy, dw, dh);
        ctx.clip();
        this.setWorldTransform();
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
      agent.drawnSitting = agent.sitting;
      agent.drawnSpeech = agent.speech.text;
      agent.drawnSpeechWidth = agent.speech.width;
      agent.neverDrawn = false;
    }
    for (const bit of this.director.bits) bit.markDrawn();
    this.handoff.markDrawn();
  }

  /** Draws everything that moves: lit monitors and the agents themselves. */
  private drawScene(ctx: CanvasRenderingContext2D, now: number): void {
    ctx.font = LABEL_FONT;
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";

    // The coordinator's chair, under whoever is sitting on it and in front of
    // the desk it belongs to. Drawn here rather than baked into the static
    // layer because it is the one piece of furniture in this office that moves.
    if (this.handoff.showsChair) {
      this.drawChair(ctx, this.handoff.chairX, this.handoff.chairY);
    }

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
    // Finished work, back where it came from. On the desk rather than in the
    // agents' pass, so anybody standing in front of the desk is drawn over it.
    if (this.handoff.tray > 0 && this.boss) {
      this.drawReturned(ctx, this.boss, this.handoff.tray);
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
      const restyled =
        agent.state !== agent.drawnState || agent.sitting !== agent.drawnSitting;
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
        const desk = deskRect(agent.deskX, agent.deskY, agent.boss);
        this.addDirty(desk);
      }
    }

    // The ball moves every frame it exists, so its old and new positions are
    // both dirty; one rect covering the flight would be most of the table.
    for (const bit of this.director.bits) {
      if (bit.ballIsDirty) this.addDirty(bit.ballRect(this.scratchBall));
    }

    // The chair only costs a rectangle on the frames it is actually moving:
    // parked, it sits inside whoever is on it, whose own box already covers
    // it. The in-tray is the same bargain, a folder at a time.
    if (this.handoff.chairIsDirty) this.addDirty(this.handoff.chairSweep(this.scratchChair));
    if (this.handoff.trayIsDirty) this.addDirty(this.trayRect(this.scratchTray));

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
    const w = deskWidth(a.boss);
    const h = deskHeight(a.boss);
    const x = a.deskX - w / 2;
    const y = a.deskY - h / 2;

    if (!lit) {
      if (a.boss) {
        this.drawBossSurface(ctx, x, y, w, h);
      } else {
        // Surface.
        ctx.fillStyle = "#2a313d";
        ctx.beginPath();
        ctx.roundRect(x, y, w, h, 7);
        ctx.fill();

        // Front edge, for a hint of thickness.
        ctx.fillStyle = "#222833";
        ctx.beginPath();
        ctx.roundRect(x, y + h - 9, w, 9, 5);
        ctx.fill();
      }
    }

    // Monitor, tinted with the owner's colour when they are working and
    // brightened further while the pointer is over it. A dark monitor lifts to
    // a neutral grey instead: colour in this office means somebody is doing
    // something, and a hover is not that. The highlight stays inside the
    // monitor's own outline so it needs no extra dirty rectangle.
    const m = monitorRect(a.deskX, a.deskY, a.boss, this.scratchMonitor);
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
      //
      // The coordinator's sits at the front of the desk on a plate with a
      // brass edge, because the middle of that desk is a writing pad. Dark
      // plate rather than a brass one: the two text colours below are the same
      // pair every other desk uses, and they only hold their contrast against
      // something dark.
      const plateY = a.deskY + 17;
      if (a.boss) {
        ctx.fillStyle = "#1d232c";
        ctx.beginPath();
        ctx.roundRect(a.deskX - 46, plateY - 8, 92, 16, 2);
        ctx.fill();
        ctx.strokeStyle = "#b4894a";
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.roundRect(a.deskX - 46, plateY - 8, 92, 16, 2);
        ctx.stroke();
      }
      ctx.fillStyle = hot ? "#c2c9d6" : "#5f6878";
      ctx.fillText(a.name, a.deskX, a.boss ? plateY + 1 : a.deskY + 2);
    }
  }

  /**
   * The coordinator's desk: walnut, bow-fronted, on two drawer pedestals, with
   * a writing pad, a lamp and a tray of paper on it.
   *
   * Not the specialists' desk in another colour. It is a different shape -- the
   * front edge curves, the ends stand on visible pedestals -- and it is the
   * only wooden thing left in an office of blue-grey steel, which is what
   * carries "this desk is not like the others" at the size the whole floor is
   * drawn at. The lamp and the paper are there because a desk with nothing on
   * it reads as unoccupied, and this one is where the work is handed out.
   *
   * All of it is static, so it is painted into the background layer once. The
   * monitor and the nameplate are the caller's, drawn on top.
   */
  private drawBossSurface(
    ctx: CanvasRenderingContext2D,
    x: number,
    y: number,
    w: number,
    h: number,
  ): void {
    // Bow front: square at the wall, generous at the front, which is the whole
    // silhouette difference from the desks in the rows.
    const bow: [number, number, number, number] = [5, 5, 38, 38];

    ctx.fillStyle = "rgba(0,0,0,0.3)";
    ctx.beginPath();
    ctx.roundRect(x + 2, y + 5, w, h, bow);
    ctx.fill();

    ctx.fillStyle = "#4a3728";
    ctx.beginPath();
    ctx.roundRect(x, y, w, h, bow);
    ctx.fill();

    // Front edge and the brass line above it: the thickness of a good top.
    ctx.fillStyle = "#33261b";
    ctx.beginPath();
    ctx.roundRect(x, y + h - 12, w, 12, [0, 0, 38, 38]);
    ctx.fill();
    ctx.fillStyle = "#b4894a";
    ctx.fillRect(x + 14, y + h - 13.5, w - 28, 1.5);

    // Inlay, inset from the whole outline so it follows the bow.
    ctx.strokeStyle = "#7d6042";
    ctx.lineWidth = 1.2;
    ctx.beginPath();
    ctx.roundRect(x + 4, y + 3, w - 8, h - 18, [3, 3, 30, 30]);
    ctx.stroke();

    // The two pedestals, drawn on the front band at either end. Two grooves
    // and a brass pull each is enough to read as drawers.
    for (const px of [x + 9, x + w - 63]) {
      ctx.fillStyle = "#3a2b1f";
      ctx.beginPath();
      ctx.roundRect(px, y + h - 30, 54, 29, 3);
      ctx.fill();
      ctx.fillStyle = "#241a12";
      ctx.fillRect(px + 4, y + h - 20, 46, 1.4);
      ctx.fillRect(px + 4, y + h - 10, 46, 1.4);
      ctx.fillStyle = "#b4894a";
      ctx.fillRect(px + 19, y + h - 25, 16, 2);
      ctx.fillRect(px + 19, y + h - 15, 16, 2);
    }

    // Green leather blotter with a keyboard on it. The first version of this
    // was a near-black pad, which sat directly under a near-black monitor and
    // read as a second screen lying face up on the desk. Green solves that by
    // not being the colour of anything else here, and the keyboard settles
    // which way round the desk faces.
    ctx.fillStyle = "#2f4a3d";
    ctx.beginPath();
    ctx.roundRect(x + w / 2 - 54, y + 6, 108, 28, 2);
    ctx.fill();
    ctx.strokeStyle = "#8a6c45";
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.roundRect(x + w / 2 - 51, y + 9, 102, 22, 1);
    ctx.stroke();

    ctx.fillStyle = "#171b21";
    ctx.beginPath();
    ctx.roundRect(x + w / 2 - 30, y + 14, 60, 13, 2);
    ctx.fill();
    ctx.fillStyle = "#4d5a6b";
    ctx.fillRect(x + w / 2 - 26, y + 17, 52, 3);
    ctx.fillRect(x + w / 2 - 26, y + 22, 52, 2);
    // Mouse.
    ctx.fillStyle = "#3f4a59";
    ctx.beginPath();
    ctx.ellipse(x + w / 2 + 40, y + 21, 3.5, 5, 0, 0, Math.PI * 2);
    ctx.fill();

    // A banker's lamp on the far side, lit. A cone on a stalk was the first
    // attempt and read as a mushroom; the shape that says "lamp" at this size
    // is the wide dome with light spilling out from under it, so that is what
    // this draws -- glass shade, brass stem, brass foot, and a warm patch of
    // desk under it.
    const lampX = x + 34;
    const lampY = y + 22;
    ctx.fillStyle = "rgba(242,181,68,0.13)";
    ctx.beginPath();
    ctx.ellipse(lampX, lampY + 5, 12, 6, 0, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#8a6c45";
    ctx.beginPath();
    ctx.roundRect(lampX - 9, lampY + 7, 18, 3, 1.5);
    ctx.fill();
    ctx.fillRect(lampX - 1, lampY - 4, 2, 11);
    ctx.fillStyle = "#2f4a3d";
    ctx.beginPath();
    ctx.ellipse(lampX, lampY - 4, 13, 6, 0, Math.PI, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#b4894a";
    ctx.fillRect(lampX - 13, lampY - 5, 26, 1.6);

    // A tray of paper and a pot of pens on the near side.
    ctx.fillStyle = "#cfd6e2";
    ctx.beginPath();
    ctx.roundRect(x + w - 52, y + 14, 34, 15, 1);
    ctx.fill();
    ctx.fillStyle = "#e8ebf0";
    ctx.beginPath();
    ctx.roundRect(x + w - 49, y + 11, 34, 15, 1);
    ctx.fill();
    ctx.fillStyle = "#4d5a6b";
    ctx.beginPath();
    ctx.arc(x + w - 26, y + 33, 5, 0, Math.PI * 2);
    ctx.fill();
    ctx.strokeStyle = "#b4894a";
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    ctx.moveTo(x + w - 28, y + 31);
    ctx.lineTo(x + w - 30, y + 23);
    ctx.moveTo(x + w - 24, y + 31);
    ctx.lineTo(x + w - 21, y + 24);
    ctx.stroke();
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
    // Only somebody the dirty-rect pass repaints every frame is allowed to
    // oscillate. That is the whole contract this renderer runs on, and the
    // standing idle breath below quietly broke it: it ran for every idle agent,
    // moved them by under a pixel, and was never marked dirty for -- so an
    // agent stood still was redrawn a clip-column at a time, at a different
    // offset per column, whenever somebody else's rectangle swept across them.
    // It painted them as a picket fence. Holding still costs nothing and is
    // the same bargain `still` already strikes for reduced motion.
    const bob = this.still || !a.animates
      ? 0
      : // Winning a rally is a hop, and losing one is a slump. Both are the
        // body's own position rather than something drawn on top of it, which
        // is what makes them read from across the room.
        a.emote === "cheer"
        ? Math.abs(Math.sin(a.clock * 7)) * 4.5
        : a.emote === "sob"
          ? -1.6
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

    // Whatever an off-duty agent picked up on the way, or whatever a director
    // put in their hands.
    if (a.holding === "mug") this.drawMug(ctx, a, bodyY - bob);
    else if (a.holding === "paddle") this.drawPaddle(ctx, a, bodyY - bob);
    else if (a.holding === "files") this.drawFolder(ctx, a, bodyY - bob);
    else if (a.holding === "phone") this.drawPhone(ctx, a, bodyY - bob);

    if (a.emote === "cheer") this.drawCheer(ctx, a, bodyY - bob);
    else if (a.emote === "sob") this.drawSob(ctx, a, bodyY - bob);

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

  /**
   * A folder of work in somebody's hand, and the pen that closes it off.
   *
   * Manila on purpose: it is the only warm paper colour in an office of
   * blue-grey, so a folder crossing the floor is visible as a thing being
   * carried rather than as part of whoever is carrying it. The tab and the two
   * ruled lines are what stop twelve units of cream reading as a biscuit.
   *
   * The pen only comes out on the finished flourish, which is the beat between
   * the work ending and the walk back to Anton's desk -- so the scribble is
   * drawn off the task state and needs nothing timed for it.
   */
  private drawFolder(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    const x = a.x + a.facing * AGENT_RADIUS * 1.05;
    const y = top + AGENT_RADIUS * 0.55;

    ctx.fillStyle = "#d8bd82";
    ctx.beginPath();
    ctx.roundRect(x - 6.5, y - 5, 13, 10.5, 1.5);
    ctx.fill();
    ctx.fillStyle = "#c19f5c";
    ctx.fillRect(x - 6.5, y - 5, 7, 2.2);
    ctx.strokeStyle = "#8d7440";
    ctx.lineWidth = 0.8;
    ctx.beginPath();
    ctx.moveTo(x - 4, y + 0.5);
    ctx.lineTo(x + 4.5, y + 0.5);
    ctx.moveTo(x - 4, y + 3);
    ctx.lineTo(x + 2, y + 3);
    ctx.stroke();

    if (a.state !== "finished") return;

    // The nib tracks back and forth across the folder. A stroke that only
    // wobbled in place read as a twitch; travelling is what reads as writing.
    const sweep = this.still ? 0.5 : Math.sin(a.clock * 9) * 0.5 + 0.5;
    const nibX = x - 4 + sweep * 8;
    ctx.strokeStyle = "#2f3540";
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    ctx.moveTo(nibX + 2.5, y - 6);
    ctx.lineTo(nibX, y - 0.5);
    ctx.stroke();
    ctx.fillStyle = "#4a6f9a";
    ctx.fillRect(x - 4, y - 1.2, Math.max(1, nibX - (x - 4)), 1.2);
  }

  /**
   * A handset up to the ear, for an agent Anton rang instead of visiting.
   *
   * The two arcs are the whole of it: a dark rectangle beside a head is a
   * phone only once something is coming out of it, and they blink on the
   * agent's own clock so two calls at once are not in unison.
   */
  private drawPhone(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    const x = a.x + a.facing * AGENT_RADIUS * 0.78;
    const y = top - AGENT_RADIUS * 0.3;

    ctx.fillStyle = "#161a21";
    ctx.beginPath();
    ctx.roundRect(x - 2.2, y - 5, 4.6, 10, 1.5);
    ctx.fill();
    ctx.fillStyle = "#4fd1c5";
    ctx.fillRect(x - 1.4, y - 3.6, 3, 1.2);

    if (this.still) return;
    const from = a.facing > 0 ? -0.7 : Math.PI - 0.7;
    const to = a.facing > 0 ? 0.7 : Math.PI + 0.7;
    ctx.strokeStyle = "rgba(226,232,240,0.55)";
    ctx.lineWidth = 1;
    for (let i = 0; i < 2; i++) {
      if (Math.sin(a.clock * 7 - i * 0.9) <= 0) continue;
      ctx.beginPath();
      ctx.arc(x + a.facing * 3, y - 1, 3.5 + i * 3, from, to);
      ctx.stroke();
    }
  }

  /**
   * Winning a rally: both arms up, and a tick off each hand.
   *
   * The hop is in the body (see drawAgent's bob), so this is only the arms --
   * which is the part that says the hop is celebration rather than a walk
   * cycle nobody asked for.
   */
  private drawCheer(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    const shoulder = top + AGENT_RADIUS * 0.3;
    const lift = this.still ? 0 : Math.abs(Math.sin(a.clock * 7)) * 2.5;
    const handY = shoulder - 12 - lift;

    ctx.strokeStyle = a.colourSoft;
    ctx.lineWidth = 2;
    ctx.beginPath();
    for (let i = 0; i < SIDES.length; i++) {
      const side = SIDES[i];
      ctx.moveTo(a.x + side * AGENT_RADIUS * 0.6, shoulder);
      ctx.lineTo(a.x + side * AGENT_RADIUS * 1.15, handY);
    }
    ctx.stroke();

    ctx.strokeStyle = "#f2b544";
    ctx.lineWidth = 1.2;
    ctx.beginPath();
    for (let i = 0; i < SIDES.length; i++) {
      const side = SIDES[i];
      const hx = a.x + side * AGENT_RADIUS * 1.15;
      ctx.moveTo(hx + side * 2, handY - 3);
      ctx.lineTo(hx + side * 5, handY - 6.5);
    }
    ctx.stroke();
  }

  /**
   * Losing one: arms down, and two tears on a loop.
   *
   * There are no faces in this office, so a mouth is not available and the
   * tears are doing all of the work. They fall a beat apart, because two dots
   * moving in lockstep read as a machine rather than as somebody crying.
   */
  private drawSob(ctx: CanvasRenderingContext2D, a: OfficeAgent, top: number): void {
    ctx.strokeStyle = a.colourDim;
    ctx.lineWidth = 2;
    ctx.beginPath();
    for (let i = 0; i < SIDES.length; i++) {
      const side = SIDES[i];
      ctx.moveTo(a.x + side * AGENT_RADIUS * 0.68, top + 2);
      ctx.lineTo(a.x + side * AGENT_RADIUS * 0.86, top + AGENT_RADIUS * 0.95);
    }
    ctx.stroke();

    if (this.still) return;
    const headY = top - AGENT_RADIUS * 0.42;
    ctx.fillStyle = "#6ea8fe";
    for (let i = 0; i < SIDES.length; i++) {
      const fall = (a.clock * 1.6 + i * 0.5) % 1;
      ctx.beginPath();
      ctx.ellipse(
        a.x + SIDES[i] * AGENT_RADIUS * 0.48,
        headY + 2 + fall * 14,
        1.3,
        2.1,
        0,
        0,
        Math.PI * 2,
      );
      ctx.fill();
    }
  }

  /**
   * The coordinator's chair.
   *
   * The only chair in this office drawn apart from the furniture it belongs
   * to, because it is the only one that goes anywhere: it is parked at his
   * working seat, and it travels to the end of his desk when he settles in to
   * wait for work to come back. A back and two arms is what keeps it an office
   * chair rather than one of the lunch benches.
   */
  private drawChair(ctx: CanvasRenderingContext2D, x: number, y: number): void {
    const r = chairRect(x, y, this.scratchProp);

    ctx.fillStyle = "#20242c";
    ctx.beginPath();
    ctx.roundRect(r.x + 3, r.y - 6, r.w - 6, 8, 3);
    ctx.fill();
    ctx.fillStyle = "#2a2f38";
    ctx.beginPath();
    ctx.roundRect(r.x, r.y, r.w, r.h, 5);
    ctx.fill();
    ctx.fillStyle = "#3a414d";
    ctx.fillRect(r.x + 1, r.y + 4, 2.5, 10);
    ctx.fillRect(r.x + r.w - 3.5, r.y + 4, 2.5, 10);
  }

  /**
   * Finished folders stacked on the coordinator's in-tray.
   *
   * The blue line along each one is the scribble the agent put on it before
   * walking it back, so a full tray says how much has come in as well as that
   * something has. It empties on the frame his own next turn starts: he is
   * reading them.
   */
  private drawReturned(ctx: CanvasRenderingContext2D, boss: OfficeAgent, count: number): void {
    const x = boss.deskX + TRAY_DX;
    for (let i = 0; i < count; i++) {
      const y = boss.deskY + TRAY_DY - i * TRAY_STEP;
      ctx.fillStyle = i === count - 1 ? "#d8bd82" : "#c19f5c";
      ctx.beginPath();
      ctx.roundRect(x, y, TRAY_FOLDER_W, TRAY_FOLDER_H, 1.5);
      ctx.fill();
      ctx.fillStyle = "#4a6f9a";
      ctx.fillRect(x + 4, y + 2.4, TRAY_FOLDER_W - 13, 1.2);
    }
  }

  /**
   * The patch the in-tray's stack occupies, tall enough for a full one.
   *
   * Asked for only on the frames a folder lands or the tray empties, which is
   * a handful of times a run.
   */
  private trayRect(out: Rect): Rect {
    const x = this.boss ? this.boss.deskX : 0;
    const y = this.boss ? this.boss.deskY : 0;
    out.x = x + TRAY_DX - 4;
    out.y = y + TRAY_DY - TRAY_STEP * TRAY_MAX - 4;
    out.w = TRAY_FOLDER_W + 8;
    out.h = TRAY_FOLDER_H + TRAY_STEP * TRAY_MAX + 8;
    return out;
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
/**
 * A title cut to fit a note, with an ellipsis where it was cut.
 *
 * Counted rather than measured: the notes are set in the same monospace face
 * as the monitors, so the advance the renderer measured once at startup says
 * exactly how many characters fit, and drawing the board costs no text
 * measurement at all.
 */
function clip(text: string, chars: number): string {
  const trimmed = text.trim();
  if (trimmed.length <= chars) return trimmed;
  return `${trimmed.slice(0, Math.max(1, chars - 1)).trimEnd()}\u2026`;
}

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
