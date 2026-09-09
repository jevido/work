import { Events } from "@wailsio/runtime";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import { RUN_CHANGES } from "../bridge/events";
import { diffStat, parseUnifiedDiff, type DiffHunkLine } from "../diff/diff";

export interface FileChange {
  path: string;
  status: string;
  binary: boolean;
  /** False when Work cannot put the file back exactly as it was. */
  restorable: boolean;
  diff: DiffHunkLine[];
  added: number;
  removed: number;
  /** Set while a revert is in flight, or after it fails. */
  reverting: boolean;
  error: string | null;
}

/**
 * What the last run did to the working tree.
 *
 * The Claude CLI offers no hook to approve a write before it happens, so the
 * review sits after the fact: here is what changed, with a revert that restores
 * the content the run started with rather than the last commit.
 */
export class ChangeReview {
  files = $state<FileChange[]>([]);
  /** False when the working directory is not a git repository. */
  tracked = $state(true);

  isEmpty = $derived(this.files.length === 0);

  /**
   * Whether files have been touched since the review was last looked at.
   *
   * The diff lives behind a button now, so the button has to carry the news as
   * well as the count: "3 files" that have been read is a different thing to
   * "3 files" that landed while you were reading the conversation, and a bare
   * number cannot tell them apart.
   */
  unseen = $state(false);

  listen(): () => void {
    return Events.On(RUN_CHANGES, (e) => {
      this.tracked = e.data.tracked ?? false;
      const next = (e.data.changes ?? []).map(toFileChange);
      // A path that was not in the list before is news. A list that only lost
      // paths is the revert that was just asked for, republished -- flagging
      // that would light the button up in answer to your own click.
      const known = new Set(this.files.map((f) => f.path));
      if (next.some((f) => !known.has(f.path))) this.unseen = true;
      // Nothing left to review is nothing left to be behind on.
      if (next.length === 0) this.unseen = false;
      this.files = next;
    });
  }

  /**
   * Reloads the list, for when the app starts mid-conversation.
   *
   * Deliberately not news: these are changes that were already on disk when
   * the window opened, so the button shows the count without claiming
   * something just happened.
   */
  async refresh(): Promise<void> {
    try {
      const list = await Workbench.Changes();
      this.files = (list ?? []).map(toFileChange);
    } catch {
      // The office already reports an unreachable backend.
    }
  }

  /** Called when the review is opened: the news has been read. */
  markSeen(): void {
    this.unseen = false;
  }

  /** Puts one file back the way it was before the run. */
  async revert(path: string): Promise<void> {
    const file = this.files.find((f) => f.path === path);
    if (!file || file.reverting) return;
    file.reverting = true;
    file.error = null;
    try {
      await Workbench.Revert(path);
      // The backend republishes the list, which drops this entry.
    } catch (err) {
      file.error = err instanceof Error ? err.message : String(err);
      file.reverting = false;
    }
  }
}

function toFileChange(c: {
  path: string;
  status: string;
  patch?: string;
  binary?: boolean;
  restorable?: boolean;
}): FileChange {
  const diff = c.binary ? [] : parseUnifiedDiff(c.patch ?? "");
  const stat = diffStat(diff);
  return {
    path: c.path,
    status: c.status,
    binary: c.binary ?? false,
    restorable: c.restorable ?? false,
    diff,
    added: stat.added,
    removed: stat.removed,
    reverting: false,
    error: null,
  };
}
