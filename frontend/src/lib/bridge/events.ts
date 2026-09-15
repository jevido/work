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
export const CLAUDE_TOOL_RESULT = "claude:tool-result";
export const CLAUDE_RESULT = "claude:result";
export const CLAUDE_ERROR = "claude:error";
export const CLAUDE_CANCELLED = "claude:cancelled";

export const RUN_STARTED = "run:started";
export const RUN_PLAN = "run:plan";
export const RUN_FINISHED = "run:finished";
export const RUN_CHANGES = "run:changes";

/** The coordinator's side channel: a question answered alongside a run. */
export const CHAT_STARTED = "chat:started";
export const CHAT_FINISHED = "chat:finished";

export const BOARD_UPDATED = "board:updated";

/**
 * The tool call a restructuring proposal arrives as.
 *
 * Not an event of its own. A proposal is Claude asking to change the outline,
 * and Claude asks for things by calling a tool -- so it reaches the frontend
 * the same way every other tool call does, over `claude:tool`, and this is the
 * name to match on. The payload is the tool's input, read by
 * lib/workspace/proposal.ts, which refuses anything that is not a set of
 * operations over named nodes.
 *
 * Matching on a name rather than adding an event keeps the backend out of it:
 * a proposal is reviewed before anything is written, so the only thing that
 * has to understand one is whatever is going to apply it, which is here.
 */
/**
 * The tool Claude calls to propose a restructuring, as it arrives on the
 * stream.
 *
 * The `mcp__work__` prefix is not decoration and not optional. An MCP tool is
 * namespaced by the server that offers it, the server is named `work` in the
 * config the backend writes for the CLI, and the name is assembled from that
 * in Go as propose.FullToolName. The two have to agree exactly: `Review.listen`
 * compares this string against the tool name on the event and drops anything
 * else without a word, so a mismatch is not an error anybody sees — it is
 * every proposal silently never arriving.
 */
export const PROPOSE_TOOL = "mcp__work__propose_restructure";

/**
 * The workspace itself changed: joined, left, or its tabs moved. Carries the
 * sync status with it, so a join does not need a second round trip to draw
 * the indicator.
 */
export const WORKSPACE_CHANGED = "workspace:changed";

/**
 * Where sync stands -- online, syncing, offline or rejected, how many ops are
 * waiting, and how far behind the server this machine is.
 *
 * Pushed rather than polled. The frontend used to run its own loop and its own
 * outbox; it does not any more, so this event and WorkbenchService.SyncStatus
 * are the only two places the badge's state comes from.
 */
export const WORKSPACE_SYNC = "workspace:sync";

/**
 * A newer release exists. Emitted by the update check at startup, not in
 * answer to anything the user did, so whatever shows it has to be interruptible
 * rather than modal.
 */
export const UPDATE_AVAILABLE = "update:available";
