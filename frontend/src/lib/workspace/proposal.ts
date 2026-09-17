/**
 * What Claude is allowed to propose, and how it reads in English.
 *
 * A restructuring proposal is **a set of operations over named nodes**, and
 * deliberately nothing else. It never carries a tree, a list of children, a
 * sort key or a coordinate. That rule is the whole reason this file exists as
 * a layer rather than the model taking Claude's word for the document:
 *
 *   - A tree rewrite is unreviewable. "Here is the new outline" gives nobody a
 *     way to approve half of it, and no way to see what actually changed
 *     without diffing two documents by eye.
 *   - A sort key from outside is a merge bug waiting. Positions are minted
 *     against the siblings that exist *at the moment the edit is applied*, by
 *     ./bounds.ts and ./position.ts. A key computed against the document as it
 *     was when the proposal was written lands in the wrong gap the moment
 *     anybody else has moved a line -- and the result is not an error, it is a
 *     line quietly in the wrong place.
 *   - A whole-graph rewrite would tombstone and recreate nodes that did not
 *     change, which in an op log is not a no-op: it breaks every task's link
 *     back to the idea it came from.
 *
 * So a proposal names nodes by the ids they already have, and says where
 * things go *relative to a sibling*. The app mints the ops. Anything that
 * arrives with a `position`, a `children` or a `tree` in it is refused whole
 * rather than cleaned up, because a proposal that tried to place something
 * itself was written against a different idea of who owns placement, and the
 * rest of it cannot be trusted to mean what it says either.
 *
 * Nothing here applies anything. Turning a proposed op into a real one is
 * Workspace.applyProposed, so that every write to the document still goes
 * through the one replica with the one clock.
 */
import { MAX_ID_LEN } from "./ops";
import { TASK_STATES, type TaskState } from "./model";
// The document layer reaching into the drawing layer, deliberately: the set of
// glyph names that mean anything is exactly the set this build can draw, and a
// second list here would be a list that goes stale the first time one is added.
import { isIconName } from "../mindmap/icons";

/**
 * The longest line a proposal may write.
 *
 * Not a protocol limit -- the op envelope caps ids and positions, not field
 * values. This is a review limit: a row nobody can read is a row nobody can
 * approve, and a proposal that pastes an essay into one line of an outline is
 * not a restructuring.
 */
export const MAX_TEXT_LEN = 2000;

/**
 * The longest body a proposal may write onto a card.
 *
 * Larger than MAX_TEXT_LEN because it is a different thing: a title is a line,
 * and this is the paragraph that did not fit on it. Still bounded, and for the
 * same reason -- a review row nobody reads to the end is a row nobody has
 * actually approved. Mirrored in internal/propose/propose.go.
 */
export const MAX_DETAIL_LEN = 8000;

/**
 * The most ops one proposal may carry.
 *
 * Five hundred rather than two hundred, because of `replace`: a board of thirty
 * notes with its links, regions, glyphs and captions is a couple of hundred
 * ops, and a limit that refuses a whole board is a limit that removes the
 * feature. Mirrored in internal/propose/propose.go, which is where the model is
 * told about it -- a limit it is not told is a refusal after the work is done.
 */
export const MAX_OPS = 500;

/**
 * One proposed edit.
 *
 * `ref` on an insert is a name for a line that does not exist yet, so later
 * ops in the same proposal can put things under it -- "make a heading called
 * Networking, move these three under it" is one proposal, not two. Refs are
 * local to the proposal and never reach the document: they are resolved to
 * real ids as the ops are applied, in order.
 *
 * `after` is a sibling id (or ref), or null for "first among its siblings".
 * There is no "at index 4": an index is a position in a document that may have
 * changed since, and a neighbour is not.
 */
export type ProposedOp =
  | { kind: "set-text"; node: string; text: string }
  | { kind: "insert"; ref?: string; parent: string; after: string | null; text: string }
  | { kind: "move"; node: string; parent: string; after: string | null }
  | { kind: "delete"; node: string }
  | { kind: "promote"; node: string }
  | { kind: "set-status"; node: string; status: TaskState }
  | { kind: "link"; node: string; other: string }
  | { kind: "unlink"; node: string; other: string }
  | { kind: "group"; node: string; region?: string; name?: string }
  /** Put a glyph on a cluster head, or "" to take one off. */
  | { kind: "set-icon"; node: string; icon: string }
  /** Write why two lines are linked, onto the link between them. */
  | { kind: "caption"; node: string; other: string; text: string }
  /** Write the body of a card: what it is, at length. "" clears it. */
  | { kind: "set-detail"; node: string; text: string }
  /**
   * Put a card under one of the workspace's guidelines, or take it out.
   *
   * By id, and only an id the workspace already holds. Claude does not get to
   * add a guideline: what a workspace is trying to be is a decision somebody
   * made, and a model that could invent one would quietly turn a vocabulary
   * into a pile of near-synonyms. A card that wants a word nobody has set up is
   * something to say in prose.
   */
  | { kind: "guide"; node: string; guideline: string }
  | { kind: "unguide"; node: string; guideline: string }
  /** Record that one of the named people or groups is waiting on a card. */
  | { kind: "interest"; node: string; party: string }
  | { kind: "uninterest"; node: string; party: string }
  /**
   * Start the board again.
   *
   * Everything on it now is moved under one collapsed line and left there. Not
   * a delete, and that is the point: a tombstone wins the merge in both
   * directions, so a wholesale delete would be the one proposal Undo could not
   * take back -- and "Claude replaced everything" is the proposal most worth
   * being able to reverse. See undo.ts, where its inverse is a set of moves.
   */
  | { kind: "replace"; reason: string };

export interface Proposal {
  /** One line saying what the whole set is for. Claude's words, shown as-is. */
  summary: string;
  ops: ProposedOp[];
}

/** Why a proposal was refused, for the console rather than for the person. */
export class ProposalError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ProposalError";
  }
}

/**
 * Reads a proposal out of whatever the tool call carried.
 *
 * Tool input reaches the frontend as a string, because that is how the backend
 * relays it -- see ClaudeSession and `claude:tool`. An object is accepted too
 * so a caller that already parsed it does not have to stringify it back.
 *
 * Throws rather than returning null, and the message names the offending op,
 * because the one consumer of a failure is a log line that has to be enough to
 * fix the tool's schema by.
 */
export function readProposal(input: unknown): Proposal {
  let body: unknown = input;
  if (typeof body === "string") {
    const text = body.trim();
    if (text === "") throw new ProposalError("empty proposal");
    try {
      body = JSON.parse(text);
    } catch {
      throw new ProposalError("proposal is not JSON");
    }
  }

  const record = asRecord(body);
  if (!record) throw new ProposalError("proposal is not an object");

  const raw = record.ops;
  if (!Array.isArray(raw)) throw new ProposalError("proposal has no ops array");
  if (raw.length === 0) throw new ProposalError("proposal has no ops in it");
  if (raw.length > MAX_OPS) {
    throw new ProposalError(`proposal has ${raw.length} ops, over the ${MAX_OPS} limit`);
  }

  const ops: ProposedOp[] = [];
  const refs = new Set<string>();
  for (let i = 0; i < raw.length; i++) {
    ops.push(readOp(raw[i], i, refs));
  }

  const summary = typeof record.summary === "string" ? record.summary.trim() : "";
  return {
    // A proposal with nothing said about it still reviews fine -- every row
    // says what it does -- so this is a fallback, not a rejection.
    summary: summary === "" ? "Claude suggested a restructuring." : clip(summary, 300),
    ops,
  };
}

/* -------------------------------------------------------------------------- */

/**
 * Field names a proposal may not carry, whatever else is right about it.
 *
 * Checked by name on every op rather than only where they would be read.
 * `position` on a `set-text` is ignored by the reader below, and ignoring it
 * would be the wrong answer: it means the thing that wrote this believes it
 * places nodes, and the next op it writes may be one where that belief does
 * real damage.
 */
const FORBIDDEN = ["position", "children", "tree", "index", "order", "clock", "actor"];

function readOp(value: unknown, at: number, refs: Set<string>): ProposedOp {
  const r = asRecord(value);
  if (!r) throw new ProposalError(`op ${at}: not an object`);

  for (const name of FORBIDDEN) {
    if (name in r) {
      throw new ProposalError(
        `op ${at}: carries "${name}". A proposal names nodes and neighbours; it does not place them.`,
      );
    }
  }

  const kind = r.kind;
  switch (kind) {
    case "set-text":
      return { kind, node: id(r.node, at, "node"), text: text(r.text, at) };

    case "insert": {
      const ref = r.ref === undefined ? undefined : id(r.ref, at, "ref");
      if (ref !== undefined) {
        if (refs.has(ref)) throw new ProposalError(`op ${at}: ref "${ref}" is used twice`);
        refs.add(ref);
      }
      return {
        kind,
        ref,
        // "" is a root, exactly as it is in the protocol.
        parent: optionalId(r.parent, at, "parent"),
        after: after(r.after, at),
        text: text(r.text, at),
      };
    }

    case "move":
      return {
        kind,
        node: id(r.node, at, "node"),
        parent: optionalId(r.parent, at, "parent"),
        after: after(r.after, at),
      };

    case "delete":
      return { kind, node: id(r.node, at, "node") };

    case "promote":
      return { kind, node: id(r.node, at, "node") };

    case "set-status": {
      const status = r.status;
      if (typeof status !== "string" || !TASK_STATES.includes(status as TaskState)) {
        throw new ProposalError(
          `op ${at}: status is ${JSON.stringify(status)}, not one of ${TASK_STATES.join(", ")}`,
        );
      }
      return { kind, node: id(r.node, at, "node"), status: status as TaskState };
    }

    case "link":
    case "unlink":
      return { kind, node: id(r.node, at, "node"), other: id(r.other, at, "other") };

    case "set-detail":
      return { kind, node: id(r.node, at, "node"), text: detail(r.text, at) };

    case "guide":
    case "unguide":
      return { kind, node: id(r.node, at, "node"), guideline: id(r.guideline, at, "guideline") };

    case "interest":
    case "uninterest":
      return { kind, node: id(r.node, at, "node"), party: id(r.party, at, "party") };

    case "set-icon": {
      const icon = r.icon;
      if (typeof icon !== "string" || (icon !== "" && !isIconName(icon))) {
        throw new ProposalError(
          `op ${at}: icon is ${JSON.stringify(icon)}, which is not a glyph this build can draw`,
        );
      }
      return { kind, node: id(r.node, at, "node"), icon };
    }

    case "caption":
      return {
        kind,
        node: id(r.node, at, "node"),
        other: id(r.other, at, "other"),
        // An empty caption is a real answer: it is how one is taken off.
        text: text(r.text, at),
      };

    case "replace": {
      const reason = r.reason;
      if (typeof reason !== "string" || reason.trim() === "") {
        throw new ProposalError(`op ${at}: a replace says why, and this one does not`);
      }
      return { kind, reason: clip(reason.replace(/\s+/g, " ").trim(), 300) };
    }

    case "group": {
      // One or the other: a region that exists, or a name for a new one. Both
      // at once is a proposal that has not decided what it means, and guessing
      // which half to honour is how a line ends up in a region nobody asked
      // for.
      const region = r.region === undefined ? undefined : id(r.region, at, "region");
      const name = r.name === undefined ? undefined : text(r.name, at);
      if ((region === undefined) === (name === undefined)) {
        throw new ProposalError(
          `op ${at}: a group names either a region that exists or a name for a new one, not both and not neither`,
        );
      }
      return { kind, node: id(r.node, at, "node"), region, name };
    }

    default:
      throw new ProposalError(`op ${at}: unknown kind ${JSON.stringify(kind)}`);
  }
}

function id(value: unknown, at: number, field: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new ProposalError(`op ${at}: ${field} is missing`);
  }
  const trimmed = value.trim();
  if (trimmed.length > MAX_ID_LEN) {
    throw new ProposalError(`op ${at}: ${field} is over the ${MAX_ID_LEN} limit`);
  }
  return trimmed;
}

/** Like id(), but "" is a real answer: it is what the protocol calls a root. */
function optionalId(value: unknown, at: number, field: string): string {
  if (value === undefined || value === null || value === "") return "";
  return id(value, at, field);
}

function after(value: unknown, at: number): string | null {
  if (value === undefined || value === null || value === "") return null;
  return id(value, at, "after");
}

function text(value: unknown, at: number): string {
  if (typeof value !== "string") throw new ProposalError(`op ${at}: text is missing`);
  if (value.length > MAX_TEXT_LEN) {
    throw new ProposalError(`op ${at}: text is over the ${MAX_TEXT_LEN} limit`);
  }
  // Newlines are not an outline. A line is a line; a paragraph is several.
  return value.replace(/\s+/g, " ").trim();
}

/**
 * A card's body, which is the one string here that keeps its newlines.
 *
 * `text` above collapses whitespace because a line is a line. This is the other
 * case: a paragraph is several, and flattening one would be the layer that
 * refuses a tree also deciding how somebody's prose is laid out.
 */
function detail(value: unknown, at: number): string {
  if (typeof value !== "string") throw new ProposalError(`op ${at}: text is missing`);
  if (value.length > MAX_DETAIL_LEN) {
    throw new ProposalError(`op ${at}: text is over the ${MAX_DETAIL_LEN} limit`);
  }
  // Trailing space and \r from a model that wrapped its own output.
  return value.replace(/\r\n/g, "\n").trimEnd();
}

function clip(value: string, limit: number): string {
  return value.length > limit ? `${value.slice(0, limit - 1)}…` : value;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}
