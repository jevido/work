/**
 * Which two sort keys a node is going between.
 *
 * Every structural edit in a workspace is the same shape: pick the neighbour
 * on each side, ask ./position.ts for a key in the gap. The picking is where
 * the mistakes live -- "the one above, unless it is the one being moved, in
 * which case the one above that" -- so it is here, as small pure functions
 * over a list of positions, rather than inline in five methods where the only
 * way to check it is to move a line and look.
 *
 * An empty string is the open end in both directions, which is how
 * ./position.ts spells "before everything" and "after everything".
 */
import { between } from "./position";

/** The neighbours a new or moved node sits between. */
export type Bounds = readonly [before: string, after: string];

/** The least this needs to know about a sibling. */
export interface Placed {
  position: string;
}

/** The key to give something placed between these. */
export function keyFor(bounds: Bounds): string {
  return between(bounds[0], bounds[1]);
}

/** Before everything in the list. */
export function atStart(siblings: readonly Placed[]): Bounds {
  return ["", siblings[0]?.position ?? ""];
}

/** After everything in the list. */
export function atEnd(siblings: readonly Placed[]): Bounds {
  return [siblings.at(-1)?.position ?? "", ""];
}

/**
 * Straight after the sibling at `index`.
 *
 * An index outside the list means the end, which is where something whose
 * intended neighbour has gone belongs -- it reads as "this arrived late",
 * which is what happened.
 */
export function afterIndex(siblings: readonly Placed[], index: number): Bounds {
  if (index < 0 || index >= siblings.length) return atEnd(siblings);
  return [siblings[index].position, siblings[index + 1]?.position ?? ""];
}

/**
 * One place up or down, for the node already at `index`.
 *
 * Null when there is nowhere to go. The subtlety this exists for: the gap to
 * land in is on the *far* side of the neighbour being stepped over, so moving
 * up looks two places up and moving down looks two places down. Reusing
 * afterIndex here would put the node back exactly where it started, because
 * the node itself is still in the list.
 */
export function stepped(
  siblings: readonly Placed[],
  index: number,
  delta: -1 | 1,
): Bounds | null {
  if (index < 0 || index >= siblings.length) return null;
  const target = index + delta;
  if (target < 0 || target >= siblings.length) return null;

  return delta === -1
    ? [siblings[index - 2]?.position ?? "", siblings[index - 1].position]
    : [siblings[index + 1].position, siblings[index + 2]?.position ?? ""];
}
