/**
 * The office lives in a fixed logical world. Everything -- rooms, desks,
 * agents -- is expressed in these units, and the renderer scales the whole
 * world to fit the canvas. Resizing the window therefore never changes the
 * layout, and the Go side can hand us desk coordinates that stay valid.
 *
 * The floor is a floorplan, not an open field: a bullpen where the desks are,
 * and a wing off it holding a break room and a meeting room. Rooms are
 * rectangles that share edges, walls are drawn on those shared edges, and the
 * only way between two rooms is a doorway. Agents cannot cross a wall because
 * the space a wall occupies is not floor -- see `onFloor` -- so the walls are
 * a drawing and a hole in the walkable area, never an obstacle to collide
 * with.
 *
 * Desk coordinates come from the backend (internal/agents/layout.go) and are
 * unchanged by any of this: the bullpen was sized around where they already
 * are, and the wing is space that did not exist before.
 */
export const WORLD_WIDTH = 1320;
export const WORLD_HEIGHT = 700;

/**
 * Thickness of a wall, drawn straddling the boundary it sits on.
 *
 * Thick enough to read. Twelve units looked right on paper and drew as a
 * hairline once the world was scaled into a panel, which made the rooms look
 * like different carpets rather than different rooms.
 */
export const WALL = 18;

/** The inside faces of the outer wall. */
export const FLOOR_LEFT = 40;
export const FLOOR_RIGHT = 1280;
export const FLOOR_TOP = 56;
export const FLOOR_BOTTOM = 660;

/** World y where the back wall meets the floor. */
export const WALL_Y = FLOOR_TOP;

/** The partition between the bullpen and the wing. */
export const WING_X = 1000;

/** The partition splitting the wing into break room and meeting room. */
export const WING_SPLIT_Y = 356;

/**
 * The whole interior, as one box.
 *
 * Only for coarse work -- clamping a candidate before testing it, bounding the
 * furniture search. Whether a point is actually floor is `onFloor`'s business,
 * because most of this box is wall or another room.
 */
export const BOUNDS = {
  minX: FLOOR_LEFT,
  maxX: FLOOR_RIGHT,
  minY: FLOOR_TOP,
  maxY: FLOOR_BOTTOM,
} as const;

/** Desk surface size in world units. */
export const DESK_WIDTH = 150;
export const DESK_HEIGHT = 58;

/** Agent body size in world units. */
export const AGENT_RADIUS = 15;

/**
 * Walking speed, world units per second.
 *
 * Raised with the floorplan: the wing put the coffee machine the best part of
 * a thousand units from the far desks, and at the old speed fetching one took
 * long enough to look like a punishment.
 */
export const WALK_SPEED = 88;

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

/**
 * Whether the person watching has asked for less movement.
 *
 * One place, read by everything that animates: the director decides whether to
 * start anything, the agents decide whether to stroll or walk anywhere, and the
 * renderer decides whether its oscillators tick. A MediaQueryList keeps itself
 * up to date, so this is live without a listener to leak.
 *
 * The rule this file settles on: motion is decoration, so it stops. State is
 * not, so it stays -- a working agent still sits at a lit desk with their
 * output filling the monitor, they simply got there without a walk.
 */
const reducedMotion =
  typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-reduced-motion: reduce)")
    : null;

export function prefersReducedMotion(): boolean {
  return reducedMotion?.matches ?? false;
}

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

/** True when two rectangles share any area. */
export function rectsOverlap(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

/** A point in the world. */
export interface Point {
  x: number;
  y: number;
}

export type RoomId = "bullpen" | "break" | "meeting";

/**
 * One room: the rectangle of its floor, what to call it, and the lanes through
 * it that are known to be clear.
 *
 * `junctions` are circulation points -- the middle of a doorway's approach, the
 * open lane down one side. The router falls back to them when a destination
 * cannot be reached by a straight line or one right-angle turn, which is the
 * normal case for anything on the far side of a table. Declaring the lanes is
 * how an office of rectangles gets navigable without a pathfinder.
 */
export interface Room {
  id: RoomId;
  label: string;
  /**
   * Which corner the label goes in. Every room in this office has something
   * against its back wall, and in the break room the near wall is where people
   * sit, so there is no one corner that is free in all three.
   */
  labelCorner: "top-left" | "top-right" | "bottom-left";
  x: number;
  y: number;
  w: number;
  h: number;
  junctions: readonly Point[];
}

export const ROOMS: readonly Room[] = [
  {
    id: "bullpen",
    label: "BULLPEN",
    labelCorner: "bottom-left",
    x: FLOOR_LEFT,
    y: FLOOR_TOP,
    w: WING_X - FLOOR_LEFT,
    h: FLOOR_BOTTOM - FLOOR_TOP,
    // The lane along the front of the desks, and the one down the wing side.
    junctions: [
      { x: 930, y: 200 },
      { x: 930, y: 520 },
      { x: 500, y: 640 },
      { x: 110, y: 400 },
    ],
  },
  {
    id: "break",
    label: "BREAK ROOM",
    labelCorner: "top-right",
    x: WING_X,
    y: FLOOR_TOP,
    w: FLOOR_RIGHT - WING_X,
    h: WING_SPLIT_Y - FLOOR_TOP,
    // Just inside the door, and the corner the lunch table does not reach.
    junctions: [
      { x: 1040, y: 200 },
      { x: 1040, y: 330 },
    ],
  },
  {
    id: "meeting",
    label: "MEETING",
    labelCorner: "bottom-left",
    x: WING_X,
    y: WING_SPLIT_Y,
    w: FLOOR_RIGHT - WING_X,
    h: FLOOR_BOTTOM - WING_SPLIT_Y,
    junctions: [
      { x: 1040, y: 520 },
      { x: 1140, y: 640 },
    ],
  },
];

/**
 * A gap in a wall, and the two rooms it joins.
 *
 * `at` is the wall's line and `centre` the middle of the gap along it, so a
 * doorway is one point to walk to from either side. `span` is the full width of
 * the opening, of which the middle `span - 2 * MARGIN` is actually walkable.
 */
export interface Doorway {
  a: RoomId;
  b: RoomId;
  axis: "v" | "h";
  at: number;
  centre: number;
  span: number;
}

export const DOORWAYS: readonly Doorway[] = [
  { a: "bullpen", b: "break", axis: "v", at: WING_X, centre: 200, span: 76 },
  { a: "bullpen", b: "meeting", axis: "v", at: WING_X, centre: 520, span: 76 },
];

/** How far an agent's centre stays off a wall. */
const MARGIN = AGENT_RADIUS + 4;

/** The point in the middle of a doorway, which is what a route aims at. */
export function doorPoint(door: Doorway, out: Point): Point {
  if (door.axis === "v") {
    out.x = door.at;
    out.y = door.centre;
  } else {
    out.x = door.centre;
    out.y = door.at;
  }
  return out;
}

/**
 * The walkable slot inside a doorway.
 *
 * Rooms stop a margin short of their own walls, which would leave the wall band
 * -- and therefore every doorway -- unreachable. This is the patch of floor
 * that bridges the two, narrowed along the wall so a body cannot clip a jamb.
 */
function passageOf(door: Doorway): Rect {
  const deep = WALL / 2 + MARGIN;
  const half = door.span / 2 - MARGIN;
  if (door.axis === "v") {
    return { x: door.at - deep, y: door.centre - half, w: deep * 2, h: half * 2 };
  }
  return { x: door.centre - half, y: door.at - deep, w: half * 2, h: deep * 2 };
}

const PASSAGES: readonly Rect[] = DOORWAYS.map(passageOf);

/** The room a point is in, ignoring walls and furniture, or null for none. */
export function roomAt(x: number, y: number): Room | null {
  for (let i = 0; i < ROOMS.length; i++) {
    const r = ROOMS[i];
    if (x >= r.x && x <= r.x + r.w && y >= r.y && y <= r.y + r.h) return ROOMS[i];
  }
  return null;
}

/**
 * True when a body can stand centred on this point: inside a room and a margin
 * clear of its walls, or inside a doorway.
 *
 * This is the only thing keeping agents out of walls, and it is why walls are
 * not obstacles. A wall is the space between two rooms' margins, so a route
 * that crosses one fails `pathIsClear` on the samples that land in the gap --
 * which is 2 * MARGIN + WALL wide, several times the sampling step.
 */
export function onFloor(x: number, y: number): boolean {
  for (let i = 0; i < ROOMS.length; i++) {
    const r = ROOMS[i];
    if (
      x >= r.x + MARGIN &&
      x <= r.x + r.w - MARGIN &&
      y >= r.y + MARGIN &&
      y <= r.y + r.h - MARGIN
    ) {
      return true;
    }
  }
  for (let i = 0; i < PASSAGES.length; i++) {
    if (inRect(PASSAGES[i], x, y)) return true;
  }
  return false;
}

/** A stretch of wall to draw. Walls are drawing and nothing else. */
export interface WallSegment {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * Every wall in the office: the outer shell, and the partitions with their
 * doorways cut out.
 *
 * Derived from the rooms and the doorways rather than listed, so a wall cannot
 * end up somewhere a room does not agree with.
 */
export const WALLS: readonly WallSegment[] = buildWalls();

function buildWalls(): WallSegment[] {
  const half = WALL / 2;
  const walls: WallSegment[] = [
    // The outer shell, drawn outside the floor so the rooms keep their size.
    { x: FLOOR_LEFT - WALL, y: FLOOR_TOP - WALL, w: FLOOR_RIGHT - FLOOR_LEFT + WALL * 2, h: WALL },
    { x: FLOOR_LEFT - WALL, y: FLOOR_BOTTOM, w: FLOOR_RIGHT - FLOOR_LEFT + WALL * 2, h: WALL },
    { x: FLOOR_LEFT - WALL, y: FLOOR_TOP - WALL, w: WALL, h: FLOOR_BOTTOM - FLOOR_TOP + WALL * 2 },
    { x: FLOOR_RIGHT, y: FLOOR_TOP - WALL, w: WALL, h: FLOOR_BOTTOM - FLOOR_TOP + WALL * 2 },
  ];

  // Interior partitions, each broken by whatever doorways sit on its line.
  const partitions = [
    { axis: "v" as const, at: WING_X, from: FLOOR_TOP, to: FLOOR_BOTTOM },
    { axis: "h" as const, at: WING_SPLIT_Y, from: WING_X, to: FLOOR_RIGHT },
  ];

  for (const part of partitions) {
    const gaps = DOORWAYS.filter((d) => d.axis === part.axis && d.at === part.at)
      .map((d) => ({ from: d.centre - d.span / 2, to: d.centre + d.span / 2 }))
      .sort((a, b) => a.from - b.from);

    let cursor = part.from;
    for (const gap of [...gaps, { from: part.to, to: part.to }]) {
      const length = gap.from - cursor;
      if (length > 0) {
        walls.push(
          part.axis === "v"
            ? { x: part.at - half, y: cursor, w: WALL, h: length }
            : { x: cursor, y: part.at - half, w: length, h: WALL },
        );
      }
      cursor = gap.to;
    }
  }

  return walls;
}

/**
 * Which doorways to pass through to get from one room to another, worked out
 * once at startup.
 *
 * Three rooms make this a table rather than a search: a breadth-first walk of
 * the doorway graph runs when the module loads and the answer is a lookup for
 * the rest of the session.
 */
const ROUTES = buildRoutes();

function buildRoutes(): Map<string, readonly Doorway[]> {
  const routes = new Map<string, readonly Doorway[]>();
  for (const start of ROOMS) {
    const seen = new Map<RoomId, readonly Doorway[]>([[start.id, []]]);
    const queue: RoomId[] = [start.id];
    while (queue.length > 0) {
      const here = queue.shift()!;
      const path = seen.get(here)!;
      for (const door of DOORWAYS) {
        const next = door.a === here ? door.b : door.b === here ? door.a : null;
        if (!next || seen.has(next)) continue;
        seen.set(next, [...path, door]);
        queue.push(next);
      }
    }
    for (const [room, path] of seen) routes.set(`${start.id}>${room}`, path);
  }
  return routes;
}

/** The doorways between two rooms, or null when they are not connected. */
export function routeBetween(from: RoomId, to: RoomId): readonly Doorway[] | null {
  return ROUTES.get(`${from}>${to}`) ?? null;
}

/** True when the point is clear of every obstacle and on the floor. */
export function isWalkable(obstacles: readonly Rect[], x: number, y: number): boolean {
  if (!onFloor(x, y)) return false;
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

/**
 * The most points a planned walk can have: two doorways, and a turn and a lane
 * junction either side of each.
 */
export const MAX_LEGS = 8;

/** Scratch for the planner, which runs in one place and never re-enters. */
const scratchPoint: Point = { x: 0, y: 0 };

/**
 * Plans a walk as a polyline of clear straight legs, written into `out`.
 *
 * Returns how many points were written, or 0 when there is no route -- which
 * is the signal to want something else instead of walking through the
 * furniture. `out` must hold at least MAX_LEGS points, all preallocated: this
 * runs when a destination is chosen, not while walking, but the office is not
 * in the business of producing garbage.
 *
 * Rooms are crossed in doorway order; within a room a leg is a straight line
 * if it can be, a right-angle turn if it cannot, and a detour via one of the
 * room's declared lanes if it still cannot. That last case is what gets an
 * agent round a lunch table instead of giving up on lunch.
 */
export function planRoute(
  obstacles: readonly Rect[],
  fromX: number,
  fromY: number,
  toX: number,
  toY: number,
  out: Point[],
): number {
  const fromRoom = roomAt(fromX, fromY);
  const toRoom = roomAt(toX, toY);
  if (!fromRoom || !toRoom) return 0;
  const doors = routeBetween(fromRoom.id, toRoom.id);
  if (!doors) return 0;

  let count = 0;
  let x = fromX;
  let y = fromY;
  for (const door of doors) {
    doorPoint(door, scratchPoint);
    const next = planLeg(obstacles, x, y, scratchPoint.x, scratchPoint.y, out, count);
    if (next < 0) return 0;
    count = next;
    x = out[count - 1].x;
    y = out[count - 1].y;
  }

  const done = planLeg(obstacles, x, y, toX, toY, out, count);
  return done < 0 ? 0 : done;
}

/**
 * The doorways to a destination and then the destination, furniture ignored.
 *
 * The fallback for a walk that has to happen whatever is in the way: an agent
 * given a task goes to their desk even if the only line to it clips the corner
 * of somebody else's. It still never crosses a wall, which is the part that
 * would look broken rather than clumsy.
 */
export function planDoors(
  fromX: number,
  fromY: number,
  toX: number,
  toY: number,
  out: Point[],
): number {
  const fromRoom = roomAt(fromX, fromY);
  const toRoom = roomAt(toX, toY);
  const doors = fromRoom && toRoom ? routeBetween(fromRoom.id, toRoom.id) : null;

  let count = 0;
  if (doors) {
    for (const door of doors) {
      doorPoint(door, scratchPoint);
      count = push(out, count, scratchPoint.x, scratchPoint.y);
    }
  }
  return push(out, count, toX, toY);
}

/** One room-to-room leg: straight, turned, or round a lane. -1 when none work. */
function planLeg(
  obstacles: readonly Rect[],
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  out: Point[],
  count: number,
): number {
  const simple = planSimple(obstacles, x0, y0, x1, y1, out, count);
  if (simple >= 0) return simple;

  // Nothing direct worked, so go the way the room is meant to be walked.
  const start = roomAt(x0, y0);
  const end = roomAt(x1, y1);
  for (let pass = 0; pass < 2; pass++) {
    const room = pass === 0 ? start : end;
    if (!room || (pass === 1 && room === start)) continue;
    for (const lane of room.junctions) {
      if (!isWalkable(obstacles, lane.x, lane.y)) continue;
      const first = planSimple(obstacles, x0, y0, lane.x, lane.y, out, count);
      if (first < 0) continue;
      const second = planSimple(obstacles, lane.x, lane.y, x1, y1, out, first);
      if (second >= 0) return second;
    }
  }
  return -1;
}

/** A straight line, or one right-angle turn. -1 when neither is clear. */
function planSimple(
  obstacles: readonly Rect[],
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  out: Point[],
  count: number,
): number {
  if (count >= MAX_LEGS) return -1;
  if (pathIsClear(obstacles, x0, y0, x1, y1)) return push(out, count, x1, y1);
  if (count + 1 >= MAX_LEGS) return -1;

  // Across then down, or down then across. Enough to get round a rectangle.
  if (
    isWalkable(obstacles, x1, y0) &&
    pathIsClear(obstacles, x0, y0, x1, y0) &&
    pathIsClear(obstacles, x1, y0, x1, y1)
  ) {
    return push(out, push(out, count, x1, y0), x1, y1);
  }
  if (
    isWalkable(obstacles, x0, y1) &&
    pathIsClear(obstacles, x0, y0, x0, y1) &&
    pathIsClear(obstacles, x0, y1, x1, y1)
  ) {
    return push(out, push(out, count, x0, y1), x1, y1);
  }
  return -1;
}

/** Writes one point into the preallocated list and returns the new length. */
function push(out: Point[], count: number, x: number, y: number): number {
  const slot = out[count];
  slot.x = x;
  slot.y = y;
  return count + 1;
}
