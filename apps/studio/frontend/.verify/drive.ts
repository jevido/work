/**
 * Drives the office and the desk panel with real events, and reports what
 * actually happened.
 *
 * This used to drive the profile *form* at an agent's desk. There is no form
 * any more: an agent is a folder on disk, so the panel shows a read-only view
 * of what the folder says and points at the editor. The two bugs the form
 * checks were written for are not about editing, though, and both still apply
 * to the button and the view that replaced it:
 *
 *   1. the header is a drag handle, so a press on a button inside it could be
 *      swallowed and drag the panel instead;
 *   2. the panel's "the desk changed" effect read a node that only exists
 *      while the thread is showing, so opening the profile unmounted it,
 *      re-ran the effect and closed the profile again -- on effect flush
 *      order, which is why it reproduced in WebKitGTK and not in Chromium.
 *
 * The config folder flow and the profile's own contents are driven by
 * drive-config.ts, which mounts the same app over stubbed config calls.
 */
import fixture from "./agents.json";

const results: { name: string; pass: boolean; detail: string }[] = [];
const V = () => (window as any).__verify;

function check(name: string, pass: boolean, detail = "") {
  results.push({ name, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"} ${name}${detail ? " -- " + detail : ""}`);
}

const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
async function settle(n = 12) { for (let i = 0; i < n; i++) await frame(); }

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]');
const nameEl = () => dialog()?.querySelector(".name")?.textContent?.trim() ?? null;
const profile = () => dialog()?.querySelector<HTMLElement>(".profile") ?? null;
const profileBtn = () => dialog()!.querySelector<HTMLButtonElement>('button[aria-label="Profile"]')!;

/**
 * Presses an element the way a mouse does: pointerdown first.
 *
 * The panel header is a drag handle, and a bare click() would skip the very
 * event that could swallow a press on a button sitting inside it.
 */
function realClick(el: HTMLElement) {
  const r = el.getBoundingClientRect();
  const at = { clientX: r.left + r.width / 2, clientY: r.top + r.height / 2 };
  const pointer = { bubbles: true, pointerId: 1, isPrimary: true, button: 0, buttons: 1, pointerType: "mouse", ...at } as any;
  // Dispatched on the deepest element under the pointer, which for the profile
  // button is the <path> in its icon -- exactly what a real press would land on.
  const target = (el.querySelector("svg path") as unknown as HTMLElement) ?? el;
  target.dispatchEvent(new PointerEvent("pointerdown", pointer));
  target.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0, buttons: 1, ...at }));
  target.dispatchEvent(new PointerEvent("pointerup", { ...pointer, buttons: 0 }));
  target.dispatchEvent(new MouseEvent("mouseup", { bubbles: true, button: 0, buttons: 0, ...at }));
  target.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0, ...at }));
}

function escape(el: HTMLElement) {
  el.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
}

async function run() {
  await settle(30);

  const canvas = document.querySelector("canvas") as HTMLCanvasElement | null;
  check("office canvas mounted", !!canvas);
  if (!canvas) return;
  check("the saved config folder was read before the office was drawn",
    V().calls.some((c: any) => c.id === 4273611995));
  check("roster loaded over bindings", V().calls.some((c: any) => c.id === 1687195856));
  check("no load error banner", !document.querySelector(".load-error"),
    document.querySelector(".load-error")?.textContent ?? "");

  const box = canvas.getBoundingClientRect();
  check("canvas has a size", box.width > 50 && box.height > 50,
    `${Math.round(box.width)}x${Math.round(box.height)} css`);

  // World -> screen, the same fit the renderer uses: scale to 1000x700, centred.
  const scale = Math.min(box.width / 1000, box.height / 700);
  const ox = (box.width - 1000 * scale) / 2;
  const oy = (box.height - 700 * scale) / 2;

  /** Clicks the monitor on an agent's desk, the way a pointer would. */
  async function clickDesk(a: any) {
    const cx = box.left + a.desk.x * scale + ox;
    const cy = box.top + (a.desk.y - 22) * scale + oy;
    const init = { bubbles: true, clientX: cx, clientY: cy } as any;
    canvas!.dispatchEvent(new PointerEvent("pointermove", init));
    canvas!.dispatchEvent(new MouseEvent("click", init));
    await settle(8);
  }

  const agents: any[] = fixture as any;
  const target = agents[0];
  const other = agents[1];

  canvas.dispatchEvent(new PointerEvent("pointermove", {
    bubbles: true,
    clientX: box.left + target.desk.x * scale + ox,
    clientY: box.top + (target.desk.y - 22) * scale + oy,
  } as any));
  await settle(2);
  check("pointer over an idle desk marks the canvas hot", canvas.classList.contains("hot"));

  await clickDesk(target);
  check("clicking an idle desk opens the panel", !!dialog(),
    `header = ${nameEl()}`);
  if (!dialog()) return;
  check("the panel opened on the desk that was clicked", nameEl() === target.name, `${nameEl()} vs ${target.name}`);
  check("the panel opens on the conversation", !!dialog()!.querySelector("textarea") && !profile());

  // Nothing in the panel writes an agent back any more, so everything below --
  // opening profiles, switching desks, reading both -- must not add a single
  // backend call to whatever the office already asked for on startup.
  const callsBefore = V().calls.length;

  // The reported bug: the header button appeared to do nothing.
  check("profile button present in header", !!dialog()!.querySelector('button[aria-label="Profile"]'));
  check("the form that used to be here is gone",
    !dialog()!.querySelector('button[aria-label="Edit profile"]') &&
      !dialog()!.querySelector("#agent-name"));

  const transformBefore = dialog()!.style.transform;
  realClick(profileBtn());
  await settle(2);
  check("pressing the profile button does not drag the panel",
    dialog()!.style.transform === transformBefore,
    `transform ${JSON.stringify(transformBefore)} -> ${JSON.stringify(dialog()!.style.transform)}`);
  check("the profile button opens the profile", !!profile());
  // The old failure only showed once effects flushed: the view mounted and was
  // torn straight back down. Waiting is the whole point of this check.
  await settle(40);
  check("the profile is still open after effects settle", !!profile(),
    profile() ? "" : "profile mounted then closed itself again");
  if (!profile()) return;

  // Read-only, and that is the point of it: the folder on disk is the only
  // place an agent is edited, so there is nothing here to type into or save.
  check("the profile has no fields to type into",
    !profile()!.querySelector("input") && !profile()!.querySelector("textarea"));
  check("the profile has no Save", !profile()!.querySelector('button[type="submit"]'));
  check("the conversation is replaced rather than covered",
    !dialog()!.querySelector(".scroller"));
  check("the header still says who this is, and stays pressed",
    nameEl() === target.name && profileBtn().getAttribute("aria-pressed") === "true");

  const chips = [...profile()!.querySelectorAll(".chip")].map((c) => c.textContent!.trim());
  check("it lists the skillset the roster carries",
    chips.length === (target.skillset ?? []).length &&
      chips.every((c, i) => c === target.skillset[i]),
    chips.join(" | "));
  check("it says the skillset is what routes work",
    (profile()!.textContent ?? "").includes("Anton routes work by these"));
  // The fixture has no personality, which is what a folder with no
  // PERSONALITY.md in it looks like: the panel has to name the file rather
  // than show a blank.
  check("an empty trait names the file it comes from",
    (profile()!.textContent ?? "").includes("PERSONALITY.md"));
  check("it says where to edit them and how to pick it up",
    (profile()!.textContent ?? "").includes(V().configPath) &&
      (profile()!.textContent ?? "").includes("Reload config"),
    profile()!.querySelector(".path")?.textContent ?? "no path");

  // The header button is the way back out too.
  realClick(profileBtn());
  await settle(6);
  check("the profile button closes the profile again",
    !profile() && !!dialog()!.querySelector("textarea"));

  // Escape is the other way back, and must not throw the desk away with it.
  realClick(profileBtn());
  await settle(20);
  escape(profile()!);
  await settle(20);
  check("Escape goes back to the conversation rather than closing the desk",
    !!dialog() && !profile() && !!dialog()!.querySelector("textarea"));

  // Switching desks must not carry the profile over to someone else.
  realClick(profileBtn());
  await settle(20);
  check("the profile is open before switching desks", !!profile());
  await clickDesk(other);
  check("another desk swaps the panel over", nameEl() === other.name, String(nameEl()));
  check("the profile does not follow to another desk",
    !profile() && !!dialog()!.querySelector("textarea"));

  realClick(profileBtn());
  await settle(20);
  const otherChips = [...(profile()?.querySelectorAll(".chip") ?? [])].map((c) => c.textContent!.trim());
  check("the other desk's profile is the other agent's",
    otherChips.length === (other.skillset ?? []).length &&
      otherChips[0] === other.skillset[0],
    otherChips.join(" | "));

  const added = V().calls.slice(callsBefore);
  check("reading a profile calls nothing on the backend", added.length === 0,
    added.map((c: any) => c.id).join(" "));

  escape(profile()!);
  await settle(20);
  escape(dialog()!);
  await settle(10);
  check("Escape from the conversation closes the desk", !dialog());
  check("the office is still running afterwards", !!document.querySelector("canvas"));
}

run()
  .catch((err) => check("harness crashed", false, String(err?.stack ?? err)))
  .finally(() => {
    void fetch("http://127.0.0.1:7788/", {
      method: "POST", headers: { "content-type": "text/plain" }, body: JSON.stringify(results),
    });
  });
