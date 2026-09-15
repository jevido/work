/**
 * Three conversations in one window, and one set of subscriptions between them.
 *
 * A discussion about what an idea should be and the output of a run against a
 * repository are different conversations. Held in one transcript they interleave
 * and share a running total, so the cost of thinking reads as the cost of
 * working.
 *
 * What is *not* allowed to change is the console. Scroll position, focus and a
 * half-typed message survive a mode switch today only because nothing is torn
 * down — `ClaudeConsole` lives outside the mode stack in App.svelte for exactly
 * that reason. Passing a different session object into the same component is
 * not a remount; creating the component inside a `{#key}` is, and would undo
 * all of it in one line.
 */
import { Events } from "@wailsio/runtime";

import {
  CHAT_FINISHED,
  CHAT_STARTED,
  CLAUDE_CANCELLED,
  CLAUDE_ERROR,
  CLAUDE_RESULT,
  CLAUDE_SESSION,
  CLAUDE_TEXT,
  CLAUDE_THINKING,
  CLAUDE_TOOL,
  CLAUDE_TOOL_RESULT,
  RUN_FINISHED,
  RUN_PLAN,
  RUN_STARTED,
} from "../bridge/events";
import type { Mode } from "../workspace/model";
import { ClaudeSession } from "./session.svelte";

/** Every event a transcript is built from. */
const STREAM = [
  RUN_STARTED,
  RUN_PLAN,
  RUN_FINISHED,
  CLAUDE_SESSION,
  CLAUDE_TEXT,
  CLAUDE_THINKING,
  CLAUDE_TOOL,
  CLAUDE_TOOL_RESULT,
  CLAUDE_RESULT,
  CLAUDE_ERROR,
  CLAUDE_CANCELLED,
  CHAT_STARTED,
  CHAT_FINISHED,
];

/** Where a piece of work is owned from the moment it starts. */
const STARTS = new Set([RUN_STARTED, CHAT_STARTED]);
const ENDS = new Set([RUN_FINISHED, CHAT_FINISHED]);

export class Conversations {
  #sessions: Record<Mode, ClaudeSession> = {
    idea: named("idea"),
    planning: named("planning"),
    work: named("work"),
  };

  /**
   * Which mode each run belongs to, from the moment it started.
   *
   * Decided where the work is raised rather than by whatever mode happens to be
   * on screen when an event arrives. A restructuring asked for in planning
   * answers into planning's transcript even if somebody has switched to work by
   * the time it does -- which is the ordinary case, because a run takes a while
   * and people do not sit and watch it.
   */
  #owner = new Map<string, Mode>();

  /** The mode work started right now belongs to. Supplied by App. */
  current: () => Mode = () => "work";

  /** The transcript for a mode. The same object every time, so nothing remounts. */
  for(mode: Mode): ClaudeSession {
    return this.#sessions[mode];
  }

  /** Every transcript, for the things that are about all of them. */
  all(): ClaudeSession[] {
    return [this.#sessions.idea, this.#sessions.planning, this.#sessions.work];
  }

  /**
   * Subscribes once per event and hands each to the session that owns it.
   *
   * Once, not three times. Three sessions each subscribing to `claude:text`
   * would put every run in all three transcripts, which is the thing this
   * exists to stop.
   */
  listen(): () => void {
    const offs = STREAM.map((name) =>
      Events.On(name, (event: any) => this.#deliver(name, event)),
    );
    return () => {
      for (const off of offs) off();
      for (const session of this.all()) session.stop();
    };
  }

  /**
   * Clears every transcript, because the backend's half of it is the window's.
   *
   * ClearConversation forgets every agent's session and empties the board.
   * Making that per-mode would mean the board clearing or not depending on
   * which mode somebody was looking at, which is a worse rule than "clear
   * clears everything" -- so all three go, and the button says so.
   */
  async clear(): Promise<void> {
    for (const session of this.all()) session.forget();
    await this.#sessions.work.clear();
  }

  setAgents(identities: Parameters<ClaudeSession["setAgents"]>[0]): void {
    for (const session of this.all()) session.setAgents(identities);
  }

  #deliver(name: string, event: { data?: any }): void {
    const id = event?.data?.runId ?? "";

    if (STARTS.has(name) && id) this.#owner.set(id, this.current());

    // An event with no run id -- a partial message, a thinking marker -- belongs
    // to whatever is running. When two things are running in different modes,
    // the run is the one with an id on the events around it; the ones without
    // go to the mode that started the most recent work, which is where somebody
    // watching would expect to see them.
    const mode: Mode = (id && this.#owner.get(id)) || this.#lastStarted || this.current();
    if (STARTS.has(name)) this.#lastStarted = mode;

    this.#sessions[mode].deliver(name, event as { data: any });

    // Forgotten when it ends, so the map does not grow for the life of the
    // window. The finishing event is delivered first -- it is the one that
    // closes the turn.
    if (ENDS.has(name) && id) this.#owner.delete(id);
  }

  #lastStarted: Mode | null = null;
}

/** A session that knows which conversation it is, so the backend can too. */
function named(mode: Mode): ClaudeSession {
  const session = new ClaudeSession();
  session.mode = mode;
  return session;
}
