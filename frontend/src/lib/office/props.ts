import { AGENT_RADIUS, BOUNDS, isWalkable, rectsOverlap, type Point, type Rect } from "./world";

/**
 * The furniture that is not a desk.
 *
 * Almost all of it now has a fixed address, because the rooms do: the coffee
 * station is against the break room's back wall, the lunch table is in the
 * middle of that room, the meeting table is in the meeting room. Only the ping
 * pong table is still placed by search, because it lives in the bullpen where
 * the desks are laid out from the roster (internal/agents/layout.go) and a
 * hand-picked spot would end up inside somebody's monitor the day a fifth
 * agent's folder appears.
 *
 * Everything here is geometry. What it looks like is the renderer's business;
 * where an agent stands to use it is this file's.
 */

/** Coffee station: a counter with the machine standing at the back of it. */
export const COFFEE_WIDTH = 128;
export const COFFEE_HEIGHT = 42;
/** The machine housing, drawn standing on the counter's back edge. */
export const COFFEE_MACHINE_WIDTH = 62;
export const COFFEE_MACHINE_HEIGHT = 42;

/** Where somebody stands to use the machine, measured from the counter front. */
export const COFFEE_STAND_GAP = 34;

export const FRIDGE_WIDTH = 46;
export const FRIDGE_HEIGHT = 64;

/** The lunch table, and the bench spots along the front of it. */
export const LUNCH_WIDTH = 150;
export const LUNCH_HEIGHT = 66;
export const LUNCH_SEAT_GAP = 22;
export const LUNCH_SEAT_SPACING = 55;

/** The meeting table, and where two people stand to talk across it. */
export const MEETING_WIDTH = 170;
export const MEETING_HEIGHT = 96;
export const MEETING_SPOT_GAP = 30;

/** The ping pong table, and how far from its centre a player stands. */
export const PONG_WIDTH = 124;
export const PONG_HEIGHT = 54;
export const PONG_STAND_X = PONG_WIDTH / 2 + 26;

/** A potted plant. Purely there to stop the corners looking like a warehouse. */
export const PLANT_RADIUS = 15;

/** A piece of furniture, addressed by its centre. */
export type Prop = Point;

/**
 * The break room's coffee station.
 *
 * Far enough off the back wall for the machine to be drawn standing on the
 * counter: things on a surface rise above it in this projection, the same way
 * a monitor rises above a desk, so a counter shoved against the wall would
 * have its machine drawn inside the wall.
 */
export const COFFEE: Prop = { x: 1092, y: 141 };

/** The fridge, along the same wall. Floor-standing, so it needs no headroom. */
export const FRIDGE: Prop = { x: 1236, y: 118 };

/**
 * The lunch table, set to the right of the room so the lane in from the door
 * stays clear. The router can find its way round a table; it should not have
 * to in order to cross the room.
 */
export const LUNCH: Prop = { x: 1150, y: 254 };

/** The meeting table. */
export const MEETING: Prop = { x: 1140, y: 480 };

/** Pots, in corners nothing else wants. */
export const PLANTS: readonly Prop[] = [
  { x: 968, y: 104 },
  { x: 1252, y: 622 },
  { x: 78, y: 168 },
];

/**
 * The office printer, against the back wall past the coordinator's desk.
 *
 * Not in the corridor between the desks and the wing, tempting as that looked:
 * with a full roster that corridor is the only way through to the doorways,
 * and a printer standing in it left the far desks unable to plan a route to
 * the break room at all.
 */
export const PRINTER: Prop = { x: 884, y: 90 };
export const PRINTER_WIDTH = 46;
export const PRINTER_HEIGHT = 42;

/** A low bookshelf along the bullpen's back wall. */
export const SHELF: Prop = { x: 172, y: 78 };
export const SHELF_WIDTH = 124;
export const SHELF_HEIGHT = 28;

export interface Props {
  coffee: Prop;
  fridge: Prop;
  lunch: Prop;
  meeting: Prop;
  /** The ping pong table, or null when the bullpen had no room for one. */
  pong: Prop | null;
}

function centred(p: Prop, w: number, h: number, out: Rect): Rect {
  out.x = p.x - w / 2;
  out.y = p.y - h / 2;
  out.w = w;
  out.h = h;
  return out;
}

export function coffeeRect(p: Prop, out: Rect): Rect {
  return centred(p, COFFEE_WIDTH, COFFEE_HEIGHT, out);
}

export function fridgeRect(p: Prop, out: Rect): Rect {
  return centred(p, FRIDGE_WIDTH, FRIDGE_HEIGHT, out);
}

export function lunchRect(p: Prop, out: Rect): Rect {
  return centred(p, LUNCH_WIDTH, LUNCH_HEIGHT, out);
}

export function meetingRect(p: Prop, out: Rect): Rect {
  return centred(p, MEETING_WIDTH, MEETING_HEIGHT, out);
}

export function pongRect(p: Prop, out: Rect): Rect {
  return centred(p, PONG_WIDTH, PONG_HEIGHT, out);
}

export function printerRect(p: Prop, out: Rect): Rect {
  return centred(p, PRINTER_WIDTH, PRINTER_HEIGHT, out);
}

export function shelfRect(p: Prop, out: Rect): Rect {
  return centred(p, SHELF_WIDTH, SHELF_HEIGHT, out);
}

/** Furniture as an obstacle, inflated by the body radius as desks are. */
function obstacleOf(r: Rect): Rect {
  const pad = AGENT_RADIUS * 0.8;
  return { x: r.x - pad, y: r.y - pad, w: r.w + pad * 2, h: r.h + pad * 2 };
}

const scratch: Rect = { x: 0, y: 0, w: 0, h: 0 };

/**
 * Everything solid that is not a desk.
 *
 * One list, built when the scene is, and handed to every agent as well as to
 * the router: what an agent walks round and what the planner routes round have
 * to be the same set or agents walk through furniture that is drawn.
 */
export function furnitureObstacles(props: Props): Rect[] {
  const list = [
    obstacleOf(coffeeRect(props.coffee, scratch)),
    obstacleOf(fridgeRect(props.fridge, scratch)),
    obstacleOf(lunchRect(props.lunch, scratch)),
    obstacleOf(meetingRect(props.meeting, scratch)),
    obstacleOf(printerRect(PRINTER, scratch)),
    obstacleOf(shelfRect(SHELF, scratch)),
  ];
  for (const plant of PLANTS) {
    list.push(
      obstacleOf({
        x: plant.x - PLANT_RADIUS,
        y: plant.y - PLANT_RADIUS,
        w: PLANT_RADIUS * 2,
        h: PLANT_RADIUS * 2,
      }),
    );
  }
  if (props.pong) list.push(obstacleOf(pongRect(props.pong, scratch)));
  return list;
}

/** Where somebody stands to fetch a coffee: in front of the counter. */
export function coffeeStand(p: Prop): Point {
  return { x: p.x, y: p.y + COFFEE_HEIGHT / 2 + COFFEE_STAND_GAP };
}

/**
 * The bench spots along the front of the lunch table.
 *
 * All on the near side on purpose. Agents are drawn as flat figures over a
 * background the table is painted into, so somebody sat at the far side would
 * be drawn on top of the table they are supposedly behind -- the same reason a
 * working agent sits in front of their desk rather than at it.
 */
export function lunchSeats(p: Prop): readonly Point[] {
  const y = p.y + LUNCH_HEIGHT / 2 + LUNCH_SEAT_GAP;
  return [
    { x: p.x - LUNCH_SEAT_SPACING, y },
    { x: p.x, y },
    { x: p.x + LUNCH_SEAT_SPACING, y },
  ];
}

/** Where two agents stand to talk across the meeting table. */
export function meetingSpots(p: Prop): readonly Point[] {
  const y = p.y + MEETING_HEIGHT / 2 + MEETING_SPOT_GAP;
  return [
    { x: p.x - 44, y },
    { x: p.x + 44, y },
  ];
}

/** The two ends of the ping pong table: left player, right player. */
export function pongStands(p: Prop): [Point, Point] {
  return [
    { x: p.x - PONG_STAND_X, y: p.y },
    { x: p.x + PONG_STAND_X, y: p.y },
  ];
}

/**
 * Puts the whole floorplan together: the fixed furniture, plus a ping pong
 * table somewhere in the bullpen that the desks have left free.
 *
 * `desks` are the inflated desk obstacles, which is what the table must keep
 * clear of -- one touching the padding round a desk would be reachable but not
 * walkable round.
 */
export function placeProps(desks: readonly Rect[]): Props {
  const props: Props = {
    coffee: COFFEE,
    fridge: FRIDGE,
    lunch: LUNCH,
    meeting: MEETING,
    pong: null,
  };
  // The fixed furniture is in the wing and the desks never are, so the table
  // only has to dodge the desks and the plants.
  const blocked = [...desks, ...furnitureObstacles(props)];
  props.pong = findPongSpot(blocked);
  return props;
}

/**
 * The first spot in the bullpen where a ping pong table and both its players
 * fit. Null is a real answer: a crowded office does not get a table, and the
 * rally simply never starts.
 */
function findPongSpot(obstacles: readonly Rect[]): Prop | null {
  const low = BOUNDS.maxY - PONG_HEIGHT / 2 - 14;
  const candidates: Prop[] = [
    // The gap between the desk rows and the wing wall first: it is the one
    // part of the bullpen nothing else uses, and a games table parked in a
    // dead corner is exactly where a games table ends up.
    { x: 820, y: 470 },
    { x: 500, y: low },
    { x: 340, y: low },
    { x: 700, y: low },
    { x: 500, y: 215 },
  ];

  for (const candidate of candidates) {
    const footprint = obstacleOf(pongRect(candidate, scratch));
    if (obstacles.some((o) => rectsOverlap(o, footprint))) continue;
    if (pongStands(candidate).some((s) => !isWalkable(obstacles, s.x, s.y))) continue;
    return candidate;
  }
  return null;
}
