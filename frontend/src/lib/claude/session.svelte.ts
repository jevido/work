import { Events } from "@wailsio/runtime";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import { describeTool, type ToolCall } from "../diff/tools";
import {
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

export type Phase = "plan" | "work" | "synthesis";

export type TurnStatus = "streaming" | "done" | "error" | "cancelled";

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

  /** True while the run can still be cancelled. */
  busy = $derived(this.status === "planning" || this.status === "working");

  /** True when there is nothing on screen to clear. */
  isEmpty = $derived(this.entries.length === 0);

  /** What the run in progress has cost, planning turn included. */
  private runCostUsd = 0;
  private nextId = 0;

  /** Agent identities, for labelling turns and plan steps. */
  private roster = new Map<string, AgentIdentity>();

  /** Text waiting to be flushed, by entry ID. */
  private pending = new Map<string, string>();
  private flushHandle = 0;

  /** Tells the console who the agents are. Safe to call again on a change. */
  setAgents(list: readonly AgentIdentity[]): void {
    this.roster = new Map(list.map((a) => [a.id, a]));

    // An agent renamed from their desk is still the same agent, so the turns
    // they have already taken are relabelled too. Leaving them alone would
    // read as two people having answered one question.
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
  }

  /** An agent's display name, falling back to their ID if they are unknown. */
  nameOf(agentId: string): string {
    return this.identify(agentId).name;
  }

  /** Subscribes to backend events. Returns an unsubscribe function. */
  listen(): () => void {
    const offs = [
      Events.On(RUN_STARTED, (e) => {
        this.runId = e.data.runId;
        this.runCostUsd = 0;
        this.status = "planning";
      }),

      Events.On(RUN_PLAN, (e) => {
        const who = this.identify(e.data.agentId ?? "");
        this.entries.push({
          kind: "plan",
          id: this.mintId("plan"),
          agentId: who.id,
          agentName: who.name,
          colour: who.colour,
          mode: e.data.mode === "team" ? "team" : "self",
          reason: e.data.reason ?? "",
          steps: (e.data.steps ?? []).map((s) => {
            const agent = this.identify(s.agentId);
            return {
              agentId: s.agentId,
              agentName: agent.name,
              colour: agent.colour,
              task: s.task,
              taskId: s.taskId ?? "",
            };
          }),
          updates: (e.data.updates ?? []).map((u) => ({
            taskId: u.taskId,
            status: u.status ?? "",
            title: u.title ?? "",
            agentName: u.agentId ? this.identify(u.agentId).name : "",
          })),
        });
        this.status = "working";
      }),

      Events.On(RUN_FINISHED, (e) => {
        this.flush();
        if (e.data.cancelled) {
          this.closeOpenTurns("cancelled");
          this.notice("Stopped.", "info");
          this.status = "cancelled";
        } else if (e.data.message) {
          this.closeOpenTurns("error");
          this.notice(e.data.message, "error");
          this.status = "error";
        } else {
          this.closeOpenTurns("done");
          if (this.runCostUsd > 0) {
            this.notice(`Run cost $${this.runCostUsd.toFixed(4)}`, "info");
          }
          this.status = "done";
        }
        this.runId = null;
      }),

      Events.On(CLAUDE_SESSION, (e) => {
        const turn = this.turnFor(e.data);
        if (turn) turn.model = e.data.model ?? "";
      }),

      Events.On(CLAUDE_TEXT, (e) => this.appendText(e.data, e.data.text ?? "")),

      Events.On(CLAUDE_THINKING, () => {
        // Thinking is streamed but not shown yet: a dedicated, collapsible
        // panel is the right home for it, not the conversation.
      }),

      Events.On(CLAUDE_TOOL, (e) => {
        const name = e.data.toolName ?? "";
        const turn = this.turnFor(e.data);
        if (!name || !turn) return;

        // Flush prose first, so the tool row lands after the sentence that
        // introduced it rather than before it.
        this.flush();
        turn.parts.push({
          kind: "tool",
          id: `${turn.id}:${e.data.toolId || this.mintId("tool")}`,
          call: describeTool(e.data.toolId ?? "", name, e.data.toolInput ?? ""),
        });
      }),

      Events.On(CLAUDE_TOOL_RESULT, (e) => {
        const toolId = e.data.toolId ?? "";
        if (!toolId) return;
        const part = this.findToolPart(toolId);
        if (!part) return;
        part.call.result = e.data.toolResult ?? "";
        part.call.failed = e.data.toolFailed ?? false;
        part.call.done = true;
      }),

      Events.On(CLAUDE_RESULT, (e) => {
        this.flush();
        this.runCostUsd += e.data.costUsd ?? 0;
        // The routing turn has no entry of its own -- the plan stands in for it
        // -- but its cost still belongs to the run.
        if (e.data.phase === "plan") return;

        const turn = this.turnFor(e.data);
        if (!turn) return;
        turn.costUsd = e.data.costUsd ?? 0;
        turn.durationMs = e.data.durationMs ?? 0;
        if (turn.status === "streaming") turn.status = "done";
      }),

      Events.On(CLAUDE_ERROR, (e) => {
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
      Events.On(CLAUDE_CANCELLED, () => {
        this.flush();
        this.closeOpenTurns("cancelled");
      }),
    ];

    return () => {
      for (const off of offs) off();
      this.cancelFlush();
    };
  }

  /**
   * Sends a request. Your words appear immediately, before the backend has
   * confirmed anything, because waiting to see what you typed reads as lag.
   */
  async submit(prompt: string, agentId = ""): Promise<void> {
    const text = prompt.trim();
    if (!text || this.busy) return;

    this.entries.push({ kind: "user", id: this.mintId("you"), text, agentId });
    this.status = "planning";

    try {
      const task = await Workbench.Submit(agentId, text);
      this.runId = task.id;
    } catch (err) {
      this.notice(messageOf(err), "error");
      this.status = "error";
      this.runId = null;
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

  /**
   * Empties the console and starts a new conversation. The agents forget the
   * exchange too, so clearing the screen and clearing their memory are the same
   * gesture rather than two that can disagree.
   */
  async clear(): Promise<void> {
    this.cancelFlush();
    this.pending.clear();
    this.entries = [];
    this.status = "idle";
    this.runId = null;
    this.runCostUsd = 0;
    try {
      await Workbench.ClearConversation();
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

    const existing = this.entries.find(
      (e): e is AgentEntry => e.kind === "agent" && e.id === taskId,
    );
    if (existing) return existing;

    const who = this.identify(data.agentId ?? "");
    const turn: AgentEntry = {
      kind: "agent",
      id: taskId,
      agentId: who.id,
      agentName: who.name,
      colour: who.colour,
      phase: data.phase === "synthesis" ? "synthesis" : "work",
      parts: [],
      status: "streaming",
      error: null,
      model: "",
      costUsd: 0,
      durationMs: 0,
    };
    this.entries.push(turn);
    return turn;
  }

  private appendText(
    data: { taskId?: string; agentId?: string; phase?: string },
    text: string,
  ): void {
    if (!text) return;
    const turn = this.turnFor(data);
    if (!turn) return;
    if (this.status === "planning") this.status = "working";

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
      const turn = this.entries.find(
        (e): e is AgentEntry => e.kind === "agent" && e.id === id,
      );
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

  /** Finds a tool part by the tool-use ID the backend gave it. */
  private findToolPart(toolId: string): ToolPart | null {
    for (const entry of this.entries) {
      if (entry.kind !== "agent") continue;
      for (const part of entry.parts) {
        if (part.kind === "tool" && part.call.id === toolId) return part;
      }
    }
    return null;
  }

  /** Closes out any turn still marked streaming when the run ends. */
  private closeOpenTurns(status: TurnStatus): void {
    for (const entry of this.entries) {
      if (entry.kind === "agent" && entry.status === "streaming") entry.status = status;
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
