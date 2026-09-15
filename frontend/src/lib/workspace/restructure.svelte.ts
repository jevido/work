/**
 * Asking Claude to restructure, and knowing when it is still thinking.
 *
 * A thin thing on purpose. The proposal does not come back through here: the
 * backend starts a run, Claude answers by calling a tool, and the tool call
 * goes past on the stream `Review.listen` is already watching. All this owns is
 * the gap between "asked" and "answered", which is the part the person has to
 * be able to see.
 *
 * One asking at a time, workspace-wide. Two proposals in flight means the
 * second replaces the first when they land -- `Review.take` says so out loud --
 * and the cheapest way to avoid having to explain that is to not let it happen.
 */
import { Events } from "@wailsio/runtime";

import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import { CHAT_FINISHED } from "../bridge/events";
import type { Mode } from "./model";
import type { Workspace } from "./workspace.svelte";

export class Restructuring {
  /** The workspace id a request is in flight for, or null. */
  asking = $state<string | null>(null);

  /** The last refusal, until something else happens. */
  refused = $state<string | null>(null);

  /** For a live region: said once, then left alone. */
  said = $state("");

  /** Whether this workspace can be asked right now. */
  busy(workspace: Workspace): boolean {
    return this.asking !== null && this.asking === workspace.id;
  }

  /**
   * Subscribes to the end of a run. Returns the unsubscribe.
   *
   * The finish event rather than a timer: a run that fails, is cancelled or
   * answers in prose all end the same way from here, and all three have to
   * give the control back.
   */
  listen(): () => void {
    return Events.On(CHAT_FINISHED, () => {
      this.asking = null;
    });
  }

  /**
   * Asks, and leaves the answer to the review panel.
   *
   * `focus` is the row the person had the caret on. It is appended to the
   * request rather than narrowing what Claude is shown, and that is
   * deliberate: reorganising a branch usually means moving something out of
   * it or into it, so a state block cut down to the branch would hide the
   * only places the answer could go.
   */
  async ask(
    workspace: Workspace,
    mode: Mode,
    request: string,
    focus?: { id: string; text: string } | null,
  ): Promise<void> {
    const said = request.trim();
    if (said === "" || this.asking !== null) return;

    let full = said;
    if (focus) {
      full += `\n\nThe line I have selected is ${focus.id} — “${focus.text}”. Start there.`;
    }

    this.asking = workspace.id;
    this.refused = null;
    this.said = "Asked Claude to suggest changes. Nothing has changed yet.";

    try {
      await Workbench.Restructure(workspace.id, mode, full);
    } catch (err) {
      this.asking = null;
      this.refused = err instanceof Error ? err.message : String(err);
      this.said = `Could not ask Claude: ${this.refused}`;
    }
  }
}
