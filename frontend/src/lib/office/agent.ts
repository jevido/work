import { MonitorTail } from "./tail";
import {
  AGENT_RADIUS,
  BOUNDS,
  WALK_SPEED,
  isWalkable,
  pathIsClear,
  type Rect,
} from "./world";

/**
 * What an agent is visibly doing. This is animation state, owned entirely by
 * the frontend. The backend never sees it and never sends positions.
 */
export type VisualState = "idle" | "walking" | "working" | "finished" | "error";

/** Where the agent is trying to get to, which outlives any single walk. */
type Intent = "wander" | "desk";

/** How far outside its own bounds an agent's drawing can reach. */
const BOX_HALF_WIDTH = 46;
const BOX_ABOVE = 52;
const BOX_BELOW = 46;

/**
 * A worker in the office.
 *
 * Every field is a number or a string set once: instances are allocated when
 * the scene is built and then mutated in place, so the animation loop does not
 * allocate.
 */
export class OfficeAgent {
  readonly id: string;
  readonly name: string;

  /** Body colour, and the two shades derived from it, computed once. */
  readonly colour: string;
  readonly colourDim: string;
  readonly colourSoft: string;

  /** Desk surface centre. */
  readonly deskX: number;
  readonly deskY: number;
  /** Where the agent stands when working. */
  readonly seatX: number;
  readonly seatY: number;

  x: number;
  y: number;
  targetX: number;
  targetY: number;

  /**
   * One optional stop on the way to the target, used to walk around a desk
   * instead of through it. Simpler than a path: two straight legs are enough
   * in a room this shape, and it costs no allocation.
   */
  private wayX = 0;
  private wayY = 0;
  private hasWaypoint = false;

  state: VisualState = "idle";
  intent: Intent = "wander";

  /** Seconds remaining in the current pause, hold or celebration. */
  timer = 0;
  /** Monotonic seconds since spawn, for cheap per-agent phase offsets. */
  clock = Math.random() * 10;
  /** -1 facing left, 1 facing right. */
  facing: 1 | -1 = 1;
  /** Walk cycle phase, in radians. */
  step = 0;

  /** Where the agent was when it was last drawn, for dirty-rectangle repaints. */
  drawnX = 0;
  drawnY = 0;
  drawnState: VisualState = "idle";
  /** True until the agent has been drawn once. */
  neverDrawn = true;

  /**
   * The tail of what this agent is writing, wrapped for their monitor. Filled
   * from the console's stream when it changes and read while drawing; empty
   * whenever they are not mid-turn, which is what keeps a quiet desk dark.
   */
  readonly tail = new MonitorTail();

  /** Desks the agent must walk around, shared with the scene. */
  private obstacles: readonly Rect[] = [];

  /**
   * True when this agent's motion is worth the full frame rate: working,
   * finishing, failing, or crossing the room because it was given a task.
   *
   * An aimless stroll is not. It is the office's resting state, it runs for
   * hours, and at half rate nobody can tell.
   */
  get wantsSmoothFrames(): boolean {
    if (this.state === "idle") return false;
    if (this.state === "walking" && this.intent === "wander") return false;
    return true;
  }

  constructor(opts: {
    id: string;
    name: string;
    colour: string;
    deskX: number;
    deskY: number;
    seatX: number;
    seatY: number;
  }) {
    this.id = opts.id;
    this.name = opts.name;
    this.colour = opts.colour;
    this.colourDim = shade(opts.colour, -0.35);
    this.colourSoft = shade(opts.colour, 0.25);
    this.deskX = opts.deskX;
    this.deskY = opts.deskY;
    this.seatX = opts.seatX;
    this.seatY = opts.seatY;

    this.x = opts.seatX;
    this.y = opts.seatY + 60;
    this.targetX = this.x;
    this.targetY = this.y;
    this.drawnX = this.x;
    this.drawnY = this.y;
    this.timer = rand(0.2, 2);
  }

  /**
   * Tells the agent which desks are solid. Called when the scene is built, and
   * again if the cast changes.
   */
  setObstacles(obstacles: readonly Rect[]): void {
    this.obstacles = obstacles;
    // Being spawned inside a desk is possible if the layout changed, so nudge
    // clear of one rather than starting stuck.
    if (!isWalkable(obstacles, this.x, this.y)) {
      for (let drop = 20; drop <= 160; drop += 20) {
        if (isWalkable(obstacles, this.seatX, this.seatY + drop)) {
          this.x = this.seatX;
          this.y = this.seatY + drop;
          break;
        }
      }
      this.targetX = this.x;
      this.targetY = this.y;
      this.drawnX = this.x;
      this.drawnY = this.y;
    }
  }

  /** The rectangle this agent's drawing occupies, in world units. */
  boxAt(x: number, y: number, out: Rect): Rect {
    out.x = x - BOX_HALF_WIDTH;
    out.y = y - BOX_ABOVE;
    out.w = BOX_HALF_WIDTH * 2;
    out.h = BOX_ABOVE + BOX_BELOW;
    return out;
  }

  /** Send the agent to their desk and put them to work when they arrive. */
  assign(): void {
    this.intent = "desk";
    this.state = "walking";
    this.headTo(this.seatX, this.seatY);
  }

  /** Already at the desk and producing output. */
  work(): void {
    this.intent = "desk";
    if (this.state !== "working") {
      this.state = this.nearTarget(6) ? "working" : "walking";
    }
  }

  /** Task done: hold at the desk briefly, then drift back to wandering. */
  finish(): void {
    this.state = "finished";
    this.timer = 1.4;
  }

  /** Task failed: stay put, show it, then drift back to wandering. */
  fail(): void {
    this.state = "error";
    this.timer = 2.6;
  }

  /**
   * Puts the agent straight into a state that was already true before this
   * page existed. A reload mid-run must not replay the walk to the desk: the
   * work started minutes ago, so they are simply sitting there, monitor lit.
   *
   * Only the states worth restoring are honoured. "finished" and "error" are
   * flourishes measured in seconds, so a reload has already missed them.
   */
  resume(state: VisualState): void {
    if (state === "working") {
      this.intent = "desk";
      this.state = "working";
      this.hasWaypoint = false;
      this.x = this.targetX = this.drawnX = this.seatX;
      this.y = this.targetY = this.drawnY = this.seatY;
      return;
    }
    if (state === "walking") this.assign();
  }

  /** Back to aimless wandering. */
  idle(): void {
    this.intent = "wander";
    this.state = "idle";
    this.timer = rand(0.3, 1.2);
  }

  /**
   * Advance by dt seconds. Called once per rendered frame per agent; allocates
   * nothing.
   */
  update(dt: number): void {
    this.clock += dt;

    switch (this.state) {
      case "idle": {
        // Standing still between strolls.
        this.timer -= dt;
        if (this.timer <= 0) {
          if (this.pickWanderTarget()) {
            this.state = "walking";
          } else {
            // Nowhere sensible to go this time; try again shortly.
            this.timer = rand(0.4, 1.2);
          }
        }
        return;
      }

      case "walking": {
        const arrived = this.moveTowardsTarget(dt);
        if (!arrived) return;
        if (this.intent === "desk") {
          this.state = "working";
        } else {
          this.state = "idle";
          this.timer = rand(0.6, 2.4);
        }
        return;
      }

      case "working": {
        // Seated. The renderer adds the small idle motion.
        return;
      }

      case "finished":
      case "error": {
        this.timer -= dt;
        if (this.timer <= 0) this.idle();
        return;
      }
    }
  }

  /**
   * Aims at a destination, inserting one intermediate stop if the direct line
   * crosses a desk.
   *
   * The detour tries the two L-shaped routes -- across then down, or down then
   * across -- which is enough to get around a rectangle in an open room.
   */
  private headTo(x: number, y: number): void {
    this.targetX = x;
    this.targetY = y;
    this.hasWaypoint = false;

    if (pathIsClear(this.obstacles, this.x, this.y, x, y)) return;

    const corners = [
      [x, this.y],
      [this.x, y],
    ] as const;
    for (const [cx, cy] of corners) {
      if (
        isWalkable(this.obstacles, cx, cy) &&
        pathIsClear(this.obstacles, this.x, this.y, cx, cy) &&
        pathIsClear(this.obstacles, cx, cy, x, y)
      ) {
        this.wayX = cx;
        this.wayY = cy;
        this.hasWaypoint = true;
        return;
      }
    }
    // No clear route found. Walking the direct line would cut a corner off a
    // desk, so give up on this destination and let the caller's state machine
    // pick another.
  }

  /** True when the agent is close enough to the target to count as arrived. */
  private nearTarget(epsilon: number): boolean {
    const dx = this.targetX - this.x;
    const dy = this.targetY - this.y;
    return dx * dx + dy * dy <= epsilon * epsilon;
  }

  /** Moves along the current leg. Returns true once the target is reached. */
  private moveTowardsTarget(dt: number): boolean {
    const goalX = this.hasWaypoint ? this.wayX : this.targetX;
    const goalY = this.hasWaypoint ? this.wayY : this.targetY;

    const dx = goalX - this.x;
    const dy = goalY - this.y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist < 1.5) {
      this.x = goalX;
      this.y = goalY;
      if (this.hasWaypoint) {
        // First leg done; carry on to the real target.
        this.hasWaypoint = false;
        return false;
      }
      this.step = 0;
      return true;
    }

    const travel = Math.min(dist, WALK_SPEED * dt);
    this.x += (dx / dist) * travel;
    this.y += (dy / dist) * travel;

    if (Math.abs(dx) > 2) this.facing = dx > 0 ? 1 : -1;
    // Roughly one full step cycle per 26 units walked.
    this.step += travel * 0.24;
    return false;
  }

  /**
   * Picks somewhere to stroll near the agent's own desk, on the floor and clear
   * of the furniture. Returns false when no candidate worked, which is the
   * signal to stand still a moment longer rather than walk through a desk.
   */
  private pickWanderTarget(): boolean {
    // Wander near the desk rather than across the whole floor: it reads as
    // "hanging around my area" instead of a random walk.
    const spanX = 190;
    for (let attempt = 0; attempt < 12; attempt++) {
      const x = clamp(
        this.deskX + rand(-spanX, spanX),
        BOUNDS.minX + AGENT_RADIUS,
        BOUNDS.maxX - AGENT_RADIUS,
      );
      const y = clamp(
        this.seatY + rand(10, 150),
        BOUNDS.minY + AGENT_RADIUS,
        BOUNDS.maxY - AGENT_RADIUS,
      );
      if (!isWalkable(this.obstacles, x, y)) continue;
      if (!pathIsClear(this.obstacles, this.x, this.y, x, y)) continue;
      this.targetX = x;
      this.targetY = y;
      this.hasWaypoint = false;
      return true;
    }
    return false;
  }
}

function rand(min: number, max: number): number {
  return min + Math.random() * (max - min);
}

function clamp(v: number, min: number, max: number): number {
  return v < min ? min : v > max ? max : v;
}

/**
 * Lightens (amount > 0) or darkens (amount < 0) a #rrggbb colour. Called three
 * times per agent at startup, never in the loop.
 */
function shade(hex: string, amount: number): string {
  const n = Number.parseInt(hex.slice(1), 16);
  const r = (n >> 16) & 0xff;
  const g = (n >> 8) & 0xff;
  const b = n & 0xff;
  const mix = (c: number) =>
    Math.round(amount >= 0 ? c + (255 - c) * amount : c * (1 + amount));
  return `rgb(${mix(r)},${mix(g)},${mix(b)})`;
}
