/**
 * Writes of this machine's that did not survive, and the way back.
 *
 * The merge is last-write-wins and always has been. What changed is that it now
 * says so: `packages/ops` reports the value a write displaced, the sync loop
 * recognises the ones this replica wrote, and this holds them until somebody
 * has seen them.
 *
 * A note says what the line said and what it says now. It does not say who,
 * because nothing anywhere knows -- see ConflictEvent in Go, which has no room
 * for an actor on purpose. If a sentence here needs a subject other than the
 * line, the sentence is wrong.
 */
import { Events } from "@wailsio/runtime";

import { WORKSPACE_CONFLICT } from "../bridge/events";
import type { Workspace } from "./workspace.svelte";

/** One write that is gone, and what it said. */
export interface Note {
  /** The line it happened to. */
  node: string;
  /** Which field lost. Empty when the whole line was deleted. */
  field: string;
  /** What this machine wrote. This is what Undo puts back. */
  yours: string;
  /** What the document says instead. Empty when the line is gone. */
  now: string;
  /** True when the line was deleted rather than changed. */
  deleted: boolean;
}

export class Conflicts {
  /**
   * The notes on screen, newest last.
   *
   * One per node and field: a second loss on the same field replaces the first
   * rather than stacking. Two notes about one line is a queue nobody works
   * through, and the older value is the one further from what anybody wants
   * back.
   */
  notes = $state.raw<Note[]>([]);

  /** For the live region, which is the only way this is heard rather than seen. */
  said = $state("");

  /** Subscribes to the backend. Returns the unsubscribe. */
  listen(): () => void {
    return Events.On(WORKSPACE_CONFLICT, (e) => {
      const data = e.data as Partial<Note> | undefined;
      if (!data?.node) return;

      const note: Note = {
        node: data.node,
        field: data.field ?? "",
        yours: data.yours ?? "",
        now: data.now ?? "",
        deleted: data.deleted === true,
      };

      const key = (n: Note) => `${n.node} ${n.field}`;
      this.notes = [...this.notes.filter((n) => key(n) !== key(note)), note];
      this.said = describeNote(note);
    });
  }

  /** The notes concerning lines this workspace holds. */
  forWorkspace(workspace: Workspace): Note[] {
    const here = new Set(workspace.rows.map((r) => r.node.id));
    for (const task of workspace.tasks) here.add(task.id);
    return this.notes.filter((n) => here.has(n.node));
  }

  dismiss(note: Note): void {
    this.notes = this.notes.filter((n) => n !== note);
  }

  dismissAll(): void {
    this.notes = [];
    this.said = "";
  }

  /**
   * Puts the lost text back, as a new edit.
   *
   * Not a special path and not a rollback: this is `setText`, the same call a
   * keystroke makes. It becomes an ordinary op with a fresh clock, so it beats
   * what beat it, the other replica receives it, and they can undo in turn.
   * That symmetry is the point -- nobody's edit is privileged, including the
   * one made by the person who noticed.
   */
  undo(workspace: Workspace, note: Note): void {
    if (note.deleted) return;
    workspace.setText(note.node, note.yours);
    this.dismiss(note);
    this.said = "Put your version back.";
  }
}

/** One note, in words. Reads the same on screen and to a screen reader. */
export function describeNote(note: Note): string {
  if (note.deleted) {
    return `A line you were editing has been deleted. You had written “${clip(note.yours)}”.`;
  }
  return `Changed elsewhere. You had written “${clip(note.yours)}”; it now says “${clip(note.now)}”.`;
}

function clip(text: string, limit = 80): string {
  const flat = text.trim().replace(/\s+/g, " ");
  if (flat === "") return "nothing";
  return flat.length > limit ? flat.slice(0, limit - 1) + "…" : flat;
}
