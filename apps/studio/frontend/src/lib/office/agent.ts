import { admits } from "./entry";
import { DISPATCH_HOLD_SECONDS, SpeechBubble } from "./speech";
import { MonitorTail } from "./tail";
import {
  AGENT_RADIUS,
  BOUNDS,
  MAX_LEGS,
  WALK_SPEED,
  isWalkable,
  planDoors,
  planRoute,
  prefersReducedMotion,
  type Point,
  type Rect,
} from "./world";

/**
 * What an agent is visibly doing. This is animation state, owned entirely by
 * the frontend. The backend never sees it and never sends positions.
 */
export type VisualState = "idle" | "walking" | "working" | "finished" | "error";

/** Where the agent is trying to get to, which outlives any single walk. */
type Intent = "wander" | "desk";

/**
 * What an off-duty agent has been talked into, or "none" for plain wandering.
 *
 * This is a sub-state of "idle", not a state of its own, and that distinction
 * is the whole design: the backend still owns whether an agent is working, and
 * an activity only ever refines the gap in between. Every method that takes a
 * task calls `release`, so a bit cannot survive a dispatch, and the director
 * (see idle.ts) is the only thing that ever sets one.
 */
export type IdleActivity = "none" | "coffee" | "lunch" | "chat" | "pingpong";

/**
 * What an agent is carrying, drawn in their hand.
 *
 * A mug is theirs and expires on a timer. A paddle belongs to the table. The
 * last two belong to a director: a folder of work is the thing that has to
 * come back, and a phone is up for as long as the call lasts.
 */
export type Held = "none" | "mug" | "paddle" | "files" | "phone";

/**
 * Whether a director is currently steering this agent, and how.
 *
 * Deliberately only two values and no detail. The scripted sequences -- a
 * handoff, a folder coming back -- keep their own state in their own slot (see
 * handoff.ts); all the agent needs to know is "somebody else is deciding where
 * I stand", which is what stops the aimless stroll below from walking them out
 * of it and what keeps the idle director from talking them into a coffee.
 *
 * "hold" is standing or sitting where they were put. "walk" is a leg of a
 * scripted route, which unlike a stroll is worth the full frame rate.
 */
export type Errand = "none" | "hold" | "walk";

/**
 * A short reaction played on the spot: winning a rally, or losing one.
 *
 * Runs on its own timer rather than a director's, because it has to outlive
 * the bit that caused it -- the point of a cheer is that it is still going
 * while both players walk away from the table.
 */
export type Emote = "none" | "cheer" | "sob";

/** How far outside its own bounds an agent's drawing can reach. */
const BOX_HALF_WIDTH = 46;
const BOX_ABOVE = 52;
const BOX_BELOW = 46;

/**
 * A worker in the office.
 *
 * Every field is a number or a string: instances are allocated when the scene
 * is built and then mutated in place, so the animation loop does not allocate.
 */
export class OfficeAgent {
  readonly id: string;

  /**
   * What the nameplate says. Editable from the agent's desk panel, so unlike
   * everything else here it can change without the scene being rebuilt -- see
   * the renderer's setAgentName.
   */
  name: string;

  /**
   * The agent's own picture, once it has been decoded, or null to draw the
   * colour blob instead. Most agents have no avatar, so null is the normal
   * case rather than a missing one.
   */
  avatar: HTMLImageElement | null = null;

  /**
   * The URL `avatar` was asked for. Held so a repeated push of the same URL is
   * a no-op, and so a load that finishes after the agent was pointed at a
   * different file can tell that it is no longer wanted.
   */
  avatarUrl = "";

  /** Body colour, and the two shades derived from it, computed once. */
  readonly colour: string;
  readonly colourDim: string;
  readonly colourSoft: string;

  /** Desk surface centre. */
  readonly deskX: number;
  readonly deskY: number;
  /**
   * True for the coordinator, who has a desk of a different size and shape.
   *
   * Carried on the agent because the geometry follows from it: how wide the
   * surface is, where the monitor stands on it, and how big a box to mark
   * dirty when the desk lights up. The role itself is the backend's; this is
   * only the one bit of it the drawing needs.
   */
  readonly boss: boolean;
  /** Where the agent stands when working. */
  readonly seatX: number;
  readonly seatY: number;

  x: number;
  y: number;
  targetX: number;
  targetY: number;

  /**
   * The planned walk: the points to hit, in order, of which the last is the
   * target. Filled by the planner in world.ts, which knows about doorways and
   * furniture; walking it is just "head for the next one".
   *
   * Preallocated and rewritten in place. A plan is made when a destination is
   * chosen -- a few times a minute per agent -- and never while walking.
   */
  private readonly legs: Point[] = Array.from({ length: MAX_LEGS }, () => ({ x: 0, y: 0 }));
  private legCount = 0;
  private legIndex = 0;

  state: VisualState = "idle";
  intent: Intent = "wander";

  /** What they are up to while off duty. Only ever set while state is "idle". */
  activity: IdleActivity = "none";

  /** What is in their hand. A mug outlives the errand that fetched it. */
  holding: Held = "none";

  /** Whether a director is steering them, and how. See Errand. */
  errand: Errand = "none";

  /** A reaction being played on the spot, or "none". */
  emote: Emote = "none";

  /** Seconds left in the reaction. */
  private emoteSeconds = 0;

  /** Seconds left before whatever they are holding is finished with. */
  private carrySeconds = 0;

  /**
   * Seconds since the thing in their hand was picked up.
   *
   * Counts up rather than down because a rule asks how fresh a coffee is, not
   * how long is left of it (see entry.ts). Meaningless while their hands are
   * empty, and reset the moment anything is put in them.
   */
  carriedFor = 0;

  /**
   * True when the agent is sat down somewhere that is not their desk -- at the
   * lunch table, for now.
   *
   * Separate from the seated look a working agent has, because that one is
   * derived from the task state and this one is a bit's business. Both end up
   * in the same place in the renderer: lower body, no walk cycle.
   */
  sitting = false;

  /**
   * The line they are saying, if any.
   *
   * Owned by the agent rather than by the bit that caused it, because a line
   * has to outlive its cause: a dispatched agent keeps answering Anton while
   * they cross the room, and a chat interrupted by a task should let its last
   * word fade rather than blink out.
   */
  readonly speech = new SpeechBubble();

  /**
   * Seconds a dispatched agent stays on their feet before starting work.
   *
   * A task arriving and a monitor lighting up in the same frame reads as the
   * agent having answered before being asked. The hold is what puts the spoken
   * line first; the walk to the desk usually covers it entirely.
   */
  private speakHold = 0;

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
  /**
   * Whether they were drawn seated.
   *
   * Sitting lowers the body six units without moving the agent, so standing up
   * changes pixels that neither `drawnX`/`drawnY` nor `drawnState` can see: a
   * waiting agent gets out of their chair while idle, and stays idle. Without
   * this the seated drawing is left on screen until something else happens to
   * dirty the same patch.
   */
  drawnSitting = false;
  /**
   * The bubble as it was last drawn.
   *
   * A bubble is wider than the agent's own box and it does not animate, so the
   * frame it changes is the only frame that touches those pixels -- and that
   * frame has to erase the bubble that is on screen, not the one replacing it.
   * The old width is the only thing that knows how much to wipe.
   */
  drawnSpeech = "";
  drawnSpeechWidth = 0;
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
   * This agent as a party of one, for the entry rules. Held rather than built
   * because it is read while picking a place to stroll to, which happens a few
   * times a minute per agent and must not allocate.
   */
  private readonly solo: readonly OfficeAgent[] = [this];

  /**
   * True when this agent's motion is worth the full frame rate: working,
   * finishing, failing, or crossing the room because it was given a task.
   *
   * An aimless stroll is not. It is the office's resting state, it runs for
   * hours, and at half rate nobody can tell.
   */
  get wantsSmoothFrames(): boolean {
    // A scripted walk is somebody being carried across the room by a
    // sequence, not a stroll: it is short, it is the thing being watched, and
    // it is the one idle-state walk worth the full rate. Standing waiting on
    // an errand is not -- Anton sits in his chair for most of a run, and
    // pinning the office at 30fps for that would buy nothing at all.
    if (this.errand === "walk") return true;
    if (this.emote !== "none") return true;
    if (this.state === "idle") return false;
    if (this.state === "walking" && this.intent === "wander") return false;
    return true;
  }

  /**
   * True when the agent animates in place, and so has to be repainted even
   * standing still: steam off a mug, a paddle waiting for the ball, typing
   * dots, a pulsing error ring.
   *
   * A conversation is deliberately not in here. Two agents standing with
   * static bubbles change no pixels, and an office at rest should cost the
   * same whether or not anybody is talking.
   */
  get animates(): boolean {
    // Nothing oscillates when movement is unwelcome, so nothing has to be
    // repainted for it. A working agent is the exception, and not really an
    // exception: their monitor is filling with text, and text arriving is
    // content rather than decoration.
    if (prefersReducedMotion()) return this.state === "working";
    if (this.state !== "idle") return true;
    // Steam off a mug, a fork going up and down, a bat held ready. A mug is in
    // here whether or not the errand that fetched it is still running: it goes
    // wandering with them and it is still steaming.
    if (this.holding !== "none") return true;
    // A seated body has a breath in it -- see the renderer's bob -- so anybody
    // sat down is repainted even though they are going nowhere. Every seated
    // agent used to be working or at lunch, both of which are already true
    // above; somebody waiting in a chair is neither.
    if (this.sitting) return true;
    // Arms up, or a tear going down. Both happen standing still, which is
    // exactly the case the dirty-rect pass would otherwise skip.
    if (this.emote !== "none") return true;
    return this.activity === "lunch" || this.activity === "pingpong";
  }

  constructor(opts: {
    id: string;
    name: string;
    colour: string;
    deskX: number;
    deskY: number;
    seatX: number;
    seatY: number;
    boss?: boolean;
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
    this.boss = opts.boss ?? false;

    // At the seat, not in front of it. A bottom-row desk's seat is already
    // near the back of the bullpen, and spawning an agent further out than
    // that put them off the floor entirely -- where nothing is walkable, no
    // route can be planned, and they stood still for the rest of the session.
    this.x = opts.seatX;
    this.y = opts.seatY;
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
    // Being spawned inside a desk, or in a doorway a wall has since been drawn
    // across, is possible if the layout changed. Nudge clear rather than
    // starting stuck: an agent standing somewhere unwalkable can plan no route
    // out of it, so it is stuck for good.
    if (!isWalkable(obstacles, this.x, this.y)) {
      for (let step = 20; step <= 180; step += 20) {
        // Out from the desk first, then back towards it: which way is clear
        // depends on whether the desk is at the front of the room or the back.
        if (this.tryStand(obstacles, this.seatX, this.seatY + step)) break;
        if (this.tryStand(obstacles, this.seatX, this.seatY - step)) break;
      }
      this.targetX = this.x;
      this.targetY = this.y;
      this.drawnX = this.x;
      this.drawnY = this.y;
      this.legCount = 0;
    }
  }

  /** Stands the agent at a spot if it is walkable. True when it took. */
  private tryStand(obstacles: readonly Rect[], x: number, y: number): boolean {
    if (!isWalkable(obstacles, x, y)) return false;
    this.x = x;
    this.y = y;
    return true;
  }

  /** The rectangle this agent's drawing occupies, in world units. */
  boxAt(x: number, y: number, out: Rect): Rect {
    out.x = x - BOX_HALF_WIDTH;
    out.y = y - BOX_ABOVE;
    out.w = BOX_HALF_WIDTH * 2;
    out.h = BOX_ABOVE + BOX_BELOW;
    return out;
  }

  /**
   * Send the agent to their desk and put them to work when they arrive.
   *
   * Whatever they were doing off duty ends here, mug included. The line they
   * answer with is set by the renderer, which is the only place that can
   * measure a bubble; this only reserves the beat it needs to be read in.
   */
  assign(): void {
    this.release();
    this.dismiss();
    this.clearHands();
    this.emote = "none";
    this.intent = "desk";
    this.state = "walking";
    this.speakHold = DISPATCH_HOLD_SECONDS;
    this.headForDesk();
  }

  /**
   * Told the work is theirs, and to wait where they stand for the folder.
   *
   * The gap between a task being assigned and its first output is real -- it
   * is the CLI starting up -- and this is what spends it on somebody bringing
   * the work over rather than on a walk to an empty desk. They keep their feet
   * and say nothing: the line belongs to the moment the folder changes hands.
   *
   * Nothing here is load-bearing. If the backend says they are producing
   * output before the folder arrives, the director hurries the exchange along
   * and calls `work` the moment it lands (see handoff.ts), so the wait costs a
   * beat and the order stays the one an office actually has: given the work,
   * then doing it.
   */
  awaitHandoff(): void {
    this.release();
    this.clearHands();
    this.emote = "none";
    this.intent = "desk";
    this.hold();
  }

  /**
   * At work, or on the way there.
   *
   * Measured against the seat rather than the current target, because an agent
   * who was at the coffee machine when this arrived is standing still and
   * "arrived" -- at the wrong end of the room. The walk is the same one
   * `assign` starts, and the arrival check is what honours the spoken line.
   */
  work(): void {
    this.release();
    this.dismiss();
    this.clearHands();
    this.emote = "none";
    this.intent = "desk";
    if (this.state === "working") return;
    if (this.nearSeat(6) && this.speakHold <= 0) {
      this.state = "working";
      return;
    }
    this.state = "walking";
    this.headForDesk();
  }

  /** Task done: hold at the desk briefly, then drift back to wandering. */
  finish(): void {
    this.release();
    this.state = "finished";
    this.timer = 1.4;
  }

  /** Task failed: stay put, show it, then drift back to wandering. */
  fail(): void {
    this.release();
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
      this.legCount = 0;
      this.x = this.targetX = this.drawnX = this.seatX;
      this.y = this.targetY = this.drawnY = this.seatY;
      return;
    }
    if (state === "walking") this.assign();
  }

  /** Back to aimless wandering. */
  idle(): void {
    this.release();
    this.intent = "wander";
    this.state = "idle";
    this.timer = rand(0.3, 1.2);
  }

  /**
   * Takes the agent on as part of an idle bit and sends them to a spot.
   *
   * False when there is no way through the furniture from where they are
   * standing, which is the director's signal to drop the whole idea rather
   * than walk somebody through a desk to reach a ping pong table.
   */
  join(activity: IdleActivity, x: number, y: number): boolean {
    if (!this.headTo(x, y)) return false;
    this.activity = activity;
    this.intent = "wander";
    this.state = "walking";
    return true;
  }

  /** Moves an agent already in a bit to another spot. */
  goto(x: number, y: number): boolean {
    if (!this.headTo(x, y)) return false;
    this.state = "walking";
    return true;
  }

  /**
   * True when a bit's walk has landed and the agent is standing waiting.
   *
   * Walking to a bit's spot ends in "idle" like any other stroll, and the idle
   * branch of `update` leaves an agent with an activity exactly where it was
   * put -- so standing still, with a bit in progress, is the arrival signal.
   */
  get settled(): boolean {
    return this.activity !== "none" && this.state === "idle";
  }

  /** Turns to look at something to the left or right of them. */
  face(x: number): void {
    if (Math.abs(x - this.x) < 1) return;
    this.facing = x > this.x ? 1 : -1;
  }

  /**
   * Ends any idle bit, leaving the agent standing where they are.
   *
   * Deliberately touches nothing but the idle sub-state: it is called from
   * every method that takes a task, and a mug being put down must not also
   * undo the walk to the desk that put it down.
   */
  release(): void {
    if (this.activity === "none") return;
    this.activity = "none";
    this.sitting = false;
    // The mug stays. Fetching a coffee and then carrying it about is the whole
    // point of the errand, so it outlives the bit and expires on its own timer;
    // a bat is the table's, and goes back on it. A folder is neither -- it
    // belongs to the handoff, which is the only thing allowed to take it back.
    if (this.holding === "paddle") this.holding = "none";
    this.timer = rand(0.3, 1.2);
  }

  /**
   * Stands the agent still under a director's hand, wherever they are.
   *
   * Cancels any walk in progress, which is the point: a scripted sequence
   * decides where its people are, and half a stroll left running would drift
   * them out from under it.
   */
  hold(): void {
    this.errand = "hold";
    this.legCount = 0;
    this.state = "idle";
  }

  /**
   * Sends the agent somewhere on a director's business. False when there is no
   * route, which is the signal to do the thing that needs no walk -- to phone
   * instead of visiting, in the handoff's case.
   */
  walk(x: number, y: number): boolean {
    if (!this.headTo(x, y)) return false;
    this.errand = "walk";
    this.intent = "wander";
    this.state = "walking";
    // Out of the chair first. A director can send somebody off from a seat --
    // Anton gets up out of his to take the next folder over -- and a walk that
    // left the flag set would draw him seated, crossing the room.
    this.sitting = false;
    return true;
  }

  /**
   * True when a scripted walk has landed and the agent is standing waiting.
   *
   * The same trick `settled` plays for idle bits: a director's walk ends in
   * "idle" like any other, and the idle branch of `update` leaves an agent on
   * an errand exactly where it put them -- so standing still, mid-errand, is
   * the arrival signal.
   */
  get arrived(): boolean {
    return this.errand !== "none" && this.state === "idle";
  }

  /**
   * Hands the agent back from a director, leaving them standing where they are.
   *
   * Deliberately does not take a folder out of their hands. A task landing
   * mid-errand ends the choreography, not the possession: the folder is the
   * thing that has to come back, and only the handoff -- which knows whether
   * it has been given back yet -- is allowed to make it disappear.
   */
  dismiss(): void {
    if (this.errand === "none") return;
    this.errand = "none";
    this.sitting = false;
    // A call ends when the errand does. There is nothing to return.
    if (this.holding === "phone") this.holding = "none";
    this.timer = rand(0.3, 1.2);
  }

  /**
   * Keeps the agent on a director's books without moving them.
   *
   * The counterpart to `dismiss`: every task transition ends an errand,
   * because a task always wins, but some claims outlive the turn that started
   * them -- a folder still owed back is one. This is how a director re-states
   * a claim on somebody who is busy, and it deliberately touches nothing else:
   * they are working, they are seated, and they stay both.
   */
  reserve(): void {
    if (this.errand === "none") this.errand = "hold";
  }

  /** Plays a reaction for a few seconds. */
  react(emote: Emote, seconds: number): void {
    this.emote = emote;
    this.emoteSeconds = seconds;
  }

  /** Puts something in the agent's hand for a while. */
  carry(item: Held, seconds: number): void {
    this.holding = item;
    this.carrySeconds = seconds;
    this.carriedFor = 0;
  }

  /** Empties their hands, whatever was in them. */
  putDown(): void {
    this.holding = "none";
    this.carrySeconds = 0;
    this.carriedFor = 0;
  }

  /**
   * Empties their hands of everything a task transition is entitled to take.
   *
   * A mug and a bat are the agent's own business, and being given work ends
   * both. A folder is not: it came off Anton's desk and it goes back to
   * Anton's desk, so a second task landing does not make the first one's
   * paperwork vanish -- the handoff clears it when the sequence closes,
   * however it ends.
   */
  private clearHands(): void {
    if (this.holding === "files") return;
    this.putDown();
  }

  /**
   * Advance by dt seconds. Called once per rendered frame per agent; allocates
   * nothing.
   */
  update(dt: number): void {
    this.clock += dt;
    this.speech.update(dt);
    if (this.speakHold > 0) this.speakHold -= dt;
    if (this.emoteSeconds > 0) {
      this.emoteSeconds -= dt;
      if (this.emoteSeconds <= 0) this.emote = "none";
    }
    if (this.carrySeconds > 0) {
      this.carrySeconds -= dt;
      this.carriedFor += dt;
      if (this.carrySeconds <= 0) this.putDown();
    }

    switch (this.state) {
      case "idle": {
        // Somebody in a bit stands where the director put them until it says
        // otherwise. This is what stops the wander below from walking an agent
        // out of a conversation it just walked them into.
        if (this.activity !== "none") return;

        // The same, for the other director: standing still on an errand is
        // how a handoff sees that a walk has landed, and is also Anton waiting
        // in his chair -- neither of which survives being strolled away from.
        if (this.errand !== "none") return;

        // No strolling for somebody who asked for less movement. They stay
        // where they are; the office is still a room full of people at desks,
        // it just stops fidgeting.
        if (prefersReducedMotion()) return;

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
          // Standing at the desk, still answering. The bubble goes up the
          // moment the task lands and this is what keeps them on their feet
          // under it, so the office never lights a monitor mid-sentence.
          if (this.speakHold > 0) return;
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
   * Aims at a destination, planning a walk that respects walls and furniture.
   *
   * Returns false when there is no clear route, which is the signal to want
   * something else instead: an idle agent has better options than shouldering
   * through a desk, and a bit that cannot reach its table should not start.
   */
  private headTo(x: number, y: number): boolean {
    this.targetX = x;
    this.targetY = y;
    this.legIndex = 0;
    this.legCount = planRoute(this.obstacles, this.x, this.y, x, y, this.legs);
    return this.legCount > 0;
  }

  /**
   * Aims at the agent's own desk, whatever is in the way.
   *
   * A task is not a suggestion, so this cannot fail: if no clear route exists
   * it falls back to the doorways plus a straight line, which may clip the
   * corner of somebody else's desk but will never walk through a wall.
   */
  private headForDesk(): void {
    this.targetX = this.seatX;
    this.targetY = this.seatY;
    this.legIndex = 0;

    // Movement unwelcome: skip the journey and keep the destination, which is
    // the usual bargain for a transition somebody has opted out of. They are
    // at their desk, on their feet, with their line still up -- the arrival
    // check in `update` puts them to work when it has been read.
    if (prefersReducedMotion()) {
      this.legCount = 0;
      this.x = this.seatX;
      this.y = this.seatY;
      return;
    }

    this.legCount = planRoute(
      this.obstacles,
      this.x,
      this.y,
      this.seatX,
      this.seatY,
      this.legs,
    );
    if (this.legCount === 0) {
      this.legCount = planDoors(this.x, this.y, this.seatX, this.seatY, this.legs);
    }
  }

  /** True when the agent is close enough to their own seat to sit down. */
  private nearSeat(epsilon: number): boolean {
    const dx = this.seatX - this.x;
    const dy = this.seatY - this.y;
    return dx * dx + dy * dy <= epsilon * epsilon;
  }

  /** Walks the plan. Returns true once the last point is reached. */
  private moveTowardsTarget(dt: number): boolean {
    if (this.legCount === 0) return true;
    const leg = this.legs[this.legIndex];

    const dx = leg.x - this.x;
    const dy = leg.y - this.y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist < 1.5) {
      this.x = leg.x;
      this.y = leg.y;
      if (this.legIndex < this.legCount - 1) {
        // Corner turned, or doorway crossed; on to the next one.
        this.legIndex++;
        return false;
      }
      this.legCount = 0;
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
      // A stroll is one agent going somewhere on their own, so a room that
      // asks for a party of two, or for a coffee in hand, is not somewhere a
      // stroll may end (see entry.ts). Today no wander target is in the wing
      // anyway; this is what keeps that true if the range ever widens.
      if (!admits(this.solo, x, y)) continue;
      if (this.headTo(x, y)) return true;
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
