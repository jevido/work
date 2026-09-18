/**
 * The office harness's transport: the real app, mounted over a fake backend.
 *
 * The office is only drawn once a config folder is settled, so this answers
 * GetConfigPath with one -- the folder picker itself is not driven from here
 * (see harness-config.ts and vite.stub.config.ts for that), so the real
 * SelectConfigFolder, which opens a dialog on somebody's desktop, is never
 * reached.
 */
import { setTransport } from "@wailsio/runtime";
import { mount } from "svelte";
import App from "../src/App.svelte";
import fixture from "./agents.json";

const AGENTS = 1687195856;
const GET_CONFIG_PATH = 4273611995;
const RELOAD_AGENTS = 1153881361;

/** The folder the app is told it is configured with. */
export const CONFIG_PATH = "/home/jevido/agents";

let agents: any[] = JSON.parse(JSON.stringify(fixture));
export const calls: { id: number; args: any[] }[] = [];

setTransport({
  async call(objectID: number, _method: number, _win: string, args: any) {
    // Only bound-method calls matter here; event subscriptions are no-ops.
    if (objectID !== 0) return null;
    const { methodID, args: a = [] } = args ?? {};
    calls.push({ id: methodID, args: a });

    if (methodID === GET_CONFIG_PATH) return CONFIG_PATH;
    if (methodID === AGENTS || methodID === RELOAD_AGENTS) return agents;
    return null;
  },
} as any);

(window as any).__verify = {
  calls,
  configPath: CONFIG_PATH,
  agentsNow: () => agents,
};

mount(App, { target: document.getElementById("app")! });

// Dynamic so it runs after the transport is installed and App is mounted.
void import("./drive");
