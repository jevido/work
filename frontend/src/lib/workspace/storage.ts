/**
 * Workspaces on disk, such as disk is inside a webview.
 *
 * Local storage rather than the Go side, and that is a decision with a cost
 * worth writing down: it is per-webview, so a workspace made here is invisible
 * to anything else on the machine, and clearing site data loses whatever had
 * not synced. The alternative is a backend call through a service that belongs
 * to somebody else this week. When that lands, this file is the whole of what
 * changes -- nothing above it knows where a workspace is kept.
 *
 * Everything read back is checked rather than cast. The store can hold
 * anything: an older version of this app wrote it, or somebody edited it, or
 * it is half a write that was interrupted.
 */
import type { Mode } from "./model";
import type { Op } from "./ops";
import type { Outgoing } from "./sync.svelte";
import type { WorkspaceSnapshot } from "./workspace.svelte";

const KEY = "work.workspaces.v1";

export interface StoredState {
  workspaces: WorkspaceSnapshot[];
  activeId: string | null;
}

const EMPTY: StoredState = { workspaces: [], activeId: null };

export function load(): StoredState {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    // Storage can be denied outright. A session with no memory still works.
    return EMPTY;
  }
  if (!raw) return EMPTY;

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return EMPTY;
  }

  const record = asRecord(parsed);
  if (!record) return EMPTY;

  const workspaces = asArray(record.workspaces)
    .map(asSnapshot)
    .filter((w): w is WorkspaceSnapshot => w !== null);

  const activeId = typeof record.activeId === "string" ? record.activeId : null;
  return {
    workspaces,
    activeId: workspaces.some((w) => w.id === activeId) ? activeId : null,
  };
}

export function save(state: StoredState): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(state));
  } catch {
    // Quota, or a private window that refuses. Losing the ability to remember
    // is not a reason to stop working, and there is nothing useful to say
    // about it on a keystroke.
  }
}

/* -------------------------------------------------------------------------- */

function asSnapshot(value: unknown): WorkspaceSnapshot | null {
  const r = asRecord(value);
  if (!r || typeof r.id !== "string" || r.id === "") return null;

  return {
    id: r.id,
    name: typeof r.name === "string" && r.name !== "" ? r.name : "Workspace",
    base: nonEmpty(r.base),
    key: nonEmpty(r.key),
    mode: asMode(r.mode),
    seq: typeof r.seq === "number" && Number.isFinite(r.seq) && r.seq >= 0 ? r.seq : 0,
    // Handed to State.fromJSON, which validates every field it reads and
    // ignores the rest. A second validator here would be a second copy of the
    // merge's own shape to keep in step with it.
    state: r.state ?? null,
    outbox: asArray(r.outbox).filter(isOutgoing),
    drafts: asDrafts(r.drafts),
  };
}

function asMode(value: unknown): Mode {
  return value === "planning" || value === "work" ? value : "idea";
}

function asDrafts(value: unknown): Record<string, string> {
  const r = asRecord(value);
  if (!r) return {};
  const out: Record<string, string> = {};
  for (const [id, text] of Object.entries(r)) if (typeof text === "string") out[id] = text;
  return out;
}

/**
 * A queued op, loosely.
 *
 * The op itself is not validated here on purpose: it is going to be handed to
 * State.apply, which validates every op it is given -- including this one --
 * and a copy of those rules in this file would be a second place to update
 * when the protocol grows a kind.
 */
function isOutgoing(value: unknown): value is Outgoing {
  const r = asRecord(value);
  if (!r) return false;
  const op = asRecord(r.op) as Op | null;
  return !!op && typeof op.id === "string" && op.id !== "";
}

function nonEmpty(value: unknown): string | null {
  return typeof value === "string" && value.trim() !== "" ? value : null;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}
