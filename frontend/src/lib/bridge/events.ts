/**
 * The semantic event contract with the Go backend.
 *
 * Keep in sync with internal/workbench/events.go. The payload types come from
 * the generated bindings, so a change on the Go side surfaces here as a type
 * error rather than a silent mismatch.
 */
export const AGENT_ASSIGNED = "agent:assigned";
export const AGENT_WORKING = "agent:working";
export const AGENT_FINISHED = "agent:finished";
export const AGENT_ERROR = "agent:error";

export const CLAUDE_SESSION = "claude:session";
export const CLAUDE_TEXT = "claude:text";
export const CLAUDE_THINKING = "claude:thinking";
export const CLAUDE_TOOL = "claude:tool";
export const CLAUDE_RESULT = "claude:result";
export const CLAUDE_ERROR = "claude:error";
export const CLAUDE_CANCELLED = "claude:cancelled";
