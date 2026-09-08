/**
 * The office lives in a fixed logical world. Everything -- desks, agents,
 * walkable bounds -- is expressed in these units, and the renderer scales the
 * whole world to fit the canvas. Resizing the window therefore never changes
 * the layout, and the Go side can hand us desk coordinates that stay valid.
 */
export const WORLD_WIDTH = 1000;
export const WORLD_HEIGHT = 700;

/** Walkable area, inset from the world edge so nobody clips the wall. */
export const BOUNDS = {
  minX: 60,
  maxX: WORLD_WIDTH - 60,
  minY: 90,
  maxY: WORLD_HEIGHT - 60,
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
