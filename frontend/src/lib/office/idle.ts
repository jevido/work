import type { IdleActivity, OfficeAgent } from "./agent";
import {
  callLine,
  coffeeLine,
  openerLine,
  replyLine,
  serveLine,
  SPEECH_SECONDS,
} from "./speech";
import {
  COFFEE,
  FRIDGE,
  LUNCH,
  MEETING,
  coffeeStand,
  lunchSeats,
  meetingSpots,
  pongStands,
  type Props,
} from "./props";
import { isWalkable, prefersReducedMotion, type Point, type Rect } from "./world";

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

/** How far above the table the ball is struck. */
const BALL_HEIGHT = 12;

const PHASE_TRAVEL = 0;
const PHASE_ACT = 1;

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
    return a.state === "idle" || (a.state === "walking" && a.intent === "wander");
  }

  private startCoffee(slot: IdleBit): boolean {
    const machine = this.props.coffee;
    // Somebody already holding a coffee does not need another one.
    const candidates = this.agents.filter((a) => this.free(a) && a.holding === "none");
    if (candidates.length === 0) return false;

    const agent = candidates[Math.floor(Math.random() * candidates.length)];
    const stand = coffeeStand(machine);
    if (!agent.join("coffee", stand.x, stand.y)) return false;

    slot.kind = "coffee";
    slot.a = agent;
    slot.phase = PHASE_TRAVEL;
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
    const start = Math.floor(Math.random() * (seats.length - (second ? 1 : 0)));
    if (!first.join("lunch", seats[start].x, seats[start].y)) return false;
    if (second && !second.join("lunch", seats[start + 1].x, seats[start + 1].y)) {
      first.release();
      return false;
    }

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
      if (!left.join("chat", leftX, midY)) continue;
      if (!right.join("chat", rightX, midY)) {
        left.release();
        continue;
      }

      this.beginChat(slot, left, right);
      return true;
    }
    return false;
  }

/** The same conversation, held across the meeting table. */
  private startMeeting(slot: IdleBit, a: OfficeAgent, b: OfficeAgent): boolean {
    const [left, right] = meetingSpots(this.props.meeting);
    const [near, far] = a.x <= b.x ? [a, b] : [b, a];
    if (!near.join("chat", left.x, left.y)) return false;
    if (!far.join("chat", right.x, right.y)) {
      near.release();
      return false;
    }
    this.beginChat(slot, near, far);
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

    if (!left.join("pingpong", nearSide.x, nearSide.y)) return false;
    if (!right.join("pingpong", farSide.x, farSide.y)) {
      left.release();
      return false;
    }
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
    const agent = bit.a!;
    const machine = this.props.coffee;

    if (bit.phase === PHASE_TRAVEL) {
      bit.timer -= dt;
      if (bit.timer <= 0) {
        this.cancel(bit);
        return;
      }
      if (!agent.settled) return;
      // At the counter, facing it, while it pours.
      agent.face(machine.x);
      if (Math.random() < 0.45) this.say(agent, coffeeLine(), SPEECH_SECONDS);
      bit.phase = PHASE_ACT;
      bit.timer = rand(2.6, 4.2);
      return;
    }

    bit.timer -= dt;
    if (bit.timer > 0) return;

    // The cup is the point of the errand, so it leaves with them: they go back
    // to wandering holding it, and it expires on its own clock a minute or so
    // later. `release` leaves a mug alone for exactly this reason.
    agent.carry("mug", rand(CARRY_MIN, CARRY_MAX));
    this.cancel(bit);
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

    // Both keep their eye on the table, which is between them either way.
    a.face(table.x);
    b.face(table.x);

    if (!bit.flyBall(dt, table.y)) return;
    bit.turns--;
    if (bit.turns > 0) return;

    // Rallies end with somebody claiming something.
    if (Math.random() < 0.6) this.say(Math.random() < 0.5 ? a : b, callLine(), SPEECH_SECONDS);
    this.cancel(bit);
  }

  /** Hands both agents back to plain wandering and frees the slot. */
  private cancel(bit: IdleBit): void {
    bit.a?.release();
    bit.b?.release();
    bit.reset();
  }
}

function rand(min: number, max: number): number {
  return min + Math.random() * (max - min);
}
