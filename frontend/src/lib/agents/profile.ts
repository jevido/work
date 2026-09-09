/**
 * What one agent's folder says about them, read when their profile is opened.
 *
 * Not part of the roster on purpose. The roster is re-sent on every reload and
 * every state change and is held by the office, the board and the console; a
 * personality file can be tens of kilobytes, and only the one panel that is
 * open needs it. So this is a call of its own, imported here and nowhere else.
 */
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";

/**
 * An agent's folder, with the backend's absences turned into empty values.
 *
 * A Go nil slice arrives as null and an omitted string as undefined, which is
 * three ways of saying "nothing" for a panel that only wants to know whether
 * there is anything to show. They are flattened here so the view can ask.
 */
export type AgentFolder = {
  /** The folder this was read from, or null for an agent with no folder. */
  dir: string | null;
  /** Skill names found in `skills/`. */
  skills: string[];
  /** The built-in skillset, empty for an agent that came from a folder. */
  skillset: string[];
  /** The one line the coordinator routes this agent on. */
  blurb: string;
  /** `PERSONALITY.md`, heading removed. */
  personality: string;
};

/** Reads an agent's folder. Throws if the folder exists but cannot be read. */
export async function readFolder(agentId: string): Promise<AgentFolder> {
  const p = await Workbench.AgentProfile(agentId);
  return {
    dir: p.dir?.trim() ? p.dir : null,
    skills: p.skills ?? [],
    skillset: p.skillset ?? [],
    blurb: p.blurb ?? "",
    personality: p.personality ?? "",
  };
}
