/**
 * The workspace harness: the real app over a fake WorkbenchService.
 *
 * This exists for one claim above all others. The frontend used to hold a
 * second sync stack -- an HTTP transport, an outbox and a push loop -- pointed
 * at the same server the Go side already pushes to. It is gone, and "gone"
 * is a thing a type-check cannot show: a module can be deleted and its
 * behaviour survive in a fetch somewhere else. So `window.fetch` and
 * `XMLHttpRequest` are wrapped *before the app is imported*, and every request
 * the page makes for its whole life is recorded. The check is that the list is
 * empty.
 *
 * The canvas is instrumented the same way and for the same reason: the office
 * must keep the same <canvas> element through a mode switch and a tab switch,
 * and must put nothing into it while it is hidden. Both are questions about
 * the engine, so both are answered by counting what the engine was actually
 * asked to do rather than by reading a flag the app set.
 *
 * Events reach the app through window._wails.dispatchWailsEvent, which is the
 * entry point the desktop runtime calls, so Events.On in the app is wired up
 * for real -- only the process on the other end of it is fake.
 */

/* -------------------------------------------------------------------------- */
/* Instruments, installed before anything else is imported                    */
/* -------------------------------------------------------------------------- */

/** Every network request this page makes, by method and URL. */
const requests: { how: string; url: string }[] = [];

const realFetch = window.fetch.bind(window);
window.fetch = ((input: any, init?: any) => {
  const url = typeof input === "string" ? input : (input?.url ?? String(input));
  requests.push({ how: (init?.method ?? "GET").toUpperCase(), url });
  return realFetch(input, init);
}) as typeof window.fetch;

const realOpen = XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open = function (this: XMLHttpRequest, method: string, url: string | URL) {
  requests.push({ how: String(method).toUpperCase(), url: String(url) });
  // eslint-disable-next-line prefer-rest-params
  return realOpen.apply(this, arguments as any);
} as typeof XMLHttpRequest.prototype.open;

const realSend = navigator.sendBeacon?.bind(navigator);
if (realSend) {
  navigator.sendBeacon = ((url: string, data?: any) => {
    requests.push({ how: "BEACON", url: String(url) });
    return realSend(url, data);
  }) as typeof navigator.sendBeacon;
}

/**
 * Draw calls, per canvas element.
 *
 * Counted on the context rather than in the renderer, so this measures what
 * the engine was asked to paint and not what the app believes it asked for.
 * Keyed by the element, so "the same canvas" and "nothing drawn into it" are
 * two readings of one instrument.
 */
const drawn = new WeakMap<HTMLCanvasElement, { ops: number }>();
const PAINTS = new Set([
  "drawImage",
  "fillRect",
  "clearRect",
  "strokeRect",
  "fillText",
  "strokeText",
  "fill",
  "stroke",
  "putImageData",
]);

const realGetContext = HTMLCanvasElement.prototype.getContext;
HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, kind: string, ...rest: any[]) {
  const context = (realGetContext as any).call(this, kind, ...rest);
  if (kind !== "2d" || !context) return context;
  const counter = drawn.get(this) ?? { ops: 0 };
  drawn.set(this, counter);
  return new Proxy(context, {
    get(target, prop, receiver) {
      const value = Reflect.get(target, prop, receiver);
      if (typeof value !== "function" || !PAINTS.has(String(prop))) {
        return typeof value === "function" ? value.bind(target) : value;
      }
      return (...args: any[]) => {
        counter.ops++;
        return value.apply(target, args);
      };
    },
    set(target, prop, value) {
      return Reflect.set(target, prop, value);
    },
  });
} as typeof HTMLCanvasElement.prototype.getContext;

/* -------------------------------------------------------------------------- */

import { setTransport } from "@wailsio/runtime";
import { mount } from "svelte";
import App from "../src/App.svelte";
import fixture from "./agents.json";

const AGENTS = 1687195856;
const GET_CONFIG_PATH = 4273611995;
const RELOAD_AGENTS = 1153881361;
const PERMISSIONS = 228874366;
const BOARD = 105432782;
const CHANGES = 2330002051;
const VERSION = 1239790594;

const WORKSPACES = 1679766508;
const WORKSPACE = 1753161623;
const WORKSPACE_KEYS = 857101543;
const SYNC_STATUS = 1767437727;
const SYNC_NOW = 889870599;
const CREATE_WORKSPACE = 137920851;
const JOIN_WORKSPACE = 1719684323;
const LEAVE_WORKSPACE = 3221268550;
const NEW_TAB = 2023998847;
const CLOSE_TAB = 451949873;
const ACTIVATE_TAB = 969308478;
const BIND_TAB_FOLDER = 2990789672;

export const calls: { id: number; args: any[] }[] = [];

function push(name: string, data: any) {
  (window as any)._wails.dispatchWailsEvent({ name, data });
}

/** The backend's own state, so the fake answers the way the real one would. */
const backend = {
  joined: true,
  serverUrl: "https://work.jevido.app",
  tabs: [
    { id: "tab-a", name: "work", dir: "/home/jevido/Projects/work", bound: true },
    // Unbound, which is what a tab a colleague made looks like here.
    { id: "tab-b", name: "planning", dir: "", bound: false },
  ],
  activeTab: "tab-a",
  /** True while a run is in flight, which is when ActivateTab is refused. */
  running: false,
  status: {
    joined: true,
    state: "online",
    pending: 0,
    dropped: 0,
    cursor: 12,
    head: 12,
    error: "",
  } as Record<string, unknown>,
};

function view() {
  if (!backend.joined) return null;
  return {
    serverUrl: backend.serverUrl,
    id: "ws_0123456789abcdef0123456789abcdef",
    name: "jevido/work",
    actor: "d-abc",
    tabs: backend.tabs.map((t) => ({ id: t.id, name: t.name, dir: t.dir, bound: t.bound })),
    activeTab: backend.activeTab,
  };
}

function announce() {
  push("workspace:changed", { workspace: view(), status: backend.status });
}

setTransport({
  async call(objectID: number, _method: number, _win: string, args: any) {
    if (objectID !== 0) return null;
    const { methodID, args: a = [] } = args ?? {};
    calls.push({ id: methodID, args: a });

    switch (methodID) {
      case GET_CONFIG_PATH:
        return "/home/jevido/agents";
      case AGENTS:
      case RELOAD_AGENTS:
        return fixture;
      case PERMISSIONS:
        return {
          mode: "acceptEdits",
          choices: [
            { id: "plan", label: "Read only", detail: "Agents may look and not touch." },
            { id: "acceptEdits", label: "Edit files", detail: "Agents may write files." },
          ],
          bypassAccepted: true,
          acceptCommand: "claude --dangerously-skip-permissions",
        };
      case BOARD:
      case CHANGES:
        return null;
      case VERSION:
        return "dev";

      case WORKSPACES:
        return true;
      case WORKSPACE:
        return view();
      case SYNC_STATUS:
        return backend.status;
      case WORKSPACE_KEYS:
        return { writeKey: "wk_" + "f".repeat(32), readKey: "rk_" + "e".repeat(32) };
      case SYNC_NOW:
        return null;

      case JOIN_WORKSPACE: {
        const [, key] = a;
        // The real one checks access against the server and refuses a read
        // key. It should never get here -- the store refuses one first -- so
        // this is what makes "never got here" a thing the driver can prove.
        if (String(key).startsWith("rk_")) {
          throw new Error("workbench: that key has read access; joining needs a write key");
        }
        if (String(key) === "wk_refused") {
          throw new Error("workbench: join workspace: unauthorized: unknown or expired key");
        }
        backend.joined = true;
        backend.status = { ...backend.status, joined: true, state: "online", error: "" };
        announce();
        return view();
      }
      case CREATE_WORKSPACE:
        backend.joined = true;
        announce();
        return view();
      case LEAVE_WORKSPACE:
        backend.joined = false;
        backend.status = { joined: false, pending: 0, cursor: 0, head: 0 };
        announce();
        return null;

      case NEW_TAB: {
        const tab = { id: `tab-${backend.tabs.length + 1}`, name: String(a[0]), dir: "", bound: false };
        backend.tabs = [...backend.tabs, tab];
        announce();
        return { id: tab.id, name: tab.name, dir: "", bound: false };
      }
      case CLOSE_TAB:
        backend.tabs = backend.tabs.filter((t) => t.id !== a[0]);
        if (backend.activeTab === a[0]) backend.activeTab = "";
        announce();
        return null;
      case ACTIVATE_TAB: {
        const tab = backend.tabs.find((t) => t.id === a[0]);
        if (backend.running) throw new Error("workbench: stop the run before switching tabs");
        if (!tab) throw new Error(`workbench: unknown tab ${JSON.stringify(a[0])}`);
        if (!tab.bound) {
          throw new Error(`workbench: tab ${JSON.stringify(a[0])} has no folder on this machine`);
        }
        backend.activeTab = tab.id;
        announce();
        return null;
      }
      case BIND_TAB_FOLDER: {
        const tab = backend.tabs.find((t) => t.id === a[0]);
        if (!tab) return "";
        tab.dir = "/home/jevido/Projects/planning";
        tab.bound = true;
        announce();
        return tab.dir;
      }
    }
    return null;
  },
} as any);

(window as any).__verify = {
  calls,
  requests,
  backend,

  /** Draw calls the engine has been asked to make on this canvas. */
  paints(canvas: HTMLCanvasElement): number {
    return drawn.get(canvas)?.ops ?? 0;
  },

  /** A sync status change, exactly as the Go side pushes one. */
  sync(patch: Record<string, unknown>) {
    backend.status = { ...backend.status, ...patch };
    push("workspace:sync", { status: backend.status });
  },

  /**
   * Claude proposing a restructuring.
   *
   * Pushed as a `claude:tool` call, which is exactly how one arrives for real
   * -- there is no proposal event and no proposal endpoint, so a harness that
   * called the store directly would be testing a path the app does not have.
   * The input is stringified because that is how the backend relays tool
   * input.
   */
  propose(input: unknown) {
    push("claude:tool", {
      toolName: "propose_restructure",
      toolId: `tool-${Math.random().toString(16).slice(2)}`,
      toolInput: typeof input === "string" ? input : JSON.stringify(input),
    });
  },

  /** A run in flight, which is when ActivateTab is refused. */
  setRunning(running: boolean) {
    backend.running = running;
  },

  announce,
};

mount(App, { target: document.getElementById("app")! });

// Dynamic so it runs after the transport is installed and App is mounted.
void import("./drive-workspace");
