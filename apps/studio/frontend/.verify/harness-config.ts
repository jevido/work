/** The form harness's transport, mounting the app against stubbed config calls. */
import { setTransport } from "@wailsio/runtime";
import { mount } from "svelte";
import App from "../src/App.svelte";
import fixture from "./agents.json";

const AGENTS = 1687195856;

let agents: any[] = JSON.parse(JSON.stringify(fixture));
export const calls: { id: number; args: any[] }[] = [];

setTransport({
  async call(objectID: number, _method: number, _win: string, args: any) {
    if (objectID !== 0) return null;
    const { methodID, args: a = [] } = args ?? {};
    calls.push({ id: methodID, args: a });
    if (methodID === AGENTS) return agents;
    return null;
  },
} as any);

(window as any).__verify = {
  calls,
  agentsNow: () => agents,
  /** Drops an agent, the way deleting their folder and reloading would. */
  dropLast: () => agents.pop(),
};

const saved = new URL(location.href).searchParams.get("saved");
if (saved) (window as any).__config.saved(saved);

mount(App, { target: document.getElementById("app")! });

void import("./drive-config");
