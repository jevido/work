import type { OfficeAgent } from "./agent";
import type { Speaker } from "./idle";
import { chairRect, waitSeat } from "./props";
import {
  answerLine,
  dispatchLine,
  handoffLine,
  phoneLine,
  receiptLine,
  returnLine,
  SPEECH_SECONDS,
} from "./speech";
import { isWalkable, prefersReducedMotion, type Rect } from "./world";

/**
 * How work physically moves through the office.
 *
 * The backend says an agent has been given a task; this is what makes that
 * look like somebody being given a task. Anton takes a folder off his own
 * desk, walks it over, hands it across and says so; the agent carries it to
 * their desk, works with it beside them, scribbles on it when they are done
 * and brings it back to his in-tray. While work is out he waits for it in a
 * chair pulled round to the end of his desk, facing the task board that hangs
 * over it.
 *
 * Nothing here is load-bearing, and that is the whole design. A handoff is a
 * plan; the agents' task states are the truth, and every phase below yields
 * the moment the two disagree -- if the backend says an agent is producing
 * output before the folder has reached them, they get up and start working and
 * the delivery follows them to their desk. The office never holds a monitor
 * dark waiting for a walk to finish.
 *
 * Nothing here costs a token and nothing here reaches the backend. It runs off
 * `agent:assigned` and `agent:finished`, which the office already receives, and
 * the phase those events already carry (see internal/workbench/events.go): a
 * folder is only ever carried for PhaseWork, because Anton's own planning and
 * synthesis turns are his own work and nobody brings you your own paperwork.
 */

/** Which part of a run a command belongs to. Mirrors workbench.Phase. */
export type TaskPhase = "plan" | "work" | "synthesis" | "chat";

/** Folders in flight at once. A plan with more steps than this phones the rest. */
const MAX_HANDOFFS = 6;

/**
 * How long a folder waits for Anton to be free before he rings instead.
 *
 * Crossing the bullpen takes the better part of ten seconds, so with a plan of
 * three this is what decides that one specialist is visited and the others are
 * phoned -- which is both what an office actually does and the only version of
 * this that does not have three people queueing at one desk.
 */
const PATIENCE = 6;

/** Longest a delivery or a return may take before it is written off. */
const TRAVEL_LIMIT = 26;

/** How long a folder changing hands takes. */
const EXCHANGE_SECONDS = 0.9;

/** How long a call lasts, which is also how long the handset is up. */
const PHONE_SECONDS = 1.8;

/**
 * Backstop on a folder sitting in somebody's hand.
 *
 * Long enough never to fire during a task. It is here because `carry` is the
 * only thing that can take an item off an agent without this file's say-so,
 * and a folder stranded by a sequence that lost track of itself should
 * eventually stop being drawn.
 */
const CARRY_LIMIT = 900;

/** How far away counts as standing next to somebody. */
const REACH = 54;

/** How far off to one side you stand to hand somebody something. */
const STAND_GAP = 34;

/** How long the chair takes to travel between its two homes. */
const SLIDE_SECONDS = 0.9;

/**
 * Folders the in-tray shows before it stops stacking them.
 *
 * Exported because the renderer sizes the patch it repaints from it: a stack
 * and the rectangle that erases it have to agree on how tall a full one is.
 */
export const TRAY_MAX = 4;

/** Retry gap after a walk that could not be planned. */
const ROUTE_RETRY = 0.5;

const PHASE_QUEUE = 0;
const PHASE_DELIVER = 1;
const PHASE_GIVE = 2;
const PHASE_PHONE = 3;
const PHASE_WORK = 4;
const PHASE_RETURN = 5;
const PHASE_GIVE_BACK = 6;

/**
 * One folder's journey, in a slot reused for the next one.
 *
 * Pooled for the same reason an idle bit is: these are read every frame, and a
 * task landing is a state transition -- allocating there would be the one
 * piece of garbage the office produces while it is actually working.
 */
class Handoff {
  receiver: OfficeAgent | null = null;
  phase = PHASE_QUEUE;
  /** Seconds left in the phase: patience, a travel watchdog, or an exchange. */
  timer = 0;

  get live(): boolean {
    return this.receiver !== null;
  }

  /** True while the folder is out and owed back. */
  get outstanding(): boolean {
    return this.phase === PHASE_WORK || this.phase === PHASE_RETURN || this.phase === PHASE_GIVE_BACK;
  }

  /** True while this folder is the one Anton has in his hands. */
  get owns(): boolean {
    return this.phase === PHASE_DELIVER || this.phase === PHASE_GIVE;
  }

  reset(): void {
    this.receiver = null;
    this.phase = PHASE_QUEUE;
    this.timer = 0;
  }
}

export class HandoffDirector {
  private readonly bits: readonly Handoff[] = Array.from(
    { length: MAX_HANDOFFS },
    () => new Handoff(),
  );

  private obstacles: readonly Rect[] = [];

  /** The coordinator, or null in an office that has none. */
  private boss: OfficeAgent | null = null;

  /** Where his chair waits, worked out once so the loop allocates nothing. */
  private waitX = 0;
  private waitY = 0;

  /** Folders on his in-tray, read by the renderer. */
  tray = 0;
  private drawnTray = 0;
  /** Whether he was mid-turn last frame, so the tray clears once per turn. */
  private wasWorking = false;

  /** Where the chair is now, in world units. Read by the renderer. */
  chairX = 0;
  chairY = 0;
  /** 0 at his seat, 1 at the waiting spot. Eased into the position above. */
  private slide = 0;
  private sliding = false;
  private drawnChairX = 0;
  private drawnChairY = 0;
  private drawnChair = false;

  /** Seconds before another attempt at a walk that could not be planned. */
  private retry = 0;

  /** Puts a line in an agent's bubble. Supplied by the renderer; see Speaker. */
  private readonly say: Speaker;

  constructor(say: Speaker) {
    this.say = say;
  }

  /** True while something in flight is worth the full frame rate. */
  get wantsSmoothFrames(): boolean {
    return this.sliding;
  }

  /** True when there is a coordinator, and so a chair to draw. */
  get showsChair(): boolean {
    return this.boss !== null;
  }

  /**
   * Points the director at a new cast. Everything in flight is dropped: the
   * agents it was steering no longer exist.
   */
  setScene(agents: readonly OfficeAgent[], obstacles: readonly Rect[]): void {
    for (const bit of this.bits) bit.reset();
    this.obstacles = obstacles;
    this.boss = agents.find((a) => a.boss) ?? null;
    this.tray = 0;
    this.wasWorking = false;
    this.slide = 0;
    this.sliding = false;
    this.retry = 0;

    const boss = this.boss;
    if (!boss) return;
    const spot = waitSeat(boss.deskX, boss.seatY);
    this.waitX = spot.x;
    this.waitY = spot.y;
    this.chairX = boss.seatX;
    this.chairY = boss.seatY;
  }

  /**
   * Takes on an assignment, standing the agent by instead of sending them
   * straight to their desk. False means "not mine": the caller walks them over
   * the way it always did.
   *
   * Refused for the coordinator himself, for anything that is not the work
   * phase, for an office with no coordinator, and when movement is unwelcome --
   * this is all decoration, and the state it decorates arrives either way.
   */
  claim(agent: OfficeAgent, phase: TaskPhase | undefined): boolean {
    if (prefersReducedMotion()) return false;
    const boss = this.boss;
    if (!boss || boss === agent) return false;
    if (phase !== "work") return false;

    // A re-run of the same task supersedes the folder that was out for it.
    for (const bit of this.bits) if (bit.receiver === agent) this.close(bit);

    const slot = this.bits.find((b) => !b.live);
    if (!slot) return false;

    slot.receiver = agent;
    slot.phase = PHASE_QUEUE;
    slot.timer = PATIENCE;
    agent.awaitHandoff();
    return true;
  }

  update(dt: number): void {
    const boss = this.boss;
    if (!boss) return;

    // Decoration, so a request for less movement switches it off rather than
    // toning it down, and takes what is already running with it. The tasks
    // themselves are unaffected: `claim` refuses, so every agent is walked to
    // their desk by the plain path instead.
    if (prefersReducedMotion()) {
      for (const bit of this.bits) if (bit.live) this.close(bit);
      this.tray = 0;
      this.slide = 0;
      this.sliding = false;
      this.chairX = boss.seatX;
      this.chairY = boss.seatY;
      return;
    }

    if (this.retry > 0) this.retry -= dt;
    for (const bit of this.bits) if (bit.live) this.advance(bit, dt);
    this.dispatch();
    this.seat(dt);

    // He reads them. Clearing on the frame his turn starts rather than while
    // it runs means anything handed back mid-synthesis stays on the tray to be
    // seen, instead of vanishing the instant it lands.
    const working = boss.state === "working";
    if (working && !this.wasWorking) this.tray = 0;
    this.wasWorking = working;
  }

  private advance(bit: Handoff, dt: number): void {
    const boss = this.boss!;
    const to = bit.receiver!;

    switch (bit.phase) {
      case PHASE_QUEUE: {
        bit.timer -= dt;
        // Waited long enough to be visited. He rings instead.
        if (bit.timer <= 0) this.ring(bit);
        return;
      }

      case PHASE_DELIVER: {
        bit.timer -= dt;
        if (bit.timer <= 0) {
          this.ring(bit);
          return;
        }
        // A turn of his own has taken him off the errand -- `assign` ends it,
        // and his own work wins. Checked before anything else here: he is
        // walking to his own desk now, and stopping him halfway to hand
        // somebody a folder would leave him standing in the room.
        if (boss.errand !== "walk") {
          this.ring(bit);
          return;
        }
        // Close enough to hand it over, wherever they ended up. The target
        // moves on purpose: an agent whose output started while Anton was
        // walking over is at their desk by now, and the folder follows them.
        if (this.near(boss, to)) {
          this.give(bit);
          return;
        }
        if (!boss.arrived) return;
        // Landed short, because they moved after the route was planned.
        if (!this.walkBeside(boss, to)) this.ring(bit);
        return;
      }

      case PHASE_GIVE: {
        bit.timer -= dt;
        // Pulled away mid-exchange by a turn of his own. The folder is out and
        // in reach, so it changes hands now rather than going back with him.
        if (bit.timer <= 0 || boss.errand === "none") this.handOver(bit);
        return;
      }

      case PHASE_PHONE: {
        bit.timer -= dt;
        if (bit.timer > 0) return;
        this.say(to, answerLine(), SPEECH_SECONDS);
        to.dismiss();
        // Still standing where the call reached them, so they set off now. If
        // their output already started they are on their way and this is not
        // their news any more.
        if (to.state === "idle" && to.intent === "desk") to.assign();
        this.close(bit);
        return;
      }

      case PHASE_WORK: {
        // The folder is the bit. Losing it -- to a superseding task, or to the
        // carry backstop -- ends the sequence.
        if (to.holding !== "files") {
          this.close(bit);
          return;
        }
        // Re-asserted every frame because every task transition clears it: a
        // folder still owed is a claim on the agent that outlives the turn
        // they were given it for, and without this the idle director would
        // talk them into a game of ping pong on the frame their task ended,
        // with Anton's paperwork still under their arm.
        to.reserve();
        // Working, walking to the desk, or holding the finished flourish. The
        // scribble on the folder happens in that flourish; the renderer draws
        // it off the state, so there is nothing to time here.
        if (to.state !== "idle") return;
        if (this.retry > 0) return;
        if (!this.walkToTray(to)) {
          this.retry = ROUTE_RETRY;
          return;
        }
        bit.phase = PHASE_RETURN;
        bit.timer = TRAVEL_LIMIT;
        return;
      }

      case PHASE_RETURN: {
        if (to.holding !== "files") {
          this.close(bit);
          return;
        }
        bit.timer -= dt;
        // Gave up on the walk, or a task landed and took them off it. Either
        // way the folder is Anton's paperwork rather than theirs, so it goes
        // on his tray from wherever they got to.
        if (bit.timer <= 0 || to.errand !== "walk") {
          this.file(bit);
          return;
        }
        if (!to.arrived) return;
        to.hold();
        to.face(boss.x);
        if (this.near(to, boss)) boss.face(to.x);
        this.say(to, returnLine(), SPEECH_SECONDS);
        bit.phase = PHASE_GIVE_BACK;
        bit.timer = EXCHANGE_SECONDS;
        return;
      }

      case PHASE_GIVE_BACK: {
        bit.timer -= dt;
        if (bit.timer > 0) return;
        // Only worth a word if he is actually sitting there to say it.
        if (this.near(to, boss)) this.say(boss, receiptLine(), SPEECH_SECONDS);
        this.file(bit);
        return;
      }
    }
  }

  /**
   * Sends Anton off with the next folder, if he has hands free.
   *
   * One delivery at a time: he cannot be in two places, and a queue that tried
   * would leave him oscillating between two desks delivering nothing. Whoever
   * has the least patience left goes first, so the folder about to give up and
   * become a phone call is the one that gets visited.
   */
  private dispatch(): void {
    const boss = this.boss!;
    if (!this.bossFree()) return;
    for (const bit of this.bits) if (bit.live && bit.owns) return;

    let next: Handoff | null = null;
    for (const bit of this.bits) {
      if (!bit.live || bit.phase !== PHASE_QUEUE) continue;
      if (!next || bit.timer < next.timer) next = bit;
    }
    if (!next) return;
    if (this.retry > 0) return;
    if (!this.walkBeside(boss, next.receiver!)) {
      this.retry = ROUTE_RETRY;
      return;
    }

    boss.carry("files", CARRY_LIMIT);
    next.phase = PHASE_DELIVER;
    next.timer = TRAVEL_LIMIT;
  }

  /** Stood next to them with the folder out. */
  private give(bit: Handoff): void {
    const boss = this.boss!;
    const to = bit.receiver!;
    boss.hold();
    boss.face(to.x);
    to.face(boss.x);
    this.say(boss, handoffLine(), SPEECH_SECONDS);
    bit.phase = PHASE_GIVE;
    bit.timer = EXCHANGE_SECONDS;
  }

  /** The folder changes hands, and Anton is free for the next one. */
  private handOver(bit: Handoff): void {
    const boss = this.boss!;
    const to = bit.receiver!;

    boss.putDown();
    to.carry("files", CARRY_LIMIT);
    this.say(to, dispatchLine(), SPEECH_SECONDS);
    // Waiting for the folder was the only thing keeping them off their feet.
    // Anyone already walking or working is left alone: they did not wait, and
    // restarting them would throw away a monitor that is already filling.
    if (to.errand !== "none" || to.state === "idle") to.assign();
    boss.dismiss();

    bit.phase = PHASE_WORK;
    bit.timer = 0;
  }

  /**
   * Rings them instead of walking over.
   *
   * The bit ends here: a call leaves nothing in anybody's hands, so there is
   * nothing to bring back and no reason for Anton to sit and wait for it.
   */
  private ring(bit: Handoff): void {
    const boss = this.boss!;
    const to = bit.receiver!;

    if (bit.owns) {
      // Giving up the walk, and the folder with it.
      boss.putDown();
      boss.dismiss();
    }
    this.say(boss, phoneLine(), SPEECH_SECONDS);
    to.carry("phone", PHONE_SECONDS);
    bit.phase = PHASE_PHONE;
    bit.timer = PHONE_SECONDS;
  }

  /** The folder lands on the in-tray and the sequence is over. */
  private file(bit: Handoff): void {
    const to = bit.receiver!;
    if (to.holding === "files" && this.tray < TRAY_MAX) this.tray++;
    this.close(bit);
  }

  /**
   * Ends a sequence however it got here, and takes back anything it handed
   * out. This is the only place a folder disappears without being filed, which
   * is what stops one being stranded in a hand by a bit that lost its way.
   */
  private close(bit: Handoff): void {
    const to = bit.receiver;
    if (to) {
      if (to.holding === "files" || to.holding === "phone") to.putDown();
      to.dismiss();
    }
    bit.reset();
  }

  /**
   * Puts Anton in his chair while work is out, and moves the chair with him.
   *
   * He waits at the end of his own desk facing back along it, which is where
   * the task board hangs. That is the point of moving: from his working seat
   * he has his back to the board, and a coordinator waiting on other people's
   * work should be looking at the board that says whose.
   */
  private seat(dt: number): void {
    const boss = this.boss!;
    let owed = false;
    let busy = false;
    for (const bit of this.bits) {
      if (!bit.live) continue;
      if (bit.outstanding) owed = true;
      if (bit.owns || bit.phase === PHASE_QUEUE) busy = true;
    }

    const waiting = owed && !busy && this.bossFree();

    if (!waiting) {
      // A task of his own, or nothing left to wait for. `assign` has already
      // stood him up in the first case; the second is this one.
      if (boss.sitting && boss.errand !== "none") boss.dismiss();
    } else if (boss.sitting) {
      // Already there. Keep him looking at the board.
      boss.face(boss.deskX);
    } else if (boss.errand === "walk" && boss.arrived) {
      if (this.atWaitSeat(boss)) {
        boss.hold();
        boss.sitting = true;
        boss.face(boss.deskX);
      } else {
        // Landed somewhere that is not the chair, because something redirected
        // him on the way. Hand him back and let the next frame set off again.
        boss.dismiss();
      }
    } else if (boss.errand === "none" && this.retry <= 0) {
      if (!boss.walk(this.waitX, this.waitY)) this.retry = ROUTE_RETRY;
    }

    // Driven by the decision to wait rather than by him being sat down: you
    // push the chair round and then sit in it. Keyed on `sitting` the chair set
    // off only once he had landed, which drew him seated in mid-air for most of
    // a second while it caught up. The walk over is the longer of the two, so
    // this way the chair is always there before he is.
    this.moveChair(dt, waiting);
  }

  /** True when the coordinator has nothing of his own on. */
  private bossFree(): boolean {
    const boss = this.boss!;
    if (boss.activity !== "none") return false;
    return boss.state === "idle" || (boss.state === "walking" && boss.intent === "wander");
  }

  private atWaitSeat(boss: OfficeAgent): boolean {
    const dx = boss.x - this.waitX;
    const dy = boss.y - this.waitY;
    return dx * dx + dy * dy <= 12 * 12;
  }

  /**
   * Slides the chair between his working seat and the waiting spot.
   *
   * Eased, and over most of a second, because the chair is the cue: it is what
   * says he has settled in to wait rather than simply wandered off, and a
   * chair that teleported would say nothing at all.
   */
  private moveChair(dt: number, out: boolean): void {
    const boss = this.boss!;
    const target = out ? 1 : 0;
    this.sliding = this.slide !== target;
    if (this.sliding) {
      const step = dt / SLIDE_SECONDS;
      this.slide =
        target > this.slide ? Math.min(target, this.slide + step) : Math.max(target, this.slide - step);
    }
    // Smoothstep: a chair pushed round a desk starts and stops, it does not
    // travel at a constant rate.
    const t = this.slide * this.slide * (3 - 2 * this.slide);
    this.chairX = boss.seatX + (this.waitX - boss.seatX) * t;
    this.chairY = boss.seatY + (this.waitY - boss.seatY) * t;
  }

  /** Offsets to try when standing next to somebody. Reused, never grown. */
  private readonly sides: number[] = [0, 0, 0, 0];

  /**
   * Walks somebody to a spot beside somebody else, nearest side first.
   *
   * False when nothing around them is both walkable and reachable, which is
   * the signal to do the thing that needs no walk: to ring them, or to file
   * the folder from where it is.
   */
  private walkBeside(who: OfficeAgent, beside: OfficeAgent): boolean {
    const near = who.x <= beside.x ? -1 : 1;
    this.sides[0] = near * STAND_GAP;
    this.sides[1] = -near * STAND_GAP;
    this.sides[2] = near * (STAND_GAP + 24);
    this.sides[3] = -near * (STAND_GAP + 24);

    for (let i = 0; i < this.sides.length; i++) {
      const x = beside.x + this.sides[i];
      if (!isWalkable(this.obstacles, x, beside.y)) continue;
      if (who.walk(x, beside.y)) return true;
    }
    return false;
  }

  /**
   * Walks a finished folder to Anton's in-tray: the spot just past the chair
   * he waits in, so the handover happens where he is sitting.
   *
   * Falls back to walking up to him wherever he actually is, which is what
   * happens when his own turn has already started and he is back at his
   * working seat.
   */
  private walkToTray(to: OfficeAgent): boolean {
    const boss = this.boss!;
    const x = this.waitX + STAND_GAP;
    if (isWalkable(this.obstacles, x, this.waitY) && to.walk(x, this.waitY)) return true;
    return this.walkBeside(to, boss);
  }

  private near(a: OfficeAgent, b: OfficeAgent): boolean {
    const dx = a.x - b.x;
    const dy = a.y - b.y;
    return dx * dx + dy * dy <= REACH * REACH;
  }

  /** True while the chair, or the last of it, still owes the screen a repaint. */
  get chairIsDirty(): boolean {
    if (!this.showsChair) return this.drawnChair;
    if (!this.drawnChair) return true;
    return this.chairX !== this.drawnChairX || this.chairY !== this.drawnChairY;
  }

  /** True when the in-tray has gained or lost a folder since it was drawn. */
  get trayIsDirty(): boolean {
    return this.tray !== this.drawnTray;
  }

  /**
   * The chair's travel since it was last drawn.
   *
   * Both ends, like the ball's: it crosses most of the desk's width in under a
   * second, so the patch to repaint is the run it just made rather than the
   * seat at the end of it.
   */
  chairSweep(out: Rect): Rect {
    const pad = 6;
    const fromX = this.drawnChair ? this.drawnChairX : this.chairX;
    const fromY = this.drawnChair ? this.drawnChairY : this.chairY;
    chairRect(Math.min(fromX, this.chairX), Math.min(fromY, this.chairY), out);
    out.x -= pad;
    out.y -= pad;
    out.w += Math.abs(this.chairX - fromX) + pad * 2;
    out.h += Math.abs(this.chairY - fromY) + pad * 2;
    return out;
  }

  /** Records the chair and the tray as painted. Called once a frame. */
  markDrawn(): void {
    this.drawnChair = this.showsChair;
    this.drawnChairX = this.chairX;
    this.drawnChairY = this.chairY;
    this.drawnTray = this.tray;
  }
}
