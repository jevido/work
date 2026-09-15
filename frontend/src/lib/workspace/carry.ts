/**
 * Carrying the notes somebody already took into the workspace they now have.
 *
 * Before the outline had a home in Go, it lived in localStorage: per tab, per
 * machine, going nowhere. Those are somebody's notes. A migration that starts
 * the workspace empty is a migration that loses work that was done, and it will
 * be one person and a handful of lines, and they will be their lines.
 *
 * Once per tab, and only for a tab that has somewhere to send to. A machine
 * with nothing joined keeps using the local store exactly as it always has —
 * there is nowhere else for its outline to go, and that is the supported case
 * rather than a fallback.
 */
import type { Edit } from "../../../bindings/dev.jevido/work/internal/workbench/models.js";
import { FIELD_TEXT, FIELD_TYPE } from "./model";
import type { TreeNode } from "./ops";
import { markMigrated, migrated, storedSnapshot } from "./storage";
import { State } from "./ops";
import type { Workspace } from "./workspace.svelte";

// The one kind this file uses. Spelled once because the wire's Kind and the
// frontend's are two generated-and-hand-written declarations of the same five
// words, and TypeScript will not take one for the other.
const CREATE = "create-node" as Edit["kind"];

/** The line local notes are hung under when the tab already has an outline. */
export const CARRIED_LABEL = "Notes from this machine";

/**
 * Carries one tab's stored outline into its workspace, if there is one to carry
 * and it has not been carried already.
 *
 * Returns the edits it sent, which is what a test asserts on and what a caller
 * can count to decide whether anything happened. An empty array is the ordinary
 * outcome on most machines and says nothing to anybody.
 */
export async function carryLocalNotes(workspace: Workspace): Promise<Edit[]> {
  if (!workspace.send) return [];
  if (migrated(workspace.id)) return [];

  const snapshot = storedSnapshot(workspace.id);
  if (!snapshot?.state) {
    // Nothing stored is the common case and is also a migration: marking it
    // means this does not re-read an empty store on every launch forever.
    markMigrated(workspace.id);
    return [];
  }

  let stored: TreeNode[];
  try {
    stored = State.fromJSON(snapshot.state).tree();
  } catch {
    // An unreadable store is left alone rather than marked. It is somebody's
    // notes and a later release may be able to read what this one cannot.
    return [];
  }
  if (stored.length === 0) {
    markMigrated(workspace.id);
    return [];
  }

  // Where the carried notes hang. If the tab already has an outline, they go
  // under a line that says what they are: interleaving two documents that were
  // never one produces an outline nobody can read, and there is no correct
  // place to put a local line among lines a colleague wrote.
  const edits: Edit[] = [];
  let root = "";
  if (workspace.rows.length > 0) {
    root = `carried-${workspace.id}`;
    edits.push({
      kind: CREATE,
      node: root,
      parent: "",
      position: "zzzz",
      fields: { [FIELD_TYPE]: "idea", [FIELD_TEXT]: CARRIED_LABEL },
    });
  }

  // Depth first, parents before children, so every edit names a parent that
  // already exists by the time it is applied.
  const walk = (nodes: readonly TreeNode[], parent: string) => {
    for (const node of nodes) {
      edits.push({
        kind: CREATE,
        node: node.id,
        parent,
        position: node.position,
        // The fields as they were. Keeping the ids as well means a task that
        // was extracted from one of these lines still points at it.
        fields: { ...node.fields },
      });
      walk(node.children, node.id);
    }
  };
  walk(stored, root);

  await workspace.send(edits);
  markMigrated(workspace.id);
  return edits;
}
