/**
 * The generated bindings with the three config calls replaced by stubs.
 *
 * GetConfigPath and ReloadAgents would work over the harness transport, but
 * SelectConfigFolder opens the platform's folder picker -- a real one, on the
 * desktop of whoever is running this. These stand in for all three so the
 * setup screen and the settings menu can be driven without a dialog appearing.
 *
 * Aliased over workbenchservice.js by .verify/vite.stub.config.ts. The rest of
 * the module is re-exported untouched; a local export of the same name wins
 * over a star export, which is what replaces these three. The real one is
 * named with its .ts extension so the alias does not match it and loop.
 */
export * from "../bindings/dev.jevido/work/services/workbenchservice.ts";

const state = { path: "", next: "/home/jevido/agents", calls: [] as string[] };

export async function GetConfigPath(): Promise<string> {
  state.calls.push("GetConfigPath");
  return state.path;
}

/** Saves and loads the folder it returns, the way the real one does. */
export async function SelectConfigFolder(): Promise<string> {
  state.calls.push("SelectConfigFolder");
  // "" is a cancelled picker, which the app must treat as "nothing changed".
  if (state.next) state.path = state.next;
  return state.next;
}

export async function ReloadAgents(): Promise<unknown> {
  state.calls.push("ReloadAgents");
  return (window as any).__verify.agentsNow();
}

(window as any).__config = {
  calls: state.calls,
  pathNow: () => state.path,
  /** What the next picker returns. "" is the user cancelling. */
  picks: (p: string) => (state.next = p),
  /** Pretends a folder was already saved before the window opened. */
  saved: (p: string) => (state.path = p),
};
