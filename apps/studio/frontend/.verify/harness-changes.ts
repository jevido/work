/**
 * The change-review harness: the real app over a fake backend, with run:changes
 * pushed in exactly the way Go pushes it.
 *
 * Events reach the app through window._wails.dispatchWailsEvent, which is the
 * same entry point the desktop runtime calls, so Events.On in the app is wired
 * up for real -- only the process on the other end of it is fake.
 *
 * Revert republishes the whole list, because that is what the backend does
 * (Workbench.Revert ends in publishChanges), and the panel's behaviour when the
 * list it is showing is replaced under it is most of what there is to check.
 */
import { setTransport } from "@wailsio/runtime";
import { mount } from "svelte";
import App from "../src/App.svelte";
import fixture from "./agents.json";

const AGENTS = 1687195856;
const GET_CONFIG_PATH = 4273611995;
const RELOAD_AGENTS = 1153881361;
const SUBMIT = 3576145318;
const CANCEL = 1254614124;
const CHANGES = 2330002051;
const REVERT = 261357752;

export const calls: { id: number; args: any[] }[] = [];

/** The working tree, as this fake backend has it. */
let tree: any[] = [];
/** A path Revert refuses, the way it does mid-run. */
let refuse: string | null = null;

function push(name: string, data: any) {
  (window as any)._wails.dispatchWailsEvent({ name, data });
}

function publish() {
  push("run:changes", { runId: "run-1", tracked: true, changes: tree });
}

setTransport({
  async call(objectID: number, _method: number, _win: string, args: any) {
    if (objectID !== 0) return null;
    const { methodID, args: a = [] } = args ?? {};
    calls.push({ id: methodID, args: a });

    if (methodID === GET_CONFIG_PATH) return "/home/jevido/agents";
    if (methodID === AGENTS || methodID === RELOAD_AGENTS) return fixture;
    if (methodID === SUBMIT) return { id: "task-1" };
    if (methodID === CHANGES) return tree;
    if (methodID === REVERT) {
      const path = a[0];
      if (path === refuse) {
        throw new Error("workbench: stop the run before reverting its changes");
      }
      tree = tree.filter((c) => c.path !== path);
      publish();
      return null;
    }
    return null;
  },
} as any);

(window as any).__verify = {
  calls,
  CANCEL,
  /** A run's worth of file changes arriving, the way a finished run delivers them. */
  land(changes: any[], refusePath: string | null = null) {
    tree = changes.map((c) => ({ binary: false, restorable: true, ...c }));
    refuse = refusePath;
    publish();
  },
  /** A line of an agent's turn, so there is a conversation to be pushed out of. */
  say(text: string) {
    push("claude:text", { taskId: "task-1", agentId: "anton", phase: "work", text });
  },
  started() {
    push("run:started", { runId: "run-1" });
  },
  finished() {
    push("run:finished", {});
  },
  treeNow: () => tree,
  /**
   * A screenshot, taken by the WebKit host rather than the page.
   *
   * Resolves once the host has written the file, so the next thing the driver
   * does cannot land in the picture.
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

void import("./drive-changes");
