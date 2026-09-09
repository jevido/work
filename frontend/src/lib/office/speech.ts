import type { Rect } from "./world";

/**
 * The things agents say out loud, and the bubble that shows one.
 *
 * Every line in this file is written here, by hand. Nothing on this path ever
 * reaches a model: an office full of small talk that cost tokens would be an
 * office nobody could afford to leave running, so the chatter is canned and
 * the only thing chosen at runtime is which line it is.
 */

/** Bubble text size, in world units, matching the nameplates' weight. */
export const SPEECH_TEXT_SIZE = 11;
export const SPEECH_FONT = `600 ${SPEECH_TEXT_SIZE}px Inter, system-ui, sans-serif`;

/** Padding either side of the text, and the bubble's fixed height. */
export const SPEECH_PAD_X = 7;
export const SPEECH_HEIGHT = 19;

/** Gap between the agent's origin and the bottom of the bubble. */
export const SPEECH_RISE = 30;

/** How long a spoken line stays up, in seconds. */
export const SPEECH_SECONDS = 2.4;

/**
 * How long a dispatched agent stands and answers before sitting down.
 *
 * Shorter than the bubble's life on purpose: the line stays readable while
 * they cross the room and the monitor comes on under it. This is only a floor,
 * and a walk of any length has already paid it.
 */
export const DISPATCH_HOLD_SECONDS = 1.2;

/**
 * A line an agent is saying, and how much room it needs.
 *
 * The width is stored rather than measured while drawing, because measuring
 * text is the one thing the render loop is not allowed to do -- and because
 * the frame a bubble disappears has to erase the bubble that was there, which
 * means the last width has to outlive the line.
 */
export class SpeechBubble {
  /** What is being said, or "" for nothing. */
  text = "";

  /** Bubble width in world units, measured once when the line was set. */
  width = 0;

  private timer = 0;

  get active(): boolean {
    return this.text.length > 0;
  }

  /** Says a line. `width` comes from the renderer, the only place that can measure. */
  say(text: string, width: number, seconds: number): void {
    this.text = text;
    this.width = width;
    this.timer = seconds;
  }

  update(dt: number): void {
    if (!this.text) return;
    this.timer -= dt;
    if (this.timer <= 0) this.clear();
  }

  clear(): void {
    this.text = "";
    this.width = 0;
    this.timer = 0;
  }
}

/** The bubble's rectangle: centred over the agent, sitting above their head. */
export function speechRect(x: number, y: number, width: number, out: Rect): Rect {
  out.x = x - width / 2;
  out.y = y - SPEECH_RISE - SPEECH_HEIGHT;
  out.w = width;
  out.h = SPEECH_HEIGHT + 5; // The tail hangs below the box.
  return out;
}

/**
 * A set of lines that avoids saying the same one twice running.
 *
 * With half a dozen entries plain random repeats often enough to read as a
 * stuck loop rather than a coincidence, which is the whole illusion gone for
 * the sake of two fields.
 */
class LinePool {
  private readonly lines: readonly string[];
  private last = -1;

  constructor(lines: readonly string[]) {
    this.lines = lines;
  }

  pick(): string {
    if (this.lines.length === 1) return this.lines[0];
    let i = this.last;
    while (i === this.last) i = Math.floor(Math.random() * this.lines.length);
    this.last = i;
    return this.lines[i];
  }
}

/** Answering Anton, the moment a task lands. */
const dispatch = new LinePool([
  "On it.",
  "Got it.",
  "Mine.",
  "On my way.",
  "Taking a look.",
  "Right, my turn.",
  "Consider it done.",
]);

/** Opening a conversation with whoever is standing nearby. */
const openers = new LinePool([
  "Coffee?",
  "Standup at ten?",
  "Seen the board?",
  "How's your branch?",
  "Did the build pass?",
  "Busy morning.",
  "Any word from Anton?",
]);

/** Answering one. Deliberately non-committal: any reply has to fit any opener. */
const replies = new LinePool([
  "Barely.",
  "Don't ask.",
  "Same here.",
  "Two more tasks.",
  "Ha. Right.",
  "Later, maybe.",
  "Not a chance.",
  "Tell me about it.",
]);

const coffee = new LinePool(["Coffee run.", "Refill.", "Need this.", "Back in a minute."]);

/** Starting a rally, which has to sound like a start. */
const serves = new LinePool(["Serve.", "Ready?", "My serve.", "First to eleven."]);

/** Calling one, once there is something to call. */
const calls = new LinePool(["Point.", "11-9.", "Rematch?", "Off the edge.", "Lucky.", "Out."]);

export function dispatchLine(): string {
  return dispatch.pick();
}

export function openerLine(): string {
  return openers.pick();
}

export function replyLine(): string {
  return replies.pick();
}

export function coffeeLine(): string {
  return coffee.pick();
}

export function serveLine(): string {
  return serves.pick();
}

export function callLine(): string {
  return calls.pick();
}
