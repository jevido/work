/**
 * The office lives in a fixed logical world. Everything -- desks, agents,
 * walkable bounds -- is expressed in these units, and the renderer scales the
 * whole world to fit the canvas. Resizing the window therefore never changes
 * the layout, and the Go side can hand us desk coordinates that stay valid.
 *
 * The layout is a classroom: the coordinator's desk sits at the front and the
 * specialists' desks face it from below.
 */
export const WORLD_WIDTH = 1000;
export const WORLD_HEIGHT = 700;

/** Walkable area, inset from the world edge so nobody clips the wall. */
export const BOUNDS = {
  minX: 60,
  maxX: WORLD_WIDTH - 60,
  minY: 90,
  maxY: WORLD_HEIGHT - 50,
} as const;

/** Desk surface size in world units. */
export const DESK_WIDTH = 150;
export const DESK_HEIGHT = 58;

/** Agent body size in world units. */
export const AGENT_RADIUS = 15;

/** Walking speed, world units per second. */
export const WALK_SPEED = 78;

/**
 * Render cap. Weak laptops are the target and the office is calm, so there is
 * nothing to gain from matching the panel's refresh rate. Animation is
 * delta-time based, so the cap changes cost, not motion.
 *
 * The cap can only skip whole animation frames, so the effective rate is the
 * refresh rate divided by a whole number: 30 lands exactly on half of a 60Hz
 * panel, where a value like 48 would also give 30 while pretending otherwise.
 */
export const TARGET_FPS = 30;

/**
 * Frame rate while nobody is working and the agents are only strolling.
 *
 * Measurement showed most of the idle cost is the webview compositing the
 * canvas surface, not our drawing, so halving the number of composited frames
 * is worth more than any further drawing optimisation. Wandering at 15fps is
 * indistinguishable; the moment an agent starts working the office goes back to
 * the full rate.
 */
export const IDLE_FPS = 15;

/** World y where the back wall meets the floor. */
export const WALL_Y = 56;

/** An axis-aligned rectangle in world coordinates. */
export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * The area a desk occupies visually: the surface plus the monitor standing on
 * it. Used both for drawing bounds and, inflated, as an obstacle.
 */
export function deskRect(deskX: number, deskY: number): Rect {
  const top = deskY - DESK_HEIGHT / 2 - 28;
  return {
    x: deskX - DESK_WIDTH / 2,
    y: top,
    w: DESK_WIDTH,
    h: deskY + DESK_HEIGHT / 2 - top,
  };
}

/** Monitor size in world units. */
export const MONITOR_WIDTH = 60;
export const MONITOR_HEIGHT = 30;

/** How far the monitor's top edge rises above the top of the desk surface. */
export const MONITOR_RISE = 26;

/** Width of the monitor's bezel: the frame around the tinted glass. */
export const MONITOR_BEZEL = 4;

/**
 * The live readout drawn on a working agent's monitor, in world units.
 *
 * A monitor is 60x30, which leaves 52x22 of glass, so the type has to be tiny.
 * Six units gets roughly thirteen characters across and three rows down: too
 * small to read a sentence from across the room, but enough that the shape of
 * the words moves, and enough to actually read once the window is large. Two
 * bigger rows was the alternative and it looked like a label, not a terminal.
 *
 * These are world units on purpose. The wrap width therefore never changes
 * with the window, so resizing re-scales the readout instead of re-wrapping it.
 */
export const MONITOR_TEXT_SIZE = 6;
export const MONITOR_LINE_HEIGHT = 7;
export const MONITOR_TEXT_LINES = 3;

/** Breathing room between the bezel and the first character. */
export const MONITOR_TEXT_PAD = 2;

/**
 * The monitor standing on a desk. Fills `out` rather than returning a new
 * object, because this is called while drawing and the loop must not allocate.
 *
 * The renderer draws from this and the pointer hit-test measures against it, so
 * the two cannot drift apart.
 */
export function monitorRect(deskX: number, deskY: number, out: Rect): Rect {
  out.x = deskX - MONITOR_WIDTH / 2;
  out.y = deskY - DESK_HEIGHT / 2 - MONITOR_RISE;
  out.w = MONITOR_WIDTH;
  out.h = MONITOR_HEIGHT;
  return out;
}

/**
 * Desks as obstacles, inflated by the agent's body so nobody clips a corner.
 *
 * Only the desk surface blocks movement, not the monitor above it: the monitor
 * is drawn standing behind the desk, and treating it as solid would push agents
 * an awkward distance away from their own seat.
 */
export function deskObstacle(deskX: number, deskY: number): Rect {
  const pad = AGENT_RADIUS * 0.8;
  return {
    x: deskX - DESK_WIDTH / 2 - pad,
    y: deskY - DESK_HEIGHT / 2 - pad,
    w: DESK_WIDTH + pad * 2,
    h: DESK_HEIGHT + pad * 2,
  };
}

/** True when the point is inside the rectangle. */
export function inRect(r: Rect, x: number, y: number): boolean {
  return x >= r.x && x <= r.x + r.w && y >= r.y && y <= r.y + r.h;
}

/** True when the point is clear of every obstacle and inside the floor. */
export function isWalkable(obstacles: readonly Rect[], x: number, y: number): boolean {
  if (x < BOUNDS.minX || x > BOUNDS.maxX || y < BOUNDS.minY || y > BOUNDS.maxY) {
    return false;
  }
  for (let i = 0; i < obstacles.length; i++) {
    if (inRect(obstacles[i], x, y)) return false;
  }
  return true;
}

/**
 * True when a straight walk from one point to another touches no obstacle.
 *
 * Sampling along the segment rather than solving the intersection exactly: the
 * step is a fraction of the agent's body, the call happens only when a new
 * target is chosen, and the arithmetic stays obvious.
 */
export function pathIsClear(
  obstacles: readonly Rect[],
  x0: number,
  y0: number,
  x1: number,
  y1: number,
): boolean {
  const dx = x1 - x0;
  const dy = y1 - y0;
  const dist = Math.sqrt(dx * dx + dy * dy);
  const steps = Math.max(1, Math.ceil(dist / (AGENT_RADIUS * 0.6)));
  for (let i = 0; i <= steps; i++) {
    const t = i / steps;
    if (!isWalkable(obstacles, x0 + dx * t, y0 + dy * t)) return false;
  }
  return true;
}
