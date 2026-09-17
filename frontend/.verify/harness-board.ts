/**
 * Draws a board in the engine the app actually uses, and photographs it.
 *
 * .verify/map.ts answers where the boxes are; this answers what they look
 * like. Paint is the half of a canvas that a pure test cannot reach -- a
 * shadow, a tilt, a piece of tape, text that fits its paper -- and the only
 * honest way to check it is to look at one.
 *
 * No app around it on purpose. This is the renderer, a canvas and a scene
 * literal: a fault in the picture is a fault in lib/mindmap and nowhere else,
 * where the same shot taken through the whole workspace harness would have a
 * dozen other places to have gone wrong.
 *
 * Run it with `task verify:board`.
 */
import { MindmapRenderer } from "../src/lib/mindmap/renderer";
import type { MapRow } from "../src/lib/mindmap/layout";

/**
 * The viewer's light theme, read out of the viewer's own stylesheet.
 *
 * Imported rather than copied. A table of forty colours transcribed into a
 * harness is a table that is right the day it is written and wrong the first
 * time somebody adds a cluster -- which is exactly what happened to the first
 * version of this file, and what the photograph then showed was two notes
 * still drawn in the dark palette.
 *
 * The media query cannot do the work: the offscreen host reports a dark colour
 * scheme, so the block never matches. The declarations are lifted out of it and
 * set on the canvas, which is where readPalette looks.
 */
import sheet from "../../web/public/style.css?raw";

const LIGHT: Record<string, string> = Object.fromEntries(
  [...sheet.slice(sheet.indexOf("prefers-color-scheme: light")).matchAll(/(--map-[\w-]+)\s*:\s*([^;]+);/g)].map(
    (found) => [found[1], found[2].trim()],
  ),
);

function row(
  id: string,
  depth: number,
  parentId: string,
  text: string,
  fields: Record<string, unknown> = {},
): MapRow {
  return { node: { id, fields: { text, ...fields } }, depth, parentId };
}

/** The reference picture, as this document would hold it. */
const ROWS: MapRow[] = [
  row("root", 0, "", "Living product brain. Ideas, context, decisions, all in one place.", {
    icon: "bulb",
  }),

  row("who", 1, "root", "Who has the problem?", { icon: "people" }),
  row("who-a", 2, "who", "Early-stage founders", { region: "reg-people" }),
  row("who-b", 2, "who", "Product teams who need faster alignment", { region: "reg-people" }),
  row("who-c", 2, "who", "Solo builders and indie hackers", { region: "reg-people" }),

  row("feel", 1, "root", "What should it feel like?", { icon: "heart" }),
  row("feel-a", 2, "feel", "Calm, focused and spacious"),
  row("feel-b", 2, "feel", "Creative but structured"),
  row("feel-c", 2, "feel", "Like a thinking partner rather than a form to fill in"),

  row("win", 1, "root", "Why does it win?", { icon: "trophy" }),
  row("win-a", 2, "win", "Combines freeform thinking with guidance"),
  row("win-b", 2, "win", "Keeps the big picture in view"),
  row("win-c", 2, "win", "Turns ideas into action, not just notes"),
  row("win-d", 3, "win-c", "A line one step deeper, to check the third tier"),

  row("next", 1, "root", "What happens next?", { icon: "signpost" }),
  row("next-a", 2, "next", "Validate with real users"),
  row("next-b", 2, "next", "Turn insights into a roadmap"),

  row("risk", 1, "root", "Risk: the map becomes cluttered", { icon: "warning" }),
  row("decided", 1, "root", "Decision: Claude proposes edits, a person approves", {
    icon: "check",
  }),

];

const REGIONS: Record<string, string> = { "reg-people": "Who we are for" };

const app = document.getElementById("app")!;
const canvas = document.createElement("canvas");
app.append(canvas);

const renderer = new MindmapRenderer(
  canvas,
  () => ({
    rows: ROWS,
    regionOf: (id: string) => {
      const region = ROWS.find((r) => r.node.id === id)?.node.fields.region;
      return typeof region === "string" && REGIONS[region]
        ? { id: region, name: REGIONS[region] }
        : null;
    },
    tasksOf: (id: string) => (id === "next-a" ? 3 : 0),
    focused: () => "feel-b",
  }),
  () => {},
);
renderer.start();
renderer.resize();

/** A screenshot, taken by the WebKit host rather than the page. */
function shot(name: string): Promise<void> {
  (window as never as { __shot: string | null }).__shot = name;
  return new Promise((resolve) => {
    const tick = setInterval(() => {
      if ((window as never as { __shot: string | null }).__shot === null) {
        clearInterval(tick);
        resolve();
      }
    }, 60);
  });
}
(window as never as { __shot: string | null }).__shot = null;

/** Two frames, so the one being photographed is the one that has been painted. */
function settled(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

/**
 * A pointer event the renderer will believe.
 *
 * Coordinates are in CSS pixels relative to the canvas, which is what the
 * renderer converts through its own pan and zoom -- so the harness works in
 * the same numbers a hand would.
 */
function point(type: string, x: number, y: number) {
  canvas.dispatchEvent(
    new PointerEvent(type, {
      pointerId: 1,
      clientX: x,
      clientY: y,
      bubbles: true,
    }),
  );
}

/** Steps the frame loop enough times for a trailing branch to be mid-flight. */
function frames(n: number): Promise<void> {
  return new Promise((resolve) => {
    let left = n;
    const step = () => (left-- <= 0 ? resolve() : requestAnimationFrame(step));
    requestAnimationFrame(step);
  });
}

async function run() {
  await settled();
  await shot("board-dark");

  /*
   * A drag, caught in the middle of one.
   *
   * What the board looks like while somebody is holding a branch: whether the
   * children come, whether the curves stay attached, and whether the held note
   * reads as picked up. Where it ends up is the document's answer -- the drop
   * writes a place onto every card that moved -- and this stub does not write
   * anything down, so there is no after to photograph here.
   */
  {
    const head = renderer.screenPoint("who");
    if (head) {
      point("pointerdown", head.x, head.y);
      point("pointermove", head.x - 170, head.y + 190);
      // Part way through the catch-up rather than after it: settled, the
      // followers are exactly under the hand and the lag is invisible.
      await frames(4);
      await shot("drag-midway");
      point("pointermove", head.x - 170, head.y + 190);
      await frames(40);
      await shot("drag-settled");
      point("pointerup", head.x - 170, head.y + 190);
      await frames(5);
    }
  }

  // The viewer's theme, applied where the renderer reads it from.
  for (const [name, value] of Object.entries(LIGHT)) canvas.style.setProperty(name, value);
  renderer.resize();
  await settled();
  await shot("board-light");

  console.log("board harness done");
}

void run();
