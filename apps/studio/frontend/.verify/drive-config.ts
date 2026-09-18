/** Drives the config setup screen and the settings menu, and reports. */
import fixture from "./agents.json";

const results: { name: string; pass: boolean; detail: string }[] = [];
const V = () => (window as any).__verify;
const C = () => (window as any).__config;

function check(name: string, pass: boolean, detail = "") {
  results.push({ name, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"} ${name}${detail ? " -- " + detail : ""}`);
}

const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
async function settle(n = 12) { for (let i = 0; i < n; i++) await frame(); }

const setup = () => document.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');
const panel = () => document.querySelector<HTMLElement>('.dialog[role="dialog"]');
const wrench = () => document.querySelector<HTMLButtonElement>('button[aria-label="Settings"]');
const menu = () => document.querySelector<HTMLElement>('[role="menu"]');
const items = () => [...(menu()?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])];
const item = (text: string) => items().find((b) => (b.textContent ?? "").includes(text));
const primary = (root: HTMLElement) => root.querySelector<HTMLButtonElement>("button.primary")!;

function press(el: HTMLElement) {
  const r = el.getBoundingClientRect();
  const at = { clientX: r.left + r.width / 2, clientY: r.top + r.height / 2 };
  const pointer = { bubbles: true, pointerId: 1, isPrimary: true, button: 0, buttons: 1, pointerType: "mouse", ...at } as any;
  const target = (el.querySelector("svg path") as unknown as HTMLElement) ?? el;
  target.dispatchEvent(new PointerEvent("pointerdown", pointer));
  target.dispatchEvent(new PointerEvent("pointerup", { ...pointer, buttons: 0 }));
  target.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0, ...at }));
}

function escape(el: HTMLElement) {
  el.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
}

/** A folder saved before the window opened: no setup screen, no flash. */
async function runSaved(path: string) {
  await settle(25);
  check("a saved folder opens straight into the app", !!document.querySelector("canvas"));
  check("the setup screen is never shown", !setup());
  check("the folder was read, not asked for",
    C().calls.includes("GetConfigPath") && !C().calls.includes("SelectConfigFolder"),
    C().calls.join(" "));
  press(wrench()!);
  await settle(6);
  check("the menu names the saved folder", (menu()?.textContent ?? "").includes(path),
    menu()?.querySelector(".path")?.getAttribute("title") ?? "");
}

async function run() {
  const params = new URL(location.href).searchParams;
  // ?shot leaves the app alone so a screenshot catches a state rather than
  // whatever the middle of a driven flow looks like.
  if (params.has("shot")) return;
  const saved = params.get("saved");
  if (saved) return runSaved(saved);

  await settle(20);

  // --- 1. Nothing saved: the setup screen has the window ------------------
  check("no config folder means the setup screen", !!setup());
  check("the office is not drawn behind it", !document.querySelector("canvas"));
  check("neither is the board or the console",
    !document.querySelector("aside") && !document.querySelector(".work"));
  check("GetConfigPath was asked before anything was drawn",
    C().calls.includes("GetConfigPath"), C().calls.join(" "));
  check("the roster was not read without a folder",
    !V().calls.some((c: any) => c.id === 1687195856));
  if (!setup()) return;

  const choose = primary(setup()!);
  check("the setup screen offers a folder picker",
    (choose.textContent ?? "").includes("Choose folder"), (choose.textContent ?? "").trim());

  // --- 2. Picking a folder, then confirming it ---------------------------
  press(choose);
  await settle(20);
  check("the picker was opened natively", C().calls.includes("SelectConfigFolder"));
  const shown = setup()?.querySelector(".path")?.textContent?.trim() ?? "";
  check("the chosen folder is shown before it is accepted",
    shown === "/home/jevido/agents", shown);
  check("the app is still behind the screen until it is accepted",
    !document.querySelector("canvas"));
  const accept = primary(setup()!);
  check("the primary action becomes accepting the folder",
    (accept.textContent ?? "").includes("Use this folder"), (accept.textContent ?? "").trim());

  press(accept);
  await settle(30);
  check("accepting the folder shows the app", !!document.querySelector("canvas"));
  check("the setup screen is gone", !setup());
  check("the roster is read once the folder is settled",
    V().calls.filter((c: any) => c.id === 1687195856).length === 1,
    `${V().calls.filter((c: any) => c.id === 1687195856).length} calls`);

  // --- 3. The settings menu ---------------------------------------------
  check("a settings control sits in the office", !!wrench());
  if (!wrench()) return;
  const w = wrench()!.getBoundingClientRect();
  const office = document.querySelector<HTMLElement>(".office")!.getBoundingClientRect();
  check("it is in the top-right of the office",
    office.right - w.right < 24 && w.top - office.top < 24,
    `${Math.round(office.right - w.right)}px from the right, ${Math.round(w.top - office.top)}px from the top`);

  press(wrench()!);
  await settle(6);
  check("it opens a menu", !!menu());
  check("the menu names the folder it acts on",
    (menu()?.textContent ?? "").includes("/home/jevido/agents"));
  check("the menu has exactly the two items", items().length === 2,
    items().map((b) => b.textContent?.trim()).join(" | "));
  check("neither item is disabled", items().every((b) => !b.disabled));
  check("the menu does not offer a way to make an agent",
    !/new|add|create/i.test(menu()?.textContent ?? ""), menu()?.textContent?.trim() ?? "");

  // --- 4. Reload ---------------------------------------------------------
  V().dropLast();
  const before = V().calls.filter((c: any) => c.id === 1687195856).length;
  press(item("Reload config")!);
  await settle(40);
  check("Reload config calls the backend", C().calls.includes("ReloadAgents"));
  check("Reload config re-reads the roster",
    V().calls.filter((c: any) => c.id === 1687195856).length === before + 1);
  check("the office redraws without the agent whose folder went",
    document.querySelectorAll("canvas").length === 1 && V().agentsNow().length === 2);
  check("the menu stays open and says what it found",
    !!menu() && (menu()?.textContent ?? "").includes("2 agents"),
    menu()?.querySelector(".hint")?.textContent?.trim() ?? "no note");

  // --- 5. Cancelling the picker changes nothing --------------------------
  C().picks("");
  const rostersBefore = V().calls.filter((c: any) => c.id === 1687195856).length;
  press(item("Change folder")!);
  await settle(30);
  check("a cancelled picker keeps the folder in use",
    C().pathNow() === "/home/jevido/agents", C().pathNow());
  check("a cancelled picker does not touch the team",
    V().calls.filter((c: any) => c.id === 1687195856).length === rostersBefore);
  check("a cancelled picker leaves the app up", !!document.querySelector("canvas") && !setup());

  // --- 6. Changing the folder reads the new team -------------------------
  C().picks("/home/jevido/other-agents");
  const reloadsBefore = C().calls.filter((c: string) => c === "ReloadAgents").length;
  press(item("Change folder")!);
  await settle(40);
  check("changing the folder reads the team from it",
    V().calls.filter((c: any) => c.id === 1687195856).length === rostersBefore + 1);
  // Picking a folder loads it on the backend, so a second scan of the same
  // folder here would be work nobody asked for.
  check("changing the folder does not rescan what the picker just scanned",
    C().calls.filter((c: string) => c === "ReloadAgents").length === reloadsBefore,
    C().calls.join(" "));
  check("the menu now names the new folder",
    (menu()?.textContent ?? "").includes("/home/jevido/other-agents"),
    menu()?.querySelector(".path")?.getAttribute("title") ?? "");

  escape(menu()!);
  await settle(6);
  check("Escape closes the menu", !menu());
  check("focus goes back to the control that opened it",
    document.activeElement === wrench());

  // --- 7. The desk panel is a read-only profile now ----------------------
  const canvas = document.querySelector("canvas") as HTMLCanvasElement;
  const box = canvas.getBoundingClientRect();
  const scale = Math.min(box.width / 1000, box.height / 700);
  const ox = (box.width - 1000 * scale) / 2;
  const oy = (box.height - 700 * scale) / 2;
  const agent: any = (fixture as any)[0];
  const init = {
    bubbles: true,
    clientX: box.left + agent.desk.x * scale + ox,
    clientY: box.top + (agent.desk.y - 22) * scale + oy,
  } as any;
  canvas.dispatchEvent(new PointerEvent("pointermove", init));
  canvas.dispatchEvent(new MouseEvent("click", init));
  await settle(10);
  check("clicking a desk still opens its panel", !!panel());
  if (!panel()) return;

  const profileBtn = panel()!.querySelector<HTMLButtonElement>('button[aria-label="Profile"]');
  check("the header offers the profile, not an edit form", !!profileBtn);
  check("the old edit button is gone",
    !panel()!.querySelector('button[aria-label="Edit profile"]'));
  if (!profileBtn) return;

  press(profileBtn);
  await settle(40);
  check("the profile opens and stays open", !!panel()!.querySelector(".profile"));
  check("it has no fields to type into",
    !panel()!.querySelector("input") && !panel()!.querySelector("textarea"),
    "read-only");
  check("it has no Save", !panel()!.querySelector('button[type="submit"]'));
  check("it shows what the folder says about them",
    (panel()!.textContent ?? "").includes(agent.skillset[0]), agent.skillset[0]);
  check("it says where to edit them and how to pick it up",
    (panel()!.textContent ?? "").includes("/home/jevido/other-agents") &&
      (panel()!.textContent ?? "").includes("Reload config"));

  escape(panel()!.querySelector(".profile")!);
  await settle(20);
  check("Escape goes back to the conversation rather than closing the desk",
    !!panel() && !panel()!.querySelector(".profile") && !!panel()!.querySelector("textarea"));
  escape(panel()!);
  await settle(10);
  check("Escape from the conversation closes the desk", !panel());
}

run()
  .catch((err) => check("harness crashed", false, String(err?.stack ?? err)))
  .finally(() => {
    void fetch("http://127.0.0.1:7788/", {
      method: "POST", headers: { "content-type": "text/plain" }, body: JSON.stringify(results),
    });
  });
