/**
 * Every call the config folder needs, and the only place their names appear.
 *
 * Agents live as folders on disk now -- PERSONALITY.md, skills/, maybe an
 * avatar -- so the app has to be told where that folder is, be able to re-read
 * it, and be able to be pointed somewhere else. Three calls, imported here and
 * nowhere else: if the backend renames one, this file is the only thing that
 * stops compiling, which is the point of it existing.
 */
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";

/**
 * The saved folder, or null if none has been chosen.
 *
 * The backend returns "" on a first run rather than an error, because nobody
 * having picked a folder yet is the normal way to open this app once.
 */
export async function getConfigPath(): Promise<string | null> {
  return orNull(await Workbench.GetConfigPath());
}

/**
 * Asks the user for a folder and returns it, or null if they cancelled.
 *
 * The backend saves the choice and loads the team from it, so a non-null
 * answer means the folder is already in use -- what is left is for the app to
 * read the team back and show it.
 *
 * Cancelling comes back as "" with no error, and must not read as one: the
 * picker closing with nothing chosen leaves the screen exactly as it was.
 */
export async function selectConfigFolder(): Promise<string | null> {
  return orNull(await Workbench.SelectConfigFolder());
}

/**
 * Re-reads the config folder.
 *
 * It answers with the new team, which is deliberately dropped: the roster has
 * one way of reading itself and this is not a second one. The cost is a single
 * extra local call, on an action a person just took by hand.
 */
export async function reloadAgents(): Promise<void> {
  await Workbench.ReloadAgents();
}

/** A Go empty string means "nothing", not "a folder called nothing". */
function orNull(value: string): string | null {
  const path = value.trim();
  return path === "" ? null : path;
}
