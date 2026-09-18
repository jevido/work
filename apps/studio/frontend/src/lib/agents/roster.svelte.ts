import type { AgentStatus } from "../../../bindings/dev.jevido/work/apps/studio/internal/workbench/models.js";
import * as Workbench from "../../../bindings/dev.jevido/work/apps/studio/internal/bridge/workbenchservice.js";
import type { AgentIdentity } from "../claude/session.svelte";

/**
 * The team, as the backend last told us.
 *
 * The office, the board and the console all label things by agent, so the
 * roster is held whole here rather than read once and mapped into three
 * snapshots that can disagree with each other. Nothing in the app edits an
 * agent any more -- they are folders on disk -- but reloading the config
 * folder replaces this list, and every label that names somebody follows it.
 */
export class Roster {
  /** The team in the order the backend lists them. */
  list = $state<AgentStatus[]>([]);

  /** Why the roster could not be read, if it could not be. */
  loadError = $state<string | null>(null);

  /**
   * How many times the team has been read.
   *
   * Avatars are files, and a file can be replaced without anything about the
   * agent changing: same folder, same filename, new picture. Nothing in the
   * roster would differ, so this is what tells the office to fetch it again --
   * it goes in the avatar URL, and reading the team is exactly the moment the
   * files behind it are worth re-reading.
   */
  revision = $state(0);

  /** The minimum the console and the board need to label a turn or a card. */
  identities = $derived<AgentIdentity[]>(
    this.list.map((a) => ({ id: a.id, name: a.name, colour: a.colour })),
  );

  /** Reads the team. Safe to call again; a failure leaves the old list alone. */
  async load(): Promise<void> {
    try {
      const list = await Workbench.Agents();
      // A Go nil slice arrives as null, so an empty team is not an error.
      this.list = list ?? [];
      this.loadError = null;
      this.revision++;
    } catch (err) {
      this.loadError = messageOf(err);
    }
  }

  /**
   * Who leads a conversation in this mode.
   *
   * The same question Go's `Registry.For` answers, asked here for one reason:
   * the console has to say whose box you are typing into, and naming the wrong
   * agent is worse than naming none. It is a read of the roster rather than a
   * call, because the roster is already here and a round trip per keystroke to
   * learn a name would be absurd.
   *
   * Falls back to the coordinator exactly as Go does, and then to null for a
   * team that has neither -- which is a roster that failed to load, and is
   * already drawn as an empty office.
   */
  leadFor(mode: string): AgentStatus | null {
    return (
      this.list.find((a) => (a.modes ?? []).includes(mode)) ??
      this.list.find((a) => a.role === "coordinator") ??
      null
    );
  }

  /** One agent, or null if they are not on the team. */
  find(agentId: string): AgentStatus | null {
    return this.list.find((a) => a.id === agentId) ?? null;
  }

}

function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
