import type {
  PermissionChoice,
  PermissionMode,
} from "../../../bindings/dev.jevido/work/internal/claude/models.js";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";

/**
 * What the agents are allowed to do.
 *
 * One object rather than a mode and a list of modes: the toggle cannot draw a
 * choice without its label, and the backend answers every call with the whole
 * setting so what is on screen is what the workbench holds rather than what
 * the last click hoped for.
 */
export class Permissions {
  /** The mode in force, or null until the backend has been asked. */
  mode = $state<PermissionMode | null>(null);

  /** The modes on offer, least powerful first, in the order to show them. */
  choices = $state<PermissionChoice[]>([]);

  /**
   * Whether the local Claude CLI will honour the dangerous mode.
   *
   * Starts true so a warning is never shown before the answer arrives. The
   * CLI drops the mode silently when its disclaimer has not been accepted, so
   * this being false is the difference between "off" and "off, and nothing
   * told you".
   */
  bypassAccepted = $state(true);

  /** The one command that accepts that disclaimer. */
  acceptCommand = $state("");

  /** True while a change is in flight. */
  busy = $state(false);

  /** Why the last change did not stick. */
  error = $state<string | null>(null);

  /** The mode in force, with its label and detail. */
  current = $derived(this.choices.find((c) => c.id === this.mode) ?? null);

  /** Reads the mode and the modes on offer. */
  async load(): Promise<void> {
    await this.#apply(() => Workbench.Permissions());
  }

  /**
   * Changes what the agents may do.
   *
   * The answer is the backend's whole setting, so a mode it refused leaves the
   * one actually in force on screen rather than the one that was clicked.
   */
  async set(mode: PermissionMode): Promise<void> {
    if (this.busy || mode === this.mode) return;
    await this.#apply(() => Workbench.SetPermissionMode(mode));
  }

  async #apply(read: () => Promise<Awaited<ReturnType<typeof Workbench.Permissions>>>) {
    this.busy = true;
    this.error = null;
    try {
      const p = await read();
      this.mode = p.mode;
      // A Go nil slice arrives as null, which would be a toggle with nothing
      // in it rather than an empty one.
      this.choices = p.choices ?? [];
      this.bypassAccepted = p.bypassAccepted;
      this.acceptCommand = p.acceptCommand;
    } catch (err) {
      this.error = messageOf(err);
    } finally {
      this.busy = false;
    }
  }
}

function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
