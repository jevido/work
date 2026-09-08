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
} from "../bridge/events";

export type ConsoleStatus =
  | "idle"
  | "starting"
  | "streaming"
  | "done"
  | "cancelled"
  | "error";

export interface RunMeta {
  sessionId: string;
  model: string;
  costUsd: number;
  durationMs: number;
}

/**
 * The state behind the Claude console.
 *
 * Text arrives one token at a time, which is far too often to drive Svelte
 * directly. Chunks accumulate in a plain string and are flushed once per
 * animation frame, so a fast stream costs one update per frame instead of one
 * per token.
 */
export class ClaudeSession {
  /** Everything Claude has said during this run. */
  transcript = $state("");
  /** Names of tools used in this run, in order, de-duplicated consecutively. */
  tools = $state<string[]>([]);
  status = $state<ConsoleStatus>("idle");
  error = $state<string | null>(null);
  taskId = $state<string | null>(null);
  meta = $state<RunMeta>({ sessionId: "", model: "", costUsd: 0, durationMs: 0 });

  /** True while a task can still be cancelled. */
  busy = $derived(this.status === "starting" || this.status === "streaming");

  private pending = "";
  private flushHandle = 0;

  /** Subscribes to backend events. Returns an unsubscribe function. */
  listen(): () => void {
    const offs = [
      Events.On(CLAUDE_SESSION, (e) => {
        this.meta.sessionId = e.data.sessionId ?? "";
        this.meta.model = e.data.model ?? "";
      }),
      Events.On(CLAUDE_TEXT, (e) => this.appendText(e.data.text ?? "")),
      Events.On(CLAUDE_THINKING, () => {
        // Thinking is streamed but not shown yet: a dedicated, collapsible
        // panel is the right home for it, not the main transcript.
        if (this.status === "starting") this.status = "streaming";
      }),
      Events.On(CLAUDE_TOOL, (e) => {
        const name = e.data.toolName ?? "";
        if (!name) return;
        if (this.tools[this.tools.length - 1] === name) return;
        this.tools = [...this.tools, name];
      }),
      Events.On(CLAUDE_RESULT, (e) => {
        this.flush();
        this.meta.costUsd = e.data.costUsd ?? 0;
        this.meta.durationMs = e.data.durationMs ?? 0;
        this.status = "done";
        this.taskId = null;
      }),
      Events.On(CLAUDE_CANCELLED, () => {
        this.flush();
        this.status = "cancelled";
        this.taskId = null;
      }),
      Events.On(CLAUDE_ERROR, (e) => {
        this.flush();
        this.error = e.data.message || "Claude failed";
        this.status = "error";
        this.taskId = null;
      }),
    ];

    return () => {
      for (const off of offs) off();
      this.cancelFlush();
    };
  }

  /** Starts a task. Rejects only on a backend refusal, which becomes an error. */
  async submit(prompt: string, agentId = ""): Promise<void> {
    const trimmed = prompt.trim();
    if (!trimmed || this.busy) return;

    this.reset();
    this.status = "starting";

    try {
      const task = await Workbench.Submit(agentId, trimmed);
      this.taskId = task.id;
    } catch (err) {
      this.error = messageOf(err);
      this.status = "error";
      this.taskId = null;
    }
  }

  /** Asks the backend to stop the running task. */
  async cancel(): Promise<void> {
    const id = this.taskId;
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
    this.pending = "";
    this.transcript = "";
    this.tools = [];
    this.error = null;
    this.status = "idle";
    this.meta = { sessionId: "", model: "", costUsd: 0, durationMs: 0 };
  }

  private appendText(text: string): void {
    if (!text) return;
    if (this.status === "starting") this.status = "streaming";
    this.pending += text;
    if (this.flushHandle) return;
    this.flushHandle = requestAnimationFrame(() => {
      this.flushHandle = 0;
      this.flush();
    });
  }

  private flush(): void {
    if (!this.pending) return;
    this.transcript += this.pending;
    this.pending = "";
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
