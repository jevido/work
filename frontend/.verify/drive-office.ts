/**
 * Drives one work handoff through the real renderer and checks the pixels.
 *
 * What is checked here is deliberately narrow, because .verify/sequences.ts
 * already owns the behaviour: it steps the simulation directly and can read
 * every agent, so "does the folder come back" is settled there. This is the
 * half a simulation with no canvas cannot answer.
 *
 * Two things in this change are drawn outside any rectangle the renderer used
 * to repaint: the coordinator's chair, which travels the width of his desk,
 * and the folders that stack on his in-tray. Everything else -- a folder in a
 * hand, a phone at an ear, arms up after a rally -- is inside the box the
 * dirty-rect pass already marks for an agent, so it cannot paint late and it
 * cannot smear. The chair and the tray can, in both directions:
 *
 *   1. they have to paint at all, which means their rectangle is collected;
 *   2. they have to leave nothing behind when they go, which means it is the
 *      right rectangle. A missing one shows as a smear that nothing ever
 *      cleans up, and that is only visible on a screen.
 *
 * Real time, because the walks are: crossing the bullpen takes what it takes.
 */
import { WORLD_HEIGHT, WORLD_WIDTH } from "../src/lib/office/world";

const results: { name: string; pass: boolean; detail: string }[] = [];
const V = () => (window as any).__verify;

function check(name: string, pass: boolean, detail = "") {
  results.push({ name, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"} ${name}${detail ? " -- " + detail : ""}`);
}

const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
async function settle(n = 12) {
  for (let i = 0; i < n; i++) await frame();
}
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Anton's desk, from the fixture the harness serves. */
const BOSS_DESK_X = 500;
const BOSS_DESK_Y = 140;
const BOSS_SEAT_Y = 208;

/**
 * The strip the chair travels along, and the corner of the desk top the
 * returned folders stack on.
 *
 * The tray patch is above the desk surface's middle and well clear of the
 * seat, so nobody standing at the desk is drawn into it -- which is what lets
 * it be compared against the opening frame at all.
 */
const CHAIR_STRIP = [BOSS_DESK_X + 40, BOSS_SEAT_Y - 22, 150, 46] as const;
const TRAY = [BOSS_DESK_X + 56, BOSS_DESK_Y - 48, 44, 26] as const;

/**
 * How much disagreement counts as "something is drawn there".
 *
 * Both patches are hundreds of pixels, so a handful of bytes is antialiasing
 * on the edge of a shape that was already there and a few hundred is a shape
 * that was not.
 */
const PAINTED = 400;
const CLEAN = 120;

async function run() {
  await settle(40);

  const canvas = document.querySelector("canvas") as HTMLCanvasElement | null;
  check("office canvas mounted", !!canvas);
  if (!canvas) return;

  check(
    "the engine is not asking for less movement",
    V().reduced() === false,
    "every sequence is switched off when it is, so nothing below would run",
  );

  const box = canvas.getBoundingClientRect();
  const scale = Math.min(box.width / WORLD_WIDTH, box.height / WORLD_HEIGHT);
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const ox = (box.width - WORLD_WIDTH * scale) / 2;
  const oy = (box.height - WORLD_HEIGHT * scale) / 2;
  const ctx = canvas.getContext("2d")!;

  /** One patch of the canvas, in world coordinates, as raw pixels. */
  function patch(area: readonly [number, number, number, number]): Uint8ClampedArray {
    const [x, y, w, h] = area;
    return ctx.getImageData(
      Math.round((x * scale + ox) * dpr),
      Math.round((y * scale + oy) * dpr),
      Math.max(1, Math.round(w * scale * dpr)),
      Math.max(1, Math.round(h * scale * dpr)),
    ).data;
  }

  /** How many bytes of two patches of the same size disagree. */
  function differs(a: Uint8ClampedArray, b: Uint8ClampedArray): number {
    let n = 0;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) n++;
    return n;
  }

  /** Watches a patch until it has moved far enough from a baseline, or gives up. */
  async function until(
    area: readonly [number, number, number, number],
    baseline: Uint8ClampedArray,
    want: number,
    seconds: number,
  ): Promise<number> {
    let best = 0;
    for (let i = 0; i < seconds * 4; i++) {
      await wait(250);
      best = Math.max(best, differs(patch(area), baseline));
      if (best > want) return best;
    }
    return best;
  }

  /** The reverse: waits for a patch to come back to a baseline. */
  async function backTo(
    area: readonly [number, number, number, number],
    baseline: Uint8ClampedArray,
    want: number,
    seconds: number,
  ): Promise<number> {
    let now = differs(patch(area), baseline);
    for (let i = 0; i < seconds * 2 && now > want; i++) {
      await wait(500);
      now = differs(patch(area), baseline);
    }
    return now;
  }

  const chairBefore = patch(CHAIR_STRIP);
  const trayBefore = patch(TRAY);
  await V().shot("office-idle");

  // A specialist is given work, and Anton takes a folder over in person. The
  // backend's own events keep arriving while he walks, exactly as they do in
  // the app: the CLI starts producing output long before he gets there.
  V().assigned("jeff");
  await wait(1500);
  await V().shot("office-handoff");
  V().working("jeff");

  const chairOut = await until(CHAIR_STRIP, chairBefore, PAINTED, 20);
  check("the chair is drawn away from his working seat", chairOut > PAINTED, `${chairOut} bytes`);
  await V().shot("office-waiting");

  // Task done: the finished flourish is the scribble beat, then the walk back.
  V().finished("jeff");
  const trayFull = await until(TRAY, trayBefore, CLEAN, 30);
  check("a returned folder is drawn on the in-tray", trayFull > CLEAN, `${trayFull} bytes`);
  await V().shot("office-returned");

  // His own turn. The chair comes home and the tray empties: he is reading them.
  V().assigned("anton", "synthesis");
  V().working("anton", "synthesis");
  await wait(3000);
  V().finished("anton", "synthesis");

  // The smear check. Both patches should be background again, so anything
  // painted outside a dirty rectangle has nothing left to erase it and shows
  // up right here. A tolerance rather than equality, because agents stroll
  // through the strip and the tray sits on a desk somebody sits at.
  const chairHome = await backTo(CHAIR_STRIP, chairBefore, PAINTED, 25);
  check(
    "the chair leaves nothing behind on its way back",
    chairHome <= PAINTED,
    `${chairHome} bytes still differ from the opening frame`,
  );

  const trayClear = await backTo(TRAY, trayBefore, CLEAN, 25);
  check(
    "the in-tray is clear again once he has read them",
    trayClear <= CLEAN,
    `${trayClear} bytes still differ from the opening frame`,
  );

  await V().shot("office-settled");
  check("the office is still running afterwards", !!document.querySelector("canvas"));
}

run()
  .catch((err) => check("harness crashed", false, String(err?.stack ?? err)))
  .finally(() => {
    void fetch("http://127.0.0.1:7788/", {
      method: "POST",
      headers: { "content-type": "text/plain" },
      body: JSON.stringify(results),
    });
  });
