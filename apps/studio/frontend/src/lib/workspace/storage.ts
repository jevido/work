/**
 * The local outline, per tab, such as disk is inside a webview.
 *
 * This file used to hold whole workspaces: their server, their **write key**,
 * their outbox and their merged state. None of that is here any more and none
 * of it should ever come back. The workspace, its keys and its unsent ops are
 * the Go side's; they live in the config file and the outbox, which fsync and
 * which survive a webview whose site data somebody cleared. A credential in
 * localStorage was a second copy of the one secret in this app, kept in the
 * least durable and least protected place it could be kept.
 *
 * What is left is the Orientation and Isolation outline. It is local, it is per tab,
 * and it does not leave this machine. `ApplyWorkspaceEdits` now gives it a
 * home in Go, so this file is on its way out -- but not by deletion: whatever
 * is already in this store is somebody's notes, and the move has to carry
 * them over rather than start them again empty.
 *
 * Everything read back is checked rather than cast. The store can hold
 * anything: an older version of this app wrote it, or somebody edited it, or
 * it is half a write that was interrupted.
 */
import { Workspace, type OutlineSnapshot } from "./workspace.svelte";
import { modeFrom } from "./model";

const KEY = "work.outlines.v1";

/**
 * Which tabs have had their local notes carried into the workspace.
 *
 * Marked rather than deleted, and marked in its own key. A crash halfway
 * through a migration must not produce half an outline and no way back: the
 * notes stay where they are until the mark says they arrived, and the mark is
 * what stops it happening twice.
 */
const MIGRATED_KEY = "work.outlines.migrated.v1";

/**
 * The store this replaced.
 *
 * Removed rather than ignored, and removed on the first read rather than left
 * for a migration nobody will write. It holds write keys. Leaving it in place
 * would mean every machine that has ever run the previous build keeps a
 * working credential in localStorage forever, for a code path that no longer
 * reads it -- which is the worst kind of leftover, because nothing will ever
 * touch it again to notice.
 */
const RETIRED_KEY = "work.workspaces.v1";

interface Stored {
  activeId: string | null;
  tabs: Record<string, OutlineSnapshot>;
}

let cache: Stored | null = null;

function read(): Stored {
  if (cache) return cache;
  cache = { activeId: null, tabs: {} };

  try {
    localStorage.removeItem(RETIRED_KEY);
  } catch {
    // Storage can be denied outright, in which case there was nothing in it.
  }

  let raw: string | null = null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    // A session with no memory still works.
    return cache;
  }
  if (!raw) return cache;

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return cache;
  }

  const record = asRecord(parsed);
  if (!record) return cache;

  const tabs = asRecord(record.tabs) ?? {};
  for (const [id, value] of Object.entries(tabs)) {
    const snapshot = asSnapshot(value);
    if (snapshot) cache.tabs[id] = snapshot;
  }
  cache.activeId = typeof record.activeId === "string" ? record.activeId : null;
  return cache;
}

/** The outline for a tab, restored if there is one and empty if there is not. */
export function open(id: string): Workspace {
  return new Workspace(id, read().tabs[id] ?? null);
}

/** Which tab was in front last time, for a first paint that has no other answer. */
export function lastActive(): string | null {
  return read().activeId;
}

export function save(workspaces: readonly Workspace[], activeId: string | null): void {
  const state = read();
  // Merged into what is already there rather than replacing it. A tab that is
  // not open right now -- a colleague's tab this machine has not bound, or one
  // that went away while the window was shut -- still has notes in it, and a
  // save that wrote only the open tabs would quietly drop them.
  for (const workspace of workspaces) state.tabs[workspace.id] = workspace.snapshot();
  state.activeId = activeId;

  try {
    localStorage.setItem(KEY, JSON.stringify(state));
  } catch {
    // Quota, or a private window that refuses. Losing the ability to remember
    // is not a reason to stop working, and there is nothing useful to say
    // about it on a keystroke.
  }
}

/* -------------------------------------------------------------------------- */

function asSnapshot(value: unknown): OutlineSnapshot | null {
  const r = asRecord(value);
  if (!r) return null;
  return {
    // Through modeFrom, so a snapshot written before the modes were renamed
    // comes back as the mode it meant rather than as the first one.
    mode: modeFrom(r.mode) ?? "orientation",
    // Handed to State.fromJSON, which validates every field it reads and
    // ignores the rest. A second validator here would be a second copy of the
    // document's own shape to keep in step with it.
    state: r.state ?? null,
    drafts: asDrafts(r.drafts),
  };
}

function asDrafts(value: unknown): Record<string, string> {
  const r = asRecord(value);
  if (!r) return {};
  const out: Record<string, string> = {};
  for (const [id, text] of Object.entries(r)) if (typeof text === "string") out[id] = text;
  return out;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

/** The outline stored for a tab, or null if there is nothing in it. */
export function storedSnapshot(id: string): OutlineSnapshot | null {
  return read().tabs[id] ?? null;
}

/** Whether this tab's local notes have already been carried over. */
export function migrated(id: string): boolean {
  try {
    const raw = localStorage.getItem(MIGRATED_KEY);
    if (!raw) return false;
    const list = JSON.parse(raw);
    return Array.isArray(list) && list.includes(id);
  } catch {
    // Unreadable is treated as "not yet", which risks doing it twice rather
    // than never. The caller checks the workspace as well, so a second run
    // finds the notes already there and does nothing.
    return false;
  }
}

export function markMigrated(id: string): void {
  try {
    const raw = localStorage.getItem(MIGRATED_KEY);
    const list: unknown = raw ? JSON.parse(raw) : [];
    const ids = Array.isArray(list) ? list.filter((v) => typeof v === "string") : [];
    if (!ids.includes(id)) ids.push(id);
    localStorage.setItem(MIGRATED_KEY, JSON.stringify(ids));
  } catch {
    // Nothing to be done, and nothing worth saying on a startup path.
  }
}

/** Forgets a tab's stored outline, once it is somewhere durable. */
export function dropStored(id: string): void {
  const state = read();
  delete state.tabs[id];
  try {
    localStorage.setItem(KEY, JSON.stringify(state));
  } catch {
    // See save().
  }
}
