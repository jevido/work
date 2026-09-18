import type { Held, IdleActivity, OfficeAgent } from "./agent";
import {
  callLine,
  coffeeLine,
  loseLine,
  openerLine,
  replyLine,
  serveLine,
  SPEECH_SECONDS,
  winLine,
} from "./speech";
import {
  COFFEE,
  FRIDGE,
  LUNCH,
  MEETING,
  coffeeStands,
  lunchSeats,
  meetingSpots,
  pongStands,
  type Prop,
  type Props,
} from "./props";
import { admits, allCarrying, entryRule } from "./entry";
import {
  isWalkable,
  prefersReducedMotion,
  type Point,
  type Rect,
  type RoomId,
} from "./world";

/**
 * What the office does with itself when nobody has given it any work.
 *
 * Agents already wander on their own (see OfficeAgent's idle state). This adds
 * the things that need more than one agent, or a room, or a piece of furniture
 * to make sense: fetching a coffee and carrying it about, sitting down to
 * lunch in the break room, a rally at the ping pong table, two people stopping
 * to talk -- in the meeting room if it is that kind of conversation.
 *
 * It is a director, not a second state machine -- it only ever steers
 * agents the task state machine has left idle, it hands them back the instant
 * a task arrives, and every line it puts in a bubble is written in speech.ts.
 *
 * Nothing here costs a token. Nothing here reaches the backend. The most
 * expensive thing in the file is a walkability check when a bit starts, which
 * happens a couple of times a minute.
 */

/** Bits in flight at once. Pacing keeps the real number near zero or one. */
const MAX_BITS = 3;

/** How many kinds of bit there are to choose between. */
const KINDS = 4;

/** Seconds between attempts to start something. */
const MIN_GAP = 11;
const MAX_GAP = 26;

/** Shorter retry after an attempt that could not find room for itself. */
const RETRY_GAP = 4;

/**
 * Longest a walk to a bit's spot may take before the bit is abandoned.
 *
 * Generous on purpose. The coffee station is in the wing, which from the far
 * desks is the width of the office plus a doorway -- getting on for fifteen
 * seconds -- and a watchdog that fired first would quietly mean only the
 * agents who sit near the door ever got a coffee.
 */
const TRAVEL_LIMIT = 28;

/** How far apart two agents stand to talk, in world units. */
const CHAT_GAP = 46;

/** Furthest apart two agents will be before a chat is not worth the walk. */
const CHAT_RANGE = 340;

/** How long a fetched coffee stays in an agent's hand afterwards. */
const CARRY_MIN = 34;
const CARRY_MAX = 70;

/** How long somebody stands at the machine while it pours. */
const POUR_MIN = 2.6;
const POUR_MAX = 4.2;

/** How long lunch takes, and the gap between remarks over it. */
const LUNCH_MIN = 11;
const LUNCH_MAX = 20;
const LUNCH_REMARK_MIN = 3.5;
const LUNCH_REMARK_MAX = 7;

/** How often a chat happens in the meeting room instead of on the spot. */
const MEETING_CHANCE = 0.4;

/** Seconds one ball flight takes, and how high it arcs. */
const VOLLEY_SECONDS = 0.42;
const VOLLEY_LIFT = 16;

/**
 * How long the winner celebrates and the loser does not.
 *
 * The cheer is the longer of the two on purpose: both players stay at the
 * table until it ends, so the loser has finished sulking and is standing there
 * watching by the time they both walk off. A sob that outlasted the cheer
 * would end the bit on the losing player, which is the wrong note to leave a
 * game on.
 */
const CHEER_SECONDS = 1.5;
const SOB_SECONDS = 1;

/** How far above the table the ball is struck. */
const BALL_HEIGHT = 12;

const PHASE_TRAVEL = 0;
const PHASE_ACT = 1;
/**
 * Walking to where something is picked up, and standing there while it is.
 *
 * These two come before PHASE_TRAVEL for a bit whose destination has an entry
 * rule to satisfy first (see entry.ts), and are the whole of the plain coffee
 * errand, which is that prelude with nothing after it.
 */
const PHASE_FETCH = 2;
const PHASE_POUR = 3;
/**
 * The beat after a rally is decided: one player cheering, the other not.
 *
 * A rally used to stop with somebody saying "Point." and everybody walking
 * off, which made the game a screensaver -- nothing was at stake because
 * nothing happened at the end of it. This is the phase that gives it a result.
 */
const PHASE_CELEBRATE = 4;

/** Sets a line on an agent. The renderer supplies it: only it can measure text. */
export type Speaker = (agent: OfficeAgent, text: string, seconds: number) => void;

/**
 * One thing happening, in a slot reused for the next thing.
 *
 * Pooled rather than allocated because the render loop reads these every
 * frame, and a bit starting is a state transition -- allocating there would be
 * the one piece of garbage the office produces while it sits idle.
 */
class IdleBit {
  kind: IdleActivity = "none";
  a: OfficeAgent | null = null;
  b: OfficeAgent | null = null;

  /**
   * The room this bit is headed for, when that room has rules about who may be
   * in it. Null for anything happening where it happens to happen.
   */
  room: RoomId | null = null;

  phase = PHASE_TRAVEL;
  /** Seconds left in the current phase, or the travel watchdog. */
  timer = 0;
  /** Lines still to say, or volleys still to play. */
  turns = 0;
  /** Which of the pair speaks next. */
  speaker = 0;
  /** True once the opening line has been said, so the rest can be replies. */
  opened = false;
  /** Seconds until somebody says something over their lunch. */
  remark = 0;

  ballX = 0;
  ballY = 0;
  private ballFromX = 0;
  private ballToX = 0;
  private ballT = 0;

  /**
   * Where the ball was last drawn.
   *
   * The ball crosses the table in under half a second, which at any frame rate
   * this renderer uses is further than its own width per frame -- so the patch
   * to repaint is the whole leg it just flew, not the dot at the end of it.
   */
  private drawnBallX = 0;
  private drawnBallY = 0;
  private drawnBall = false;

  get live(): boolean {
    return this.kind !== "none";
  }

  /** True while there is a ball in the air worth drawing. */
  get showsBall(): boolean {
    return this.kind === "pingpong" && this.phase === PHASE_ACT;
  }

  /** True while the ball, or the last of it, still owes the screen a repaint. */
  get ballIsDirty(): boolean {
    return this.showsBall || this.drawnBall;
  }

  /** The ball's flight since it was last drawn, for the dirty-rect pass. */
  ballRect(out: Rect): Rect {
    const pad = 5;
    const fromX = this.drawnBall ? this.drawnBallX : this.ballX;
    const fromY = this.drawnBall ? this.drawnBallY : this.ballY;
    const x0 = Math.min(fromX, this.ballX);
    const y0 = Math.min(fromY, this.ballY);
    out.x = x0 - pad;
    out.y = y0 - pad;
    out.w = Math.abs(this.ballX - fromX) + pad * 2;
    out.h = Math.abs(this.ballY - fromY) + pad * 2;
    return out;
  }

  /** Records the ball as painted. Called once a frame, after drawing. */
  markDrawn(): void {
    this.drawnBall = this.showsBall;
    this.drawnBallX = this.ballX;
    this.drawnBallY = this.ballY;
  }

  serve(fromX: number, toX: number, y: number): void {
    this.ballFromX = fromX;
    this.ballToX = toX;
    this.ballT = 0;
    this.ballX = fromX;
    this.ballY = y - BALL_HEIGHT;
  }

  /** Flies the ball. Returns true on the frame it lands. */
  flyBall(dt: number, y: number): boolean {
    this.ballT += dt / VOLLEY_SECONDS;
    const t = this.ballT > 1 ? 1 : this.ballT;
    this.ballX = this.ballFromX + (this.ballToX - this.ballFromX) * t;
    this.ballY = y - BALL_HEIGHT - Math.sin(Math.PI * t) * VOLLEY_LIFT;
    if (this.ballT < 1) return false;
    // Bounce back the other way; the caller decides whether the rally goes on.
    const from = this.ballFromX;
    this.ballFromX = this.ballToX;
    this.ballToX = from;
    this.ballT = 0;
    return true;
  }

  reset(): void {
    this.kind = "none";
    this.a = null;
    this.b = null;
    this.room = null;
    this.phase = PHASE_TRAVEL;
    this.timer = 0;
    this.turns = 0;
    this.speaker = 0;
    this.opened = false;
    this.remark = 0;
    // drawnBall is left alone: the ball is off screen now, and the frame that
    // notices still has to wipe where it last was.
  }
}

export class IdleDirector {
  /** Read by the renderer to draw whatever a bit puts on screen. */
  readonly bits: readonly IdleBit[] = Array.from({ length: MAX_BITS }, () => new IdleBit());

  private agents: readonly OfficeAgent[] = [];
  private obstacles: readonly Rect[] = [];
  private props: Props = { coffee: COFFEE, fridge: FRIDGE, lunch: LUNCH, meeting: MEETING, pong: null };
  private cooldown = rand(MIN_GAP / 2, MIN_GAP);

  /** Puts a line in an agent's bubble. See Speaker. */
  private readonly say: Speaker;

  constructor(say: Speaker) {
    this.say = say;
  }

  /** True while anything in flight is worth the full frame rate. */
  get wantsSmoothFrames(): boolean {
    for (const bit of this.bits) if (bit.showsBall) return true;
    return false;
  }

  /**
   * Points the director at a new cast and room. Everything in flight is
   * dropped: the agents it was steering no longer exist.
   */
  setScene(agents: readonly OfficeAgent[], obstacles: readonly Rect[], props: Props): void {
    for (const bit of this.bits) bit.reset();
    this.agents = agents;
    this.obstacles = obstacles;
    this.props = props;
    this.cooldown = rand(MIN_GAP / 2, MIN_GAP);
  }

  update(dt: number): void {
    // All of this is decoration -- it carries nothing the console does not
    // already say -- so a request for less movement switches it off rather
    // than toning it down, and takes what is already running with it. Waiting
    // for the rally to finish would be answering the question later.
    if (prefersReducedMotion()) {
      for (const bit of this.bits) if (bit.live) this.cancel(bit);
      this.cooldown = MAX_GAP;
      return;
    }

    for (const bit of this.bits) if (bit.live) this.advance(bit, dt);

    this.cooldown -= dt;
    if (this.cooldown > 0) return;
    this.cooldown = this.start() ? rand(MIN_GAP, MAX_GAP) : RETRY_GAP;
  }

  /** Tries to start one bit. False when the room had no room for it. */
  private start(): boolean {
    const slot = this.bits.find((b) => !b.live);
    if (!slot) return false;

    // Shuffled by picking a starting point: over an evening every bit gets its
    // turn without any of them owning a fixed share of the office's attention.
    const order = Math.floor(Math.random() * KINDS);
    for (let i = 0; i < KINDS; i++) {
      switch ((order + i) % KINDS) {
        case 0:
          if (this.startChat(slot)) return true;
          break;
        case 1:
          if (this.startCoffee(slot)) return true;
          break;
        case 2:
          if (this.startLunch(slot)) return true;
          break;
        default:
          if (this.startPingPong(slot)) return true;
      }
    }
    return false;
  }

  /**
   * True when an agent is off duty and free to be talked into something.
   *
   * An agent mid-stroll counts: they were walking nowhere in particular, and
   * being redirected is exactly what a colleague calling over does.
   */
  private free(a: OfficeAgent): boolean {
    if (a.activity !== "none") return false;
    // Somebody on a scripted errand is off duty as far as the task machine is
    // concerned and very much not free: Anton crossing the room with a folder,
    // or waiting in his chair for one to come back, is the whole point of what
    // he is doing. See handoff.ts, which is the other director in this office.
    if (a.errand !== "none") return false;
    return a.state === "idle" || (a.state === "walking" && a.intent === "wander");
  }

  private startCoffee(slot: IdleBit): boolean {
    // Somebody already holding a coffee does not need another one.
    const candidates = this.agents.filter((a) => this.free(a) && a.holding === "none");
    if (candidates.length === 0) return false;

    const agent = candidates[Math.floor(Math.random() * candidates.length)];
    const pickup = this.pickupFor("mug", 1);
    if (!pickup) return false;
    if (!this.sendParty(this.toParty(agent, null), "coffee", pickup.spots)) return false;

    slot.kind = "coffee";
    slot.a = agent;
    slot.phase = PHASE_FETCH;
    slot.timer = TRAVEL_LIMIT;
    return true;
  }

  /**
   * Lunch: one or two agents walk to the break room, sit down at the table and
   * eat, with the odd remark over it.
   *
   * Only one sitting at a time, which is how two bits are kept off the same
   * bench spot without any shared bookkeeping.
   */
  private startLunch(slot: IdleBit): boolean {
    if (this.bits.some((b) => b.kind === "lunch")) return false;

    const candidates = this.agents.filter((a) => this.free(a));
    if (candidates.length === 0) return false;

    const seats = lunchSeats(this.props.lunch);
    const first = candidates[Math.floor(Math.random() * candidates.length)];
    const second =
      candidates.length > 1 && Math.random() < 0.65
        ? candidates.find((a) => a !== first) ?? null
        : null;

    // Neighbouring seats, so two diners are sat together rather than at
    // opposite ends of an empty table.
    const party = this.toParty(first, second);
    const start = Math.floor(Math.random() * (seats.length - (second ? 1 : 0)));
    if (!this.sendParty(party, "lunch", seats.slice(start, start + party.length))) return false;

    slot.kind = "lunch";
    slot.a = first;
    slot.b = second;
    slot.phase = PHASE_TRAVEL;
    slot.timer = TRAVEL_LIMIT;
    return true;
  }

  private startChat(slot: IdleBit): boolean {
    const pair = this.pickPair(CHAT_RANGE);
    if (!pair) return false;
    const [a, b] = pair;

    // Some conversations are worth a room. The rest happen where the two of
    // them happen to be standing, which is most of them.
    if (Math.random() < MEETING_CHANCE && this.startMeeting(slot, a, b)) return true;

    // Somewhere between them that both can reach and stand at. Jittered,
    // because the exact midpoint puts every conversation in the same handful
    // of places once the desks are fixed.
    for (let attempt = 0; attempt < 6; attempt++) {
      const midX = (a.x + b.x) / 2 + rand(-30, 30);
      const midY = (a.y + b.y) / 2 + rand(-22, 22);
      const leftX = midX - CHAT_GAP / 2;
      const rightX = midX + CHAT_GAP / 2;
      if (!isWalkable(this.obstacles, leftX, midY)) continue;
      if (!isWalkable(this.obstacles, rightX, midY)) continue;

      // Whoever is further left takes the left side, so nobody crosses over.
      const [left, right] = a.x <= b.x ? [a, b] : [b, a];
      const spots = [
        { x: leftX, y: midY },
        { x: rightX, y: midY },
      ];
      if (!this.sendParty(this.toParty(left, right), "chat", spots)) continue;

      this.beginChat(slot, left, right);
      return true;
    }
    return false;
  }

  /**
   * The same conversation, held in the meeting room -- which has house rules.
   *
   * The room asks for a party of two, each with a fresh coffee (entry.ts).
   * A pair already holding one walks straight in; otherwise the bit starts at
   * the coffee machine and the walk to the meeting room is the second half of
   * it. Nothing here is written for that particular rule: it reads the room's
   * rule, checks it, and runs the errand the rule implies.
   */
  private startMeeting(slot: IdleBit, a: OfficeAgent, b: OfficeAgent): boolean {
    const rule = entryRule("meeting");
    const [near, far] = a.x <= b.x ? [a, b] : [b, a];
    const party = this.toParty(near, far);
    if (party.length < rule.minParty) return false;

    const spots = this.roomSpots("meeting", party.length);
    if (!spots) return false;

    if (allCarrying(party, rule)) {
      if (!this.sendParty(party, "chat", spots)) return false;
      this.beginChat(slot, near, far);
      slot.room = "meeting";
      return true;
    }

    // Everybody goes to the machine, not only whoever is short of a cup: the
    // rule is about who walks in together, so the party stays a party for the
    // errand too.
    const pickup = this.pickupFor(rule.carrying, party.length);
    if (!pickup) return false;
    if (!this.sendParty(party, "chat", pickup.spots)) return false;
    this.beginChat(slot, near, far);
    slot.room = "meeting";
    slot.phase = PHASE_FETCH;
    slot.timer = TRAVEL_LIMIT;
    return true;
  }

  /** Fills a slot with a conversation, wherever the two of them are heading. */
  private beginChat(slot: IdleBit, left: OfficeAgent, right: OfficeAgent): void {
    slot.kind = "chat";
    slot.a = left;
    slot.b = right;
    slot.phase = PHASE_TRAVEL;
    slot.timer = TRAVEL_LIMIT;
    slot.turns = 2 + Math.floor(Math.random() * 3);
    slot.speaker = Math.random() < 0.5 ? 0 : 1;
    slot.opened = false;
  }

  private startPingPong(slot: IdleBit): boolean {
    const table = this.props.pong;
    if (!table) return false;
    const pair = this.pickPair(Number.POSITIVE_INFINITY);
    if (!pair) return false;

    const [nearSide, farSide] = pongStands(table);
    const [a, b] = pair;
    // Each takes the end they are already closer to.
    const [left, right] = Math.abs(a.x - nearSide.x) <= Math.abs(b.x - nearSide.x) ? [a, b] : [b, a];

    if (!this.sendParty(this.toParty(left, right), "pingpong", [nearSide, farSide])) return false;
    left.holding = "paddle";
    right.holding = "paddle";

    slot.kind = "pingpong";
    slot.a = left;
    slot.b = right;
    slot.phase = PHASE_TRAVEL;
    slot.timer = TRAVEL_LIMIT;
    slot.turns = 5 + Math.floor(Math.random() * 6);
    return true;
  }

  /**
   * A party as a list, in one array that is refilled rather than replaced.
   *
   * Borrowed, not kept: the caller uses it and lets go of it, because the next
   * caller gets the same array back. It exists because a bit mid-errand asks
   * for its party every frame, and an idle office is supposed to allocate
   * nothing at all.
   */
  private readonly castScratch: OfficeAgent[] = [];

  private toParty(a: OfficeAgent | null, b: OfficeAgent | null): readonly OfficeAgent[] {
    const out = this.castScratch;
    out.length = 0;
    if (a) out.push(a);
    if (b) out.push(b);
    return out;
  }

  /**
   * Walks a party to one spot each, all or nothing.
   *
   * Two things every bit needs and each used to do for itself. The rollback:
   * an agent who has joined a bit is off duty with nothing steering them, so a
   * party that only half arrives has to be handed back rather than left
   * standing in the middle of the office. And the house rules: this is the one
   * place a bit sends anybody anywhere, so it is the place to ask whether the
   * room they are being sent to will have them (entry.ts).
   *
   * A party turned away here is a bit that does not start -- the director tries
   * something else, or the same thing from somewhere else, a few seconds later.
   */
  private sendParty(
    party: readonly OfficeAgent[],
    activity: IdleActivity,
    spots: readonly Point[],
  ): boolean {
    if (spots.length < party.length) return false;
    for (let i = 0; i < party.length; i++) {
      if (!admits(party, spots[i].x, spots[i].y)) return false;
    }
    for (let i = 0; i < party.length; i++) {
      if (party[i].join(activity, spots[i].x, spots[i].y)) continue;
      for (let j = 0; j < i; j++) party[j].release();
      return false;
    }
    return true;
  }

  /**
   * Where an item comes from: the furniture it is picked up at, and a spot at
   * it for each member of the party. Null when the office has nowhere to get
   * one, which is the answer for anything a rule asks for that is not a drink.
   */
  private pickupFor(item: Held, count: number): { at: Prop; spots: readonly Point[] } | null {
    if (item !== "mug") return null;
    return { at: this.props.coffee, spots: coffeeStands(this.props.coffee, count) };
  }

  /**
   * Where a party stands once inside a room, one spot each, or null when the
   * room has nowhere to put that many.
   */
  private roomSpots(room: RoomId, count: number): readonly Point[] | null {
    if (room !== "meeting") return null;
    const spots = meetingSpots(this.props.meeting);
    return spots.length >= count ? spots : null;
  }

  /** Two free agents within range of each other, or null. */
  private pickPair(range: number): [OfficeAgent, OfficeAgent] | null {
    const candidates = this.agents.filter((a) => this.free(a));
    if (candidates.length < 2) return null;

    const first = candidates[Math.floor(Math.random() * candidates.length)];
    let best: OfficeAgent | null = null;
    let bestDistance = range;
    for (const other of candidates) {
      if (other === first) continue;
      const distance = Math.hypot(other.x - first.x, other.y - first.y);
      if (distance > bestDistance) continue;
      best = other;
      bestDistance = distance;
    }
    return best ? [first, best] : null;
  }

  private advance(bit: IdleBit, dt: number): void {
    // A task landing calls release on the agent, which is how a bit learns it
    // has lost somebody: whoever is left is handed back and the slot is freed.
    if (bit.a?.activity !== bit.kind || (bit.b && bit.b.activity !== bit.kind)) {
      this.cancel(bit);
      return;
    }

    switch (bit.kind) {
      case "coffee":
        this.advanceCoffee(bit, dt);
        return;
      case "lunch":
        this.advanceLunch(bit, dt);
        return;
      case "chat":
        this.advanceChat(bit, dt);
        return;
      case "pingpong":
        this.advancePingPong(bit, dt);
        return;
    }
  }

  private advanceCoffee(bit: IdleBit, dt: number): void {
    if (!this.pour(bit, dt, this.toParty(bit.a, null), "mug")) return;

    // The cup is the point of the errand, so it leaves with them: they go back
    // to wandering holding it, and it expires on its own clock a minute or so
    // later. `release` leaves a mug alone for exactly this reason.
    this.cancel(bit);
  }

  /**
   * Walks a party to where an item is kept and puts one in each of their
   * hands: over to the counter, then standing there while it pours.
   *
   * Shared by the plain coffee errand and by any room whose entry rule asks
   * for something in hand. Both are the same two beats and differ only in what
   * happens once everybody has one.
   *
   * Returns true on the frame the last cup lands. A walk that takes too long,
   * or an item the office cannot supply, cancels the bit outright -- so a
   * false return means "not yet, and possibly never", which is all a caller
   * ever has to do anything about.
   */
  private pour(
    bit: IdleBit,
    dt: number,
    party: readonly OfficeAgent[],
    item: Held,
  ): boolean {
    const pickup = this.pickupFor(item, party.length);
    if (!pickup) {
      this.cancel(bit);
      return false;
    }

    if (bit.phase === PHASE_FETCH) {
      bit.timer -= dt;
      if (bit.timer <= 0) {
        this.cancel(bit);
        return false;
      }
      for (const agent of party) if (!agent.settled) return false;
      // At the counter, facing it, while it pours.
      for (const agent of party) agent.face(pickup.at.x);
      if (Math.random() < 0.45) this.say(party[0], coffeeLine(), SPEECH_SECONDS);
      bit.phase = PHASE_POUR;
      bit.timer = rand(POUR_MIN, POUR_MAX);
      return false;
    }

    bit.timer -= dt;
    if (bit.timer > 0) return false;
    for (const agent of party) agent.carry(item, rand(CARRY_MIN, CARRY_MAX));
    return true;
  }

  /**
   * The errand a room's rule imposes before its door: fetch whatever it asks
   * for, then go in together.
   *
   * The rule is tested again at the door rather than trusted from when the bit
   * started, because a walk across the office and a queue at the machine
   * happen in between, and a colleague can be called away to a task in the
   * middle of them. This is the check that makes "never alone, coffee in hand"
   * true of the room rather than merely intended by whoever set the bit up.
   */
  private advanceEntry(bit: IdleBit, dt: number): void {
    const room = bit.room!;
    const rule = entryRule(room);
    const party = this.toParty(bit.a, bit.b);
    if (!this.pour(bit, dt, party, rule.carrying)) return;

    const spots = this.roomSpots(room, party.length);
    if (!spots || !admits(party, spots[0].x, spots[0].y)) {
      this.cancel(bit);
      return;
    }
    for (let i = 0; i < party.length; i++) {
      if (party[i].goto(spots[i].x, spots[i].y)) continue;
      this.cancel(bit);
      return;
    }
    bit.phase = PHASE_TRAVEL;
    bit.timer = TRAVEL_LIMIT;
  }

  /**
   * Lunch: sit down, eat, say something occasionally, get up.
   *
   * Sitting is a flag on the agent rather than a state, because as far as the
   * task machine is concerned they are idle and interruptible -- a task landing
   * gets them out of their chair through the same `release` as everything else.
   */
  private advanceLunch(bit: IdleBit, dt: number): void {
    const a = bit.a!;
    const b = bit.b;

    if (bit.phase === PHASE_TRAVEL) {
      bit.timer -= dt;
      if (bit.timer <= 0) {
        this.cancel(bit);
        return;
      }
      if (!a.settled || (b && !b.settled)) return;

      a.sitting = true;
      if (b) {
        b.sitting = true;
        a.face(b.x);
        b.face(a.x);
      }
      bit.phase = PHASE_ACT;
      bit.timer = rand(LUNCH_MIN, LUNCH_MAX);
      bit.remark = rand(1.5, LUNCH_REMARK_MAX);
      return;
    }

    bit.timer -= dt;
    if (bit.timer <= 0) {
      this.cancel(bit);
      return;
    }

    // Talking over lunch, if there is anybody to talk to.
    if (!b) return;
    bit.remark -= dt;
    if (bit.remark > 0) return;
    const talker = bit.speaker === 0 ? a : b;
    this.say(talker, bit.opened ? replyLine() : openerLine(), SPEECH_SECONDS);
    bit.opened = true;
    bit.speaker = bit.speaker === 0 ? 1 : 0;
    bit.remark = rand(LUNCH_REMARK_MIN, LUNCH_REMARK_MAX);
  }

  private advanceChat(bit: IdleBit, dt: number): void {
    const a = bit.a!;
    const b = bit.b!;

    // A conversation in a room with an entry rule starts at the coffee machine.
    if (bit.phase === PHASE_FETCH || bit.phase === PHASE_POUR) {
      this.advanceEntry(bit, dt);
      return;
    }

    if (bit.phase === PHASE_TRAVEL) {
      bit.timer -= dt;
      if (bit.timer <= 0) {
        this.cancel(bit);
        return;
      }
      if (!a.settled || !b.settled) return;
      a.face(b.x);
      b.face(a.x);
      bit.phase = PHASE_ACT;
      // Straight into the first line: they have just walked over to each other
      // and a silent pause first reads as two people who forgot why.
      bit.timer = 0;
      return;
    }

    bit.timer -= dt;
    if (bit.timer > 0) return;
    if (bit.turns <= 0) {
      this.cancel(bit);
      return;
    }

    const talker = bit.speaker === 0 ? a : b;
    const listener = bit.speaker === 0 ? b : a;
    // The first line opens; every line after it answers one. Splitting the
    // pools that way is what lets any reply follow any opener and still read
    // as a conversation.
    this.say(talker, bit.opened ? replyLine() : openerLine(), SPEECH_SECONDS);
    bit.opened = true;
    talker.face(listener.x);
    bit.speaker = bit.speaker === 0 ? 1 : 0;
    bit.turns--;
    bit.timer = rand(1.9, 2.7);
  }

  private advancePingPong(bit: IdleBit, dt: number): void {
    const a = bit.a!;
    const b = bit.b!;
    const table = this.props.pong;
    if (!table) {
      this.cancel(bit);
      return;
    }

    if (bit.phase === PHASE_TRAVEL) {
      bit.timer -= dt;
      if (bit.timer <= 0) {
        this.cancel(bit);
        return;
      }
      if (!a.settled || !b.settled) return;
      a.face(table.x);
      b.face(table.x);
      bit.phase = PHASE_ACT;
      bit.serve(a.x, b.x, table.y);
      if (Math.random() < 0.5) this.say(a, serveLine(), SPEECH_SECONDS);
      return;
    }

    if (bit.phase === PHASE_CELEBRATE) {
      // Left to their reactions. Neither is steered here: the emotes run on
      // the agents' own clocks, so the cheer carries on while they walk away.
      bit.timer -= dt;
      if (bit.timer <= 0) this.cancel(bit);
      return;
    }

    // Both keep their eye on the table, which is between them either way.
    a.face(table.x);
    b.face(table.x);

    if (!bit.flyBall(dt, table.y)) return;
    bit.turns--;

    // Mid-rally, and somebody occasionally says so.
    if (bit.turns > 0) {
      if (bit.turns === 1 && Math.random() < 0.5) {
        this.say(Math.random() < 0.5 ? a : b, callLine(), SPEECH_SECONDS);
      }
      return;
    }

    // The last ball has landed and nobody sent it back: that is the point, and
    // who won it is a coin toss. Deliberately decided here rather than at the
    // serve -- a rally whose result was already fixed while it was still being
    // played would be the same animation with a secret.
    const [winner, loser] = Math.random() < 0.5 ? [a, b] : [b, a];
    winner.react("cheer", CHEER_SECONDS);
    loser.react("sob", SOB_SECONDS);
    this.say(winner, winLine(), SPEECH_SECONDS);
    this.say(loser, loseLine(), SPEECH_SECONDS);
    // Turning to face each other is what makes it an exchange rather than two
    // people reacting to the wall.
    winner.face(loser.x);
    loser.face(winner.x);

    bit.phase = PHASE_CELEBRATE;
    bit.timer = CHEER_SECONDS;
  }

  /**
   * Hands both agents back to plain wandering and frees the slot.
   *
   * Whoever is left in a room they were escorted into walks out of it on their
   * own: an idle agent strolls near their own desk, and every desk is in the
   * bullpen. They cannot wander back in, because a stroll is a party of one
   * and the rules turn those away at the door (see OfficeAgent's wander).
   */
  private cancel(bit: IdleBit): void {
    bit.a?.release();
    bit.b?.release();
    bit.reset();
  }
}

function rand(min: number, max: number): number {
  return min + Math.random() * (max - min);
}
