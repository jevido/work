import { Events } from "@wailsio/runtime";
import { untrack } from "svelte";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import { describeTool, type ToolCall } from "../diff/tools";
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

/** Where the current run is. Drives the console's status line. */
export type RunStatus =
  | "idle"
  | "planning"
  | "working"
  | "done"
  | "cancelled"
  | "error";

export type Phase = "plan" | "work" | "synthesis" | "chat";

export type TurnStatus = "streaming" | "done" | "error" | "cancelled";

/** Narrows the backend's phase string, defaulting to ordinary work. */
function phaseOf(phase: string | undefined): Phase {
  if (phase === "synthesis" || phase === "chat" || phase === "plan") return phase;
  return "work";
}

/** A run of prose inside a turn. */
export interface TextPart {
  kind: "text";
  id: string;
  text: string;
}

/** A tool call inside a turn, with its arguments, result and any diff. */
export interface ToolPart {
  kind: "tool";
  id: string;
  call: ToolCall;
}

/**
 * A turn's output in order. Prose and tool calls interleave as they happened,
 * because "he read this file, then said that" is the part worth seeing.
 */
export type TurnPart = TextPart | ToolPart;

/** Something you said. */
export interface UserEntry {
  kind: "user";
  id: string;
  text: string;
  /**
   * The agent it was addressed to, or "" for the team -- which is to say
   * Anton, who decides who picks it up. Set when you talk to one agent
   * directly, so that conversation can be read back on its own.
   */
  agentId: string;
}

/** One edit Anton made to the board while routing. */
export interface PlanUpdate {
  taskId: string;
  status: string;
  title: string;
  agentName: string;
}

/** Anton's routing decision, in his own voice. */
export interface PlanEntry {
  kind: "plan";
  id: string;
  /** Whoever did the routing, kept so the entry can be relabelled if they are
      renamed from their desk. */
  agentId: string;
  agentName: string;
  colour: string;
  mode: "self" | "team";
  reason: string;
  steps: {
    agentId: string;
    agentName: string;
    colour: string;
    task: string;
    taskId: string;
  }[];
  updates: PlanUpdate[];
}

/** One agent's turn. */
export interface AgentEntry {
  kind: "agent";
  id: string;
  agentId: string;
  agentName: string;
  colour: string;
  phase: Phase;
  parts: TurnPart[];
  status: TurnStatus;
  error: string | null;
  model: string;
  costUsd: number;
  durationMs: number;
}

/** A line from Work itself: what a run cost, why it stopped. */
export interface NoticeEntry {
  kind: "notice";
  id: string;
  text: string;
  tone: "info" | "error";
}

export type Entry = UserEntry | PlanEntry | AgentEntry | NoticeEntry;

/** The minimum the console needs to label an agent. */
export interface AgentIdentity {
  id: string;
  name: string;
  colour: string;
}

/**
 * The state behind the Claude console.
 *
 * The console is a conversation: your requests and every agent's reply stay on
 * screen, in order, until you clear it. Entries are appended, never replaced,
 * and a run adds several of them because several agents can answer one request.
 *
 * Text arrives one token at a time, which is far too often to drive Svelte
 * directly, so chunks accumulate per turn and flush once per animation frame: a
 * fast stream costs one update per frame instead of one per token.
 */
export class ClaudeSession {
  entries = $state<Entry[]>([]);
  status = $state<RunStatus>("idle");
  runId = $state<string | null>(null);

  /**
   * The coordinator's side channel, tracked apart from the run.
   *
   * A question asked while specialists are working is answered concurrently,
   * so it needs its own status: sharing the run's would make the composer
   * think the work had finished the moment an answer came back.
   */
  chatStatus = $state<RunStatus>("idle");
  chatId = $state<string | null>(null);

  /** True while the run can still be cancelled. */
  busy = $derived(this.status === "planning" || this.status === "working");

  /** True while the coordinator is answering a question on the side. */
  chatBusy = $derived(
    this.chatStatus === "planning" || this.chatStatus === "working",
  );

  /**
   * What is half typed into the composer for this conversation.
   *
   * On the session rather than in the component, because the component does not
   * remount when the mode changes -- that is the point of where it sits in
   * App.svelte -- so a draft held locally would follow you from one
   * conversation into another and appear in the one it was not meant for.
   */
  draft = $state("");

  /**
   * Which conversation this is, for the backend to keep its own memory of it.
   *
   * Both halves, because both are things a person switches between. A session
   * that does not know its mode resumes the wrong one; a session that does not
   * know its tab is shared by every tab in that mode, so two projects open side
   * by side answer each other's questions out of one history. Either way the
   * transcripts read as separate while the model continues a single one, which
   * looks fixed and is not.
   *
   * Plain fields rather than $state: they are set once when the session is
   * minted and never change for its life. See Conversations.for.
   */
  mode = "work";
  tab = "";

  /** The pair, as the backend spells it. */
  get conversation(): { tabId: string; mode: string } {
    return { tabId: this.tab, mode: this.mode };
  }

  /** True when there is nothing on screen to clear. */
  isEmpty = $derived(this.entries.length === 0);

  /** What the run in progress has cost, planning turn included. */
  private runCostUsd = 0;
  /** What the side channel has cost, kept out of the run's total. */
  private chatCostUsd = 0;

  /**
   * What this conversation has cost since it was last cleared.
   *
   * A third number next to two that look like it, and the distinction is worth
   * stating: the two above answer "what did *this* run cost" and are reset at
   * the start of every one, which is what the per-run notice line reports. This
   * one only ever resets when the conversation does. Without it there is no
   * answer at all to "what has this transcript cost me", which is the number
   * somebody with nine of them open actually wants.
   */
  totalCostUsd = $state(0);
  private nextId = 0;

  /** Agent identities, for labelling turns and plan steps. */
  private roster = new Map<string, AgentIdentity>();

  /**
   * The turns, by the task ID they were opened under.
   *
   * Every streamed token has to find the turn it belongs to, and a stream is
   * the one thing here that arrives thousands of times. Searching `entries`
   * for it made that search longer with every turn already on screen, so a
   * long conversation streamed slower than a fresh one for no reason anybody
   * could see. Written only where a turn is created or the console is
   * cleared, which is the whole of its life.
   */
  private turns = new Map<string, AgentEntry>();

  /** Tool calls by the tool-use ID the backend gave them, for the same reason. */
  private tools = new Map<string, ToolPart>();

  /** Text waiting to be flushed, by entry ID. */
  private pending = new Map<string, string>();
  private flushHandle = 0;

  /** Tells the console who the agents are. Safe to call again on a change. */
  setAgents(list: readonly AgentIdentity[]): void {
    this.roster = new Map(list.map((a) => [a.id, a]));

    // An agent renamed from their desk is still the same agent, so the turns
    // they have already taken are relabelled too. Leaving them alone would
    // read as two people having answered one question.
    //
    // Untracked because this is called from an effect that watches the roster.
    // Reading the transcript here made that effect watch the transcript too,
    // so every turn the run added re-ran the whole relabelling walk -- work
    // proportional to the conversation, done on a roster that had not changed.
    untrack(() => {
      for (const entry of this.entries) {
        if (entry.kind === "agent") {
          entry.agentName = this.identify(entry.agentId).name;
        } else if (entry.kind === "plan") {
          entry.agentName = this.identify(entry.agentId).name;
          for (const step of entry.steps) {
            step.agentName = this.identify(step.agentId).name;
          }
        }
      }
    });
  }

  /** An agent's display name, falling back to their ID if they are unknown. */
  nameOf(agentId: string): string {
    return this.identify(agentId).name;
  }

  /**
   * Subscribes to backend events. Returns an unsubscribe function.
   *
   * Kept for a session that is the only one there is. Where several sessions
   * share one window -- see Conversations -- the events are subscribed once and
   * handed to the right session with [ClaudeSession.deliver], because three
   * subscriptions to one event is three transcripts holding the same run.
   */
  listen(): () => void {
    const offs = this.handlers().map(([name, handle]) => Events.On(name, handle));
    return () => {
      for (const off of offs) off();
      this.cancelFlush();
    };
  }

  /** Hands one event to this session, by name. */
  deliver(name: string, event: { data: any }): void {
    this.#delivery ??= new Map(this.handlers());
    this.#delivery.get(name)?.(event);
  }

  /** Stops the flush timer for a session that is handed events rather than
   * subscribing to them. */
  stop(): void {
    this.cancelFlush();
  }

  #delivery: Map<string, (e: any) => void> | null = null;

  /**
   * Every event this session knows, paired with what it does about it.
   *
   * A table rather than thirteen subscriptions, so the same handlers can be
   * reached two ways: subscribed directly, or dispatched to by whoever owns the
   * window's subscriptions.
   */
  private handlers(): [string, (e: any) => void][] {
    const on = (name: string, handle: (e: any) => void): [string, (e: any) => void] => [name, handle];
    return [
      on(RUN_STARTED, (e) => {
        this.runId = e.data.runId;
        this.runCostUsd = 0;
        this.status = "planning";
      }),

      on(RUN_PLAN, (e) => {
        const who = this.identify(e.data.agentId ?? "");
        this.entries.push({
          kind: "plan",
          id: this.mintId("plan"),
          agentId: who.id,
          agentName: who.name,
          colour: who.colour,
          mode: e.data.mode === "team" ? "team" : "self",
          reason: e.data.reason ?? "",
          steps: (e.data.steps ?? []).map((s: any) => {
            const agent = this.identify(s.agentId);
            return {
              agentId: s.agentId,
              agentName: agent.name,
              colour: agent.colour,
              task: s.task,
              taskId: s.taskId ?? "",
            };
          }),
          updates: (e.data.updates ?? []).map((u: any) => ({
            taskId: u.taskId,
            status: u.status ?? "",
            title: u.title ?? "",
            agentName: u.agentId ? this.identify(u.agentId).name : "",
          })),
        });
        this.status = "working";
      }),

      on(RUN_FINISHED, (e) => {
        this.flush();
        if (e.data.cancelled) {
          this.closeOpenTurns("cancelled", "run");
          this.notice("Stopped.", "info");
          this.status = "cancelled";
        } else if (e.data.message) {
          this.closeOpenTurns("error", "run");
          this.notice(e.data.message, "error");
          this.status = "error";
        } else {
          this.closeOpenTurns("done", "run");
          if (this.runCostUsd > 0) {
            this.notice(`Run cost $${this.runCostUsd.toFixed(4)}`, "info");
          }
          this.status = "done";
        }
        this.runId = null;
      }),

      on(CLAUDE_SESSION, (e) => {
        const turn = this.turnFor(e.data);
        if (turn) turn.model = e.data.model ?? "";
      }),

      on(CLAUDE_TEXT, (e) => this.appendText(e.data, e.data.text ?? "")),

      on(CLAUDE_THINKING, () => {
        // Thinking is streamed but not shown yet: a dedicated, collapsible
        // panel is the right home for it, not the conversation.
      }),

      on(CLAUDE_TOOL, (e) => {
        const name = e.data.toolName ?? "";
        const turn = this.turnFor(e.data);
        if (!name || !turn) return;

        // Flush prose first, so the tool row lands after the sentence that
        // introduced it rather than before it.
        this.flush();
        const toolId = e.data.toolId ?? "";
        const part: ToolPart = {
          kind: "tool",
          id: `${turn.id}:${toolId || this.mintId("tool")}`,
          call: describeTool(toolId, name, e.data.toolInput ?? ""),
        };
        turn.parts.push(part);
        // Indexed by the backend's ID rather than searched for later: the
        // result comes back by that ID, and every other part on screen is
        // beside the point when it does.
        //
        // Read back out of `parts` rather than indexed as it was written:
        // `entries` is deep state, so what went in is the plain object and
        // what comes out is the proxy. Only writes through the proxy are
        // noticed, and a result written to the plain object would be a row
        // that stayed on "running" with the answer already in it.
        if (toolId) this.tools.set(toolId, turn.parts[turn.parts.length - 1] as ToolPart);
      }),

      on(CLAUDE_TOOL_RESULT, (e) => {
        const toolId = e.data.toolId ?? "";
        if (!toolId) return;
        const part = this.tools.get(toolId);
        if (!part) return;
        part.call.result = e.data.toolResult ?? "";
        part.call.failed = e.data.toolFailed ?? false;
        part.call.done = true;
      }),

      on(CLAUDE_RESULT, (e) => {
        this.flush();
        if (e.data.phase === "chat") {
          this.chatCostUsd += e.data.costUsd ?? 0;
        } else {
          this.runCostUsd += e.data.costUsd ?? 0;
        }
        // And the conversation's own total, which nothing resets but a clear.
        // Counted here rather than below the plan-phase return so a routing
        // turn is in it: it was paid for, and a number that quietly omits a
        // third of the spend is worse than no number.
        this.totalCostUsd += e.data.costUsd ?? 0;
        // The routing turn has no entry of its own -- the plan stands in for it
        // -- but its cost still belongs to the run.
        if (e.data.phase === "plan") return;

        const turn = this.turnFor(e.data);
        if (!turn) return;
        turn.costUsd = e.data.costUsd ?? 0;
        turn.durationMs = e.data.durationMs ?? 0;
        if (turn.status === "streaming") turn.status = "done";
      }),

      on(CLAUDE_ERROR, (e) => {
        this.flush();
        const message = e.data.message || "Claude failed";
        const turn = this.turnFor(e.data);
        if (turn) {
          turn.error = message;
          turn.status = "error";
          return;
        }
        this.notice(message, "error");
      }),

      // A run-wide cancellation, distinct from a failure: nothing went wrong.
      on(CLAUDE_CANCELLED, () => {
        this.flush();
        this.closeOpenTurns("cancelled", "run");
      }),

      // The side channel: the coordinator answering while work is in flight.
      on(CHAT_STARTED, (e) => {
        this.chatId = e.data.runId;
        this.chatCostUsd = 0;
        this.chatStatus = "working";
      }),

      on(CHAT_FINISHED, (e) => {
        this.flush();
        if (e.data.cancelled) {
          this.closeOpenTurns("cancelled", "chat");
          this.chatStatus = "cancelled";
        } else if (e.data.message) {
          this.closeOpenTurns("error", "chat");
          this.notice(e.data.message, "error");
          this.chatStatus = "error";
        } else {
          this.closeOpenTurns("done", "chat");
          if (this.chatCostUsd > 0) {
            this.notice(`Question cost $${this.chatCostUsd.toFixed(4)}`, "info");
          }
          this.chatStatus = "done";
        }
        this.chatId = null;
      }),
    ];
  }

  /**
   * Sends a request. Your words appear immediately, before the backend has
   * confirmed anything, because waiting to see what you typed reads as lag.
   */
  async submit(prompt: string, agentId = ""): Promise<void> {
    const text = prompt.trim();
    if (!text) return;

    // Work is already in flight, so this is a question about it rather than a
    // second run. Asking beats being told to wait, which is what the disabled
    // composer used to say.
    if (this.busy) {
      await this.ask(text);
      return;
    }
    if (this.chatBusy) return;

    this.entries.push({ kind: "user", id: this.mintId("you"), text, agentId });
    this.status = "planning";

    try {
      const task = await Workbench.Submit(this.conversation, agentId, text);
      this.runId = task.id;
    } catch (err) {
      this.notice(messageOf(err), "error");
      this.status = "error";
      this.runId = null;
    }
  }

  /**
   * Puts a question to the coordinator without starting a run.
   *
   * This is what the composer does while specialists are working. The answer
   * arrives in its own lane, so the run's output is not interleaved with it.
   */
  async ask(prompt: string): Promise<void> {
    const text = prompt.trim();
    if (!text || this.chatBusy) return;

    this.entries.push({ kind: "user", id: this.mintId("you"), text, agentId: "" });
    this.chatStatus = "planning";

    try {
      const task = await Workbench.Chat(this.conversation, text);
      this.chatId = task.id;
    } catch (err) {
      this.notice(messageOf(err), "error");
      this.chatStatus = "error";
      this.chatId = null;
    }
  }

  /** Asks the backend to stop the run, specialists included. */
  async cancel(): Promise<void> {
    const id = this.runId;
    if (!id) return;
    try {
      await Workbench.Cancel(id);
    } catch (err) {
      this.notice(messageOf(err), "error");
      this.status = "error";
    }
  }

  /** Stops the coordinator answering, leaving the run untouched. */
  async cancelChat(): Promise<void> {
    const id = this.chatId;
    if (!id) return;
    try {
      await Workbench.Cancel(id);
    } catch (err) {
      this.notice(messageOf(err), "error");
      this.chatStatus = "error";
    }
  }

  /**
   * Empties the console and starts a new conversation. The agents forget the
   * exchange too, so clearing the screen and clearing their memory are the same
   * gesture rather than two that can disagree.
   */
  /**
   * Empties what is on screen, without telling the backend anything.
   *
   * The half of clear() that is about this transcript alone. Used for the two
   * conversations that are not the one whose button was pressed -- see
   * Conversations.clear(), and why the backend's half is the window's.
   */
  forget(): void {
    this.cancelFlush();
    this.pending.clear();
    this.entries = [];
    this.turns.clear();
    this.tools.clear();
    this.status = "idle";
    this.runId = null;
    this.runCostUsd = 0;
    this.chatStatus = "idle";
    this.chatId = null;
    this.chatCostUsd = 0;
    this.totalCostUsd = 0;
    this.draft = "";
    // The indexes point into the transcript that just went, so they go with
    // it. A stale turn left in here would take a new run's events.
  }

  /**
   * Empties this transcript and forgets the agents' memory of it.
   *
   * This one, and nothing else. The backend half used to be the window's --
   * every session of every agent, plus the board -- which was defensible when
   * there were three transcripts and one of them was always the one you meant.
   * There is one per tab per mode now, and a button that emptied all of them
   * and the board would be a button nobody could afford to press.
   *
   * The screen half is forget(), because the two are the same act seen from
   * either side and keeping two copies of that list is how they drift.
   */
  async clear(): Promise<void> {
    this.forget();
    try {
      await Workbench.ClearConversation(this.conversation);
    } catch {
      // Nothing useful to say: the screen is clear either way, and the next
      // request will simply continue the old session.
    }
  }

  private mintId(prefix: string): string {
    this.nextId += 1;
    return `${prefix}-${this.nextId}`;
  }

  private notice(text: string, tone: "info" | "error"): void {
    this.entries.push({ kind: "notice", id: this.mintId("note"), text, tone });
  }

  private identify(agentId: string): AgentIdentity {
    return this.roster.get(agentId) ?? { id: agentId, name: agentId, colour: "#8b93a3" };
  }

  /**
   * Finds the entry for an agent's turn, creating it on first sight. Returns
   * null for the routing phase, which the plan entry represents instead.
   */
  private turnFor(data: {
    taskId?: string;
    agentId?: string;
    phase?: string;
  }): AgentEntry | null {
    const taskId = data.taskId ?? "";
    if (!taskId || data.phase === "plan") return null;

    const existing = this.turns.get(taskId);
    if (existing) return existing;

    const who = this.identify(data.agentId ?? "");
    const turn: AgentEntry = {
      kind: "agent",
      id: taskId,
      agentId: who.id,
      agentName: who.name,
      colour: who.colour,
      phase: phaseOf(data.phase),
      parts: [],
      status: "streaming",
      error: null,
      model: "",
      costUsd: 0,
      durationMs: 0,
    };
    this.entries.push(turn);
    // The proxy, not the object that was pushed -- see the tool index above.
    // Everything after this point writes to the turn expecting it to show.
    const tracked = this.entries[this.entries.length - 1] as AgentEntry;
    this.turns.set(taskId, tracked);
    return tracked;
  }

  private appendText(
    data: { taskId?: string; agentId?: string; phase?: string },
    text: string,
  ): void {
    if (!text) return;
    const turn = this.turnFor(data);
    if (!turn) return;
    if (turn.phase !== "chat" && this.status === "planning") {
      this.status = "working";
    }

    this.pending.set(turn.id, (this.pending.get(turn.id) ?? "") + text);

    if (this.flushHandle) return;
    this.flushHandle = requestAnimationFrame(() => {
      this.flushHandle = 0;
      this.flush();
    });
  }

  private flush(): void {
    if (this.pending.size === 0) return;
    for (const [id, text] of this.pending) {
      const turn = this.turns.get(id);
      if (!turn) continue;

      // Append to the trailing run of prose, or start a new one if the last
      // thing that happened was a tool call.
      const last = turn.parts[turn.parts.length - 1];
      if (last && last.kind === "text") {
        last.text += text;
      } else {
        turn.parts.push({ kind: "text", id: this.mintId("text"), text });
      }
    }
    this.pending.clear();
  }

  /**
   * Closes out turns still marked streaming when their lane ends.
   *
   * The two lanes are closed separately because they end separately: a run
   * finishing must not mark the answer you are still reading as cancelled,
   * and stopping a question must not close the work.
   */
  private closeOpenTurns(status: TurnStatus, lane: "run" | "chat"): void {
    for (const entry of this.entries) {
      if (entry.kind !== "agent" || entry.status !== "streaming") continue;
      if ((entry.phase === "chat") !== (lane === "chat")) continue;
      entry.status = status;
    }
  }

  private cancelFlush(): void {
    if (!this.flushHandle) return;
    cancelAnimationFrame(this.flushHandle);
    this.flushHandle = 0;
  }
}

function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
