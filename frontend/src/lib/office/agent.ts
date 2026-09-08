import { AGENT_RADIUS, BOUNDS, WALK_SPEED } from "./world";

/**
 * What an agent is visibly doing. This is animation state, owned entirely by
 * the frontend. The backend never sees it and never sends positions.
 */
export type VisualState = "idle" | "walking" | "working" | "finished" | "error";

/** Where the agent is trying to get to, which outlives any single walk. */
type Intent = "wander" | "desk";

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

    // Start somewhere loose in the room rather than on the desk, so the first
    // frame already looks like an office and not a lineup.
    this.x = clamp(opts.deskX + rand(-110, 110), BOUNDS.minX, BOUNDS.maxX);
    this.y = clamp(opts.deskY + rand(90, 170), BOUNDS.minY, BOUNDS.maxY);
    this.targetX = this.x;
    this.targetY = this.y;
    this.timer = rand(0.2, 2);
  }

  /** Send the agent to their desk and put them to work when they arrive. */
  assign(): void {
    this.intent = "desk";
    this.state = "walking";
    this.targetX = this.seatX;
    this.targetY = this.seatY;
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
          this.pickWanderTarget();
          this.state = "walking";
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

  /** True when the agent is close enough to the target to count as arrived. */
  private nearTarget(epsilon: number): boolean {
    const dx = this.targetX - this.x;
    const dy = this.targetY - this.y;
    return dx * dx + dy * dy <= epsilon * epsilon;
  }

  /** Moves along a straight line. Returns true on arrival. */
  private moveTowardsTarget(dt: number): boolean {
    const dx = this.targetX - this.x;
    const dy = this.targetY - this.y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist < 1.5) {
      this.x = this.targetX;
      this.y = this.targetY;
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

  private pickWanderTarget(): void {
    // Wander near the desk rather than across the whole floor: it reads as
    // "hanging around my area" instead of a random walk.
    const spanX = 190;
    const spanY = 120;
    this.targetX = clamp(
      this.deskX + rand(-spanX, spanX),
      BOUNDS.minX + AGENT_RADIUS,
      BOUNDS.maxX - AGENT_RADIUS,
    );
    this.targetY = clamp(
      this.deskY + rand(60, spanY + 90),
      BOUNDS.minY + AGENT_RADIUS,
      BOUNDS.maxY - AGENT_RADIUS,
    );
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
