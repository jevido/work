import { Events } from "@wailsio/runtime";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import {
  CLAUDE_CANCELLED,
  CLAUDE_ERROR,
  CLAUDE_RESULT,
  CLAUDE_SESSION,
  CLAUDE_TEXT,
  CLAUDE_THINKING,
  CLAUDE_TOOL,
  RUN_FINISHED,
  RUN_PLAN,
  RUN_STARTED,
} from "../bridge/events";

/** Where a run currently is. Drives the console's status line. */
export type RunStatus =
  | "idle"
  | "planning"
  | "working"
  | "done"
  | "cancelled"
  | "error";

export type Phase = "plan" | "work" | "synthesis";

export type BlockStatus = "streaming" | "done" | "error" | "cancelled";

/** One agent's turn, as shown in the console. */
export interface ConsoleBlock {
  taskId: string;
  agentId: string;
  agentName: string;
  colour: string;
  phase: Phase;
  text: string;
  tools: string[];
  status: BlockStatus;
  error: string | null;
  model: string;
  costUsd: number;
  durationMs: number;
}

/** Anton's routing decision, shown above the work it produced. */
export interface PlanCard {
  mode: "self" | "team";
  reason: string;
  steps: { agentId: string; agentName: string; colour: string; task: string }[];
}

/** The minimum the console needs to label an agent. */
export interface AgentIdentity {
  id: string;
  name: string;
  colour: string;
}

/**
 * The state behind the Claude console.
 *
 * A run can involve several agents at once, so output is grouped into blocks
 * keyed by task rather than concatenated into one transcript. Text arrives one
 * token at a time, which is far too often to drive Svelte directly, so chunks
 * accumulate per task and flush once per animation frame: a fast stream costs
 * one update per frame instead of one per token.
 */
export class ClaudeSession {
  blocks = $state<ConsoleBlock[]>([]);
  plan = $state<PlanCard | null>(null);
  status = $state<RunStatus>("idle");
  error = $state<string | null>(null);
  runId = $state<string | null>(null);

  /** True while the run can still be cancelled. */
  busy = $derived(this.status === "planning" || this.status === "working");

  /** The planning turn's cost, which has no block of its own. */
  private planCostUsd = $state(0);

  /** What the whole run has cost so far, planning turn included. */
  totalCostUsd = $derived(
    this.blocks.reduce((sum, b) => sum + b.costUsd, 0) + this.planCostUsd,
  );

  /** Agent identities, for labelling blocks and plan steps. */
  private roster = new Map<string, AgentIdentity>();

  /** Text waiting to be flushed, by task ID. */
  private pending = new Map<string, string>();
  private flushHandle = 0;

  /** Tells the console who the agents are. Safe to call again on a change. */
  setAgents(list: readonly AgentIdentity[]): void {
    this.roster = new Map(list.map((a) => [a.id, a]));
  }

  /** Subscribes to backend events. Returns an unsubscribe function. */
  listen(): () => void {
    const offs = [
      Events.On(RUN_STARTED, (e) => {
        this.reset();
        this.runId = e.data.runId;
        this.status = "planning";
      }),

      Events.On(RUN_PLAN, (e) => {
        const steps = (e.data.steps ?? []).map((s) => {
          const who = this.identify(s.agentId);
          return { agentId: s.agentId, agentName: who.name, colour: who.colour, task: s.task };
        });
        this.plan = {
          mode: e.data.mode === "team" ? "team" : "self",
          reason: e.data.reason ?? "",
          steps,
        };
        this.status = "working";
      }),

      Events.On(RUN_FINISHED, (e) => {
        this.flush();
        if (e.data.cancelled) {
          this.markOpenBlocks("cancelled");
          this.status = "cancelled";
          return;
        }
        if (e.data.message) {
          this.error = e.data.message;
          this.markOpenBlocks("error");
          this.status = "error";
          return;
        }
        this.markOpenBlocks("done");
        this.status = "done";
      }),

      Events.On(CLAUDE_SESSION, (e) => {
        const block = this.blockFor(e.data);
        if (block) block.model = e.data.model ?? "";
      }),

      Events.On(CLAUDE_TEXT, (e) => this.appendText(e.data, e.data.text ?? "")),

      Events.On(CLAUDE_THINKING, () => {
        // Thinking is streamed but not shown yet: a dedicated, collapsible
        // panel is the right home for it, not the main output.
      }),

      Events.On(CLAUDE_TOOL, (e) => {
        const name = e.data.toolName ?? "";
        const block = this.blockFor(e.data);
        if (!name || !block) return;

        // Text before and after a tool call arrives as separate content
        // blocks with no separator of their own, so the two runs of prose
        // would otherwise be glued together mid-sentence.
        this.separate(block.taskId);

        if (block.tools[block.tools.length - 1] === name) return;
        block.tools.push(name);
      }),

      Events.On(CLAUDE_RESULT, (e) => {
        this.flush();
        // The planning turn has no block of its own -- the plan card stands in
        // for it -- but its cost still belongs to the run.
        if (e.data.phase === "plan") {
          this.planCostUsd += e.data.costUsd ?? 0;
          return;
        }
        const block = this.blockFor(e.data);
        if (!block) return;
        block.costUsd = e.data.costUsd ?? 0;
        block.durationMs = e.data.durationMs ?? 0;
        if (block.status === "streaming") block.status = "done";
      }),

      Events.On(CLAUDE_ERROR, (e) => {
        this.flush();
        const message = e.data.message || "Claude failed";
        const block = this.blockFor(e.data);
        if (block) {
          block.error = message;
          block.status = "error";
          return;
        }
        this.error = message;
      }),

      // A run-wide cancellation, distinct from a failure: nothing went wrong.
      Events.On(CLAUDE_CANCELLED, () => {
        this.flush();
        this.markOpenBlocks("cancelled");
      }),
    ];

    return () => {
      for (const off of offs) off();
      this.cancelFlush();
    };
  }

  /** Starts a run. A backend refusal surfaces as an error, not a rejection. */
  async submit(prompt: string, agentId = ""): Promise<void> {
    const trimmed = prompt.trim();
    if (!trimmed || this.busy) return;

    this.reset();
    this.status = "planning";

    try {
      const task = await Workbench.Submit(agentId, trimmed);
      this.runId = task.id;
    } catch (err) {
      this.error = messageOf(err);
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
      this.error = messageOf(err);
      this.status = "error";
    }
  }

  reset(): void {
    this.cancelFlush();
    this.pending.clear();
    this.blocks = [];
    this.plan = null;
    this.error = null;
    this.status = "idle";
    this.runId = null;
    this.planCostUsd = 0;
  }

  /** True when there is nothing to clear. */
  get isEmpty(): boolean {
    return this.blocks.length === 0 && !this.plan && !this.error;
  }

  private identify(agentId: string): AgentIdentity {
    return this.roster.get(agentId) ?? { id: agentId, name: agentId, colour: "#8b93a3" };
  }

  /**
   * Finds the block for an event, creating it on first sight. Returns null for
   * the planning phase, which the plan card represents instead.
   */
  private blockFor(data: {
    taskId?: string;
    agentId?: string;
    phase?: string;
  }): ConsoleBlock | null {
    const taskId = data.taskId ?? "";
    if (!taskId || data.phase === "plan") return null;

    const existing = this.blocks.find((b) => b.taskId === taskId);
    if (existing) return existing;

    const who = this.identify(data.agentId ?? "");
    const block: ConsoleBlock = {
      taskId,
      agentId: who.id,
      agentName: who.name,
      colour: who.colour,
      phase: data.phase === "synthesis" ? "synthesis" : "work",
      text: "",
      tools: [],
      status: "streaming",
      error: null,
      model: "",
      costUsd: 0,
      durationMs: 0,
    };
    this.blocks.push(block);
    return block;
  }

  private appendText(
    data: { taskId?: string; agentId?: string; phase?: string },
    text: string,
  ): void {
    if (!text) return;
    const block = this.blockFor(data);
    if (!block) return;
    if (this.status === "planning") this.status = "working";

    const taskId = block.taskId;
    this.pending.set(taskId, (this.pending.get(taskId) ?? "") + text);

    if (this.flushHandle) return;
    this.flushHandle = requestAnimationFrame(() => {
      this.flushHandle = 0;
      this.flush();
    });
  }

  /** Ensures the next text for a task starts on a new paragraph. */
  private separate(taskId: string): void {
    const pending = this.pending.get(taskId);
    const tail = pending ?? this.blocks.find((b) => b.taskId === taskId)?.text ?? "";
    if (!tail || tail.endsWith("\n\n")) return;
    this.pending.set(taskId, (pending ?? "") + (tail.endsWith("\n") ? "\n" : "\n\n"));
  }

  private flush(): void {
    if (this.pending.size === 0) return;
    for (const [taskId, text] of this.pending) {
      const block = this.blocks.find((b) => b.taskId === taskId);
      if (block) block.text += text;
    }
    this.pending.clear();
  }

  /** Closes out any block still marked streaming when the run ends. */
  private markOpenBlocks(status: BlockStatus): void {
    for (const block of this.blocks) {
      if (block.status === "streaming") block.status = status;
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
