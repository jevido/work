import type { Held, OfficeAgent } from "./agent";
import { roomAt, type RoomId } from "./world";

/**
 * What a room asks of anybody who wants to be inside it.
 *
 * The office is a floorplan (world.ts) and the director (idle.ts) is what puts
 * agents in it. Neither of them should carry house rules: "the meeting room is
 * for two or more, coffee in hand" is neither geometry nor choreography, and
 * written into either it would be a special case nobody could reuse. It lives
 * here instead, as data per room plus one predicate -- `admits` -- that every
 * path into a room goes through, whether an agent is being escorted there by a
 * bit or has simply wandered that way.
 *
 * Adding a rule to another room is a line in ENTRY_RULES. Nothing else in the
 * office has to know it happened.
 *
 * The one thing a rule may not currently ask for is a party larger than two:
 * an idle bit holds a pair, so that is the largest group the director can
 * assemble. `minParty` above 2 would be honoured by the predicate and never
 * satisfied by the director.
 */
export interface EntryRule {
  /** Fewest agents that may be in the room together. 1 is "come as you are". */
  minParty: number;
  /** What each of them must have in hand, or "none" to ask for nothing. */
  carrying: Held;
  /**
   * How recently `carrying` must have been picked up, in seconds.
   *
   * A rule that asks for a coffee means a fresh one: an agent who has been
   * carrying the same mug round the office since before lunch has an ornament,
   * not a coffee, and is sent back to the machine for one.
   */
  freshFor: number;
}

/** A room with no rule of its own: anybody may walk in, alone, empty-handed. */
const OPEN: EntryRule = { minParty: 1, carrying: "none", freshFor: 0 };

/**
 * The house rules, by room. Rooms absent from here are OPEN.
 *
 * Meeting room: never alone, and everybody brings a coffee. Two is the whole
 * point of the room, and the coffee is what makes the walk over read as people
 * going into a meeting rather than two agents standing in a side room.
 */
export const ENTRY_RULES: Readonly<Partial<Record<RoomId, EntryRule>>> = {
  meeting: { minParty: 2, carrying: "mug", freshFor: 45 },
};

/** The rule for a room, which is OPEN unless it has one of its own. */
export function entryRule(room: RoomId): EntryRule {
  return ENTRY_RULES[room] ?? OPEN;
}

/** The rule in force at a point. Anywhere that is not in a room is OPEN. */
export function ruleAt(x: number, y: number): EntryRule {
  const room = roomAt(x, y);
  return room ? entryRule(room.id) : OPEN;
}

/** True when an agent holds what a rule asks for, freshly enough. */
export function satisfies(agent: OfficeAgent, rule: EntryRule): boolean {
  if (rule.carrying === "none") return true;
  return agent.holding === rule.carrying && agent.carriedFor <= rule.freshFor;
}

/**
 * Whether every member of the party is carrying what the rule asks for.
 *
 * Kept separate from `admits` because the director needs the difference: a
 * party that is big enough but empty-handed has an errand to run, and one that
 * is too small has nothing to do but give up.
 */
export function allCarrying(party: readonly OfficeAgent[], rule: EntryRule): boolean {
  for (const agent of party) if (!satisfies(agent, rule)) return false;
  return true;
}

/**
 * True when this party, exactly as it stands, may walk to a point.
 *
 * This is the gate. Every way an agent gets somewhere -- escorted into a room
 * by the director, or picking their own spot to stroll to -- asks this first,
 * so a rule cannot be walked round by a path nobody thought to check.
 *
 * Only the destination is tested, not the route. The rooms with rules are
 * leaves off the bullpen (see DOORWAYS), so no route passes through one on its
 * way somewhere else; a rule on a room that is a corridor would need the legs
 * of the plan checked too.
 */
export function admits(party: readonly OfficeAgent[], x: number, y: number): boolean {
  const rule = ruleAt(x, y);
  if (party.length < rule.minParty) return false;
  return allCarrying(party, rule);
}
