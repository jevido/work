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
  /**
   * Every transcript, by tab and then by mode.
   *
   * Nested rather than keyed on `${tab}:${mode}`, for the reason Go's own
   * sessionKey gives: a tab id is a node id and a separator is a legal
   * character in one, so a flat key is a key that can be collided into by
   * naming a tab carefully.
   *
   * A plain Map, not $state. It is an identity cache whose whole job is to
   * hand back the same object every time, so that switching tabs swaps what
   * the console reads rather than remounting it. Nothing renders from the map
   * itself.
   */
  #sessions = new Map<string, Record<Mode, ClaudeSession>>();

  /**
   * Which transcript each run belongs to, from the moment it started.
   *
   * The session object rather than a key, which is what lets a run outlive its
   * tab: a stream whose tab was closed mid-run still has somewhere to land,
   * because the map holds the transcript rather than a name to look one up by.
   *
   * Decided where the work is raised rather than by whatever is on screen when
   * an event arrives. A restructuring asked for in planning answers into
   * planning's transcript even if somebody has switched to work by the time it
   * does -- which is the ordinary case, because a run takes a while and people
   * do not sit and watch it.
   */
  #owner = new Map<string, ClaudeSession>();

  /** The transcript work started right now belongs to. Supplied by App. */
  current: () => ClaudeSession = () => this.for("", "work");

  /**
   * The transcript for one tab in one mode. The same object every time.
   *
   * Minted on read. Called from a $derived in App, which is safe precisely
   * because the map is not reactive: creating an entry here does not
   * invalidate anything, so the derived does not re-run on its own result.
   */
  for(tab: string, mode: Mode): ClaudeSession {
    let byMode = this.#sessions.get(tab);
    if (!byMode) {
      byMode = {
        idea: named(tab, "idea"),
        planning: named(tab, "planning"),
        work: named(tab, "work"),
      };
      this.#sessions.set(tab, byMode);
    }
    return byMode[mode];
  }

  /** Every transcript, for the things that are about all of them. */
  all(): ClaudeSession[] {
    const out: ClaudeSession[] = [];
    for (const byMode of this.#sessions.values()) {
      out.push(byMode.idea, byMode.planning, byMode.work);
    }
    return out;
  }

  /**
   * Drops a closed tab's transcripts.
   *
   * Refuses while anything in them is live, and that is the point of the
   * method rather than an afterthought: a session dropped mid-run takes the
   * Cancel button, the transcript and the final cost line with it while the
   * run carries on writing files. The map keeps it until its lane ends, and
   * `#owner` still holds it by reference either way, so the stream lands.
   */
  forget(tab: string): void {
    const byMode = this.#sessions.get(tab);
    if (!byMode) return;
    for (const session of [byMode.idea, byMode.planning, byMode.work]) {
      if (session.busy || session.chatBusy) return;
    }
    for (const session of [byMode.idea, byMode.planning, byMode.work]) session.stop();
    this.#sessions.delete(tab);
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
   * Clears one transcript: the one the person is looking at.
   *
   * It used to clear all three and the board with them, because the backend's
   * half of it was the window's. It is not any more -- ClearConversation takes
   * the conversation it is about -- so this is the whole of the act, and the
   * button can honestly say it affects nothing else.
   */
  async clear(session: ClaudeSession): Promise<void> {
    await session.clear();
  }

  setAgents(identities: Parameters<ClaudeSession["setAgents"]>[0]): void {
    for (const session of this.all()) session.setAgents(identities);
  }

  #deliver(name: string, event: { data?: any }): void {
    const id = event?.data?.runId ?? "";

    if (STARTS.has(name) && id) this.#owner.set(id, this.current());

    // An event with no run id -- a partial message, a thinking marker -- belongs
    // to whatever is running. When two things are running at once, the run is
    // the one with an id on the events around it; the ones without go to the
    // transcript that started the most recent work, which is where somebody
    // watching would expect to see them.
    //
    // That fallback was a reasonable bet among three transcripts and is a coin
    // flip among nine, so it is reached as rarely as possible: see #finished,
    // which keeps a run's owner findable for a while after it ends rather than
    // dropping it the instant the last event arrives.
    const session =
      (id && (this.#owner.get(id) ?? this.#finished.get(id))) ||
      this.#lastStarted ||
      this.current();
    if (STARTS.has(name)) this.#lastStarted = session;

    session.deliver(name, event as { data: any });

    // Moved out of the live map when it ends rather than forgotten, so a
    // trailing event -- a late claude:error after run:finished -- still lands
    // where it belongs instead of being guessed at. Bounded, so neither map
    // grows for the life of the window. The finishing event is delivered
    // first: it is the one that closes the turn.
    if (ENDS.has(name) && id) {
      this.#owner.delete(id);
      this.#finished.set(id, session);
      if (this.#finished.size > RECENT_RUNS) {
        const oldest = this.#finished.keys().next();
        if (!oldest.done) this.#finished.delete(oldest.value);
      }
    }
  }

  #lastStarted: ClaudeSession | null = null;
  /** Runs that have ended, so their stragglers are not guessed at. */
  #finished = new Map<string, ClaudeSession>();
}

/**
 * How many finished runs stay addressable.
 *
 * Small on purpose. It exists for the event that arrives a moment after
 * run:finished, not as a history: anything older than the last handful of runs
 * is not going to produce another event, and a map that remembered every run
 * would grow for the life of the window to serve a case that never happens.
 */
const RECENT_RUNS = 8;

/** A session that knows which conversation it is, so the backend can too. */
function named(tab: string, mode: Mode): ClaudeSession {
  const session = new ClaudeSession();
  session.tab = tab;
  session.mode = mode;
  return session;
}
