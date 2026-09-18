/**
 * The handoff harness: the real app over a fake backend, with the agent:*
 * events pushed in exactly the way Go pushes them.
 *
 * The office's scripted sequences are checked for what they do in
 * .verify/sequences.ts, which steps the simulation directly and can read every
 * agent. This one is here for the half that cannot: the pixels. A folder, a
 * chair and an in-tray are all drawn outside the rectangles the renderer used
 * to have to repaint, so the thing worth checking on a real engine is that the
 * new art paints where it should and -- the part only a screen can answer --
 * that it leaves nothing behind when it goes.
 *
 * Events reach the app through window._wails.dispatchWailsEvent, which is the
 * entry point the desktop runtime calls, so Events.On in the app is wired up
 * for real and the phase on an event is the phase the office sees.
 */
import { setTransport } from "@wailsio/runtime";
import { mount } from "svelte";
import App from "../src/App.svelte";
import fixture from "./agents.json";

const AGENTS = 1687195856;
const GET_CONFIG_PATH = 4273611995;
const RELOAD_AGENTS = 1153881361;

export const calls: { id: number; args: any[] }[] = [];

function push(name: string, data: any) {
  (window as any)._wails.dispatchWailsEvent({ name, data });
}

setTransport({
  async call(objectID: number, _method: number, _win: string, args: any) {
    if (objectID !== 0) return null;
    const { methodID, args: a = [] } = args ?? {};
    calls.push({ id: methodID, args: a });
    if (methodID === GET_CONFIG_PATH) return "/home/jevido/agents";
    if (methodID === AGENTS || methodID === RELOAD_AGENTS) return fixture;
    return null;
  },
} as any);

/** One agent:* event, with the phase the office routes on. */
function agentEvent(name: string, agentId: string, phase: string, message = "") {
  push(name, { agentId, taskId: "task-1", runId: "run-1", phase, message });
}

(window as any).__verify = {
  calls,
  assigned: (id: string, phase = "work") => agentEvent("agent:assigned", id, phase),
  working: (id: string, phase = "work") => agentEvent("agent:working", id, phase),
  finished: (id: string, phase = "work") => agentEvent("agent:finished", id, phase),

  /** Whether the engine is asking for less movement, which switches all of this off. */
  reduced: () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,

  /**
   * A screenshot, taken by the WebKit host rather than the page. Resolves once
   * the file is written, so the next thing the driver does is not in it.
   */
  shot(name: string): Promise<void> {
    (window as any).__shot = name;
    return new Promise((resolve) => {
      const tick = setInterval(() => {
        if ((window as any).__shot === null) {
          clearInterval(tick);
          resolve();
        }
      }, 60);
    });
  },
};
(window as any).__shot = null;

mount(App, { target: document.getElementById("app")! });

void import("./drive-office-timeline");
