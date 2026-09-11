/** TEMPORARY diagnostic. Logs when and where the canvas actually changes. */
import { WORLD_HEIGHT, WORLD_WIDTH } from "../src/lib/office/world";

const V = () => (window as any).__verify;
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const log: any[] = [];

const BOSS_DESK_X = 500;
const BOSS_DESK_Y = 140;
const BOSS_SEAT_Y = 208;
const CHAIR_STRIP = [BOSS_DESK_X + 40, BOSS_SEAT_Y - 22, 150, 46] as const;
const TRAY = [BOSS_DESK_X + 56, BOSS_DESK_Y - 48, 44, 26] as const;

async function run() {
  await wait(2000);
  const canvas = document.querySelector("canvas") as HTMLCanvasElement;
  const box = canvas.getBoundingClientRect();
  const scale = Math.min(box.width / WORLD_WIDTH, box.height / WORLD_HEIGHT);
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const ox = (box.width - WORLD_WIDTH * scale) / 2;
  const oy = (box.height - WORLD_HEIGHT * scale) / 2;
  const ctx = canvas.getContext("2d")!;

  log.push({ meta: { cw: canvas.width, ch: canvas.height, bw: box.width, bh: box.height, scale, dpr, ox, oy } });

  const dev = (a: readonly [number, number, number, number]) => [
    Math.round((a[0] * scale + ox) * dpr),
    Math.round((a[1] * scale + oy) * dpr),
    Math.max(1, Math.round(a[2] * scale * dpr)),
    Math.max(1, Math.round(a[3] * scale * dpr)),
  ] as const;

  log.push({ rects: { chairStripDev: dev(CHAIR_STRIP), trayDev: dev(TRAY) } });

  function patch(a: readonly [number, number, number, number]) {
    const d = dev(a);
    return ctx.getImageData(d[0], d[1], d[2], d[3]).data;
  }
  function differs(a: Uint8ClampedArray, b: Uint8ClampedArray) {
    let n = 0;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) n++;
    return n;
  }

  // Whole canvas baseline, for a change bounding box in world coords.
  const full = () => ctx.getImageData(0, 0, canvas.width, canvas.height).data;
  const base = full();
  const chairBefore = patch(CHAIR_STRIP);
  const trayBefore = patch(TRAY);

  function bbox(now: Uint8ClampedArray) {
    let minx = 1e9, miny = 1e9, maxx = -1, maxy = -1, n = 0;
    const w = canvas.width;
    for (let i = 0; i < now.length; i += 4) {
      if (now[i] !== base[i] || now[i + 1] !== base[i + 1] || now[i + 2] !== base[i + 2]) {
        const p = i / 4, x = p % w, y = (p / w) | 0;
        n++;
        if (x < minx) minx = x; if (x > maxx) maxx = x;
        if (y < miny) miny = y; if (y > maxy) maxy = y;
      }
    }
    if (maxx < 0) return null;
    const toWorld = (px: number, py: number) => [
      Math.round((px / dpr - ox) / scale),
      Math.round((py / dpr - oy) / scale),
    ];
    const a = toWorld(minx, miny), b = toWorld(maxx, maxy);
    return { px: n, world: [a[0], a[1], b[0], b[1]] };
  }

  const t0 = performance.now();
  const at = () => Math.round(performance.now() - t0);

  let phase = "idle";
  const sample = () => {
    log.push({
      t: at(),
      phase,
      chair: differs(patch(CHAIR_STRIP), chairBefore),
      tray: differs(patch(TRAY), trayBefore),
      bbox: bbox(full()),
    });
  };

  sample();
  phase = "assigned";
  V().assigned("jeff");
  await wait(1500);
  V().working("jeff");
  phase = "working";

  // 120 seconds of timeline at 1s, which is far past every current budget.
  for (let i = 0; i < 120; i++) {
    await wait(1000);
    sample();
    if (i === 40) { V().finished("jeff"); phase = "finished-jeff"; }
    if (i === 80) {
      V().assigned("anton", "synthesis");
      V().working("anton", "synthesis");
      phase = "anton-synthesis";
    }
    if (i === 90) { V().finished("anton", "synthesis"); phase = "anton-done"; }
  }
}

run()
  .catch((e) => log.push({ crash: String(e?.stack ?? e) }))
  .finally(() => {
    void fetch("http://127.0.0.1:7788/", {
      method: "POST",
      headers: { "content-type": "text/plain" },
      body: JSON.stringify(log),
    });
  });
