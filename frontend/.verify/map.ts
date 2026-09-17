/**
 * Checks the map's geometry without a canvas, a browser or a screenshot.
 *
 * `lib/mindmap/layout.ts` is pure on purpose -- it takes rows and a text
 * measurement and answers with rectangles -- and the questions worth asking of
 * it are questions about those rectangles. Do two notes overlap. Does a branch
 * stay in its own sector. Does the same tree come out the same way twice. None
 * of those need pixels, and answering them by looking at a picture is how a
 * layout ends up with nobody checking it at all.
 *
 * The screenshot harnesses next door still matter: they answer what the paint
 * looks like. This answers where things are.
 *
 * Run it with `task verify:map`.
 */
import {
  estimate,
  layout,
  tier,
  wrap,
  type Box,
  type MapRow,
} from "../src/lib/mindmap/layout";

interface Result {
  name: string;
  pass: boolean;
  detail: string;
}

const results: Result[] = [];

function check(name: string, pass: boolean, detail = ""): void {
  results.push({ name, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"} ${name}${detail ? " -- " + detail : ""}`);
}

/* -------------------------------------------------------------------------- */
/* Trees                                                                      */
/* -------------------------------------------------------------------------- */

function row(
  id: string,
  depth: number,
  parentId: string,
  text: string,
  fields: Record<string, unknown> = {},
): MapRow {
  return { node: { id, fields: { text, ...fields } }, depth, parentId };
}

/**
 * A board of the shape the design draws: a subject, four clusters, leaves.
 *
 * Built by hand rather than generated, because the interesting cases are the
 * uneven ones -- a cluster with one leaf next to a cluster with nine is exactly
 * where an even split of the circle falls apart.
 */
function board(): MapRow[] {
  const rows: MapRow[] = [row("root", 0, "", "Living product brain", { icon: "bulb" })];
  const clusters: [string, string, number][] = [
    ["who", "Who has the problem?", 3],
    ["feel", "What should it feel like?", 9],
    ["win", "Why does it win?", 1],
    ["next", "What happens next?", 4],
  ];
  for (const [id, text, leaves] of clusters) {
    rows.push(row(id, 1, "root", text, { icon: "people" }));
    for (let i = 0; i < leaves; i++) {
      rows.push(
        row(
          `${id}-${i}`,
          2,
          id,
          `A leaf with enough words on it that the wrapping has something to do, number ${i}`,
        ),
      );
    }
  }
  return rows;
}

/** A plain outline: several roots, no subject, three deep in places. */
function outline(): MapRow[] {
  const rows: MapRow[] = [];
  for (let r = 0; r < 6; r++) {
    rows.push(row(`r${r}`, 0, "", `Root number ${r}`));
    for (let c = 0; c < 3; c++) {
      rows.push(row(`r${r}c${c}`, 1, `r${r}`, `Child ${c} of root ${r}`));
      if (c === 1) {
        rows.push(row(`r${r}c${c}g`, 2, `r${r}c${c}`, `A grandchild under child ${c}`));
      }
    }
  }
  return rows;
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

/** Two rectangles, with a pixel of slack so touching is not overlapping. */
function overlaps(a: Box, b: Box): boolean {
  return (
    a.x < b.x + b.width - 1 &&
    b.x < a.x + a.width - 1 &&
    a.y < b.y + b.height - 1 &&
    b.y < a.y + a.height - 1
  );
}

function collisions(boxes: readonly Box[]): [Box, Box][] {
  const found: [Box, Box][] = [];
  for (let i = 0; i < boxes.length; i++) {
    for (let j = i + 1; j < boxes.length; j++) {
      if (overlaps(boxes[i], boxes[j])) found.push([boxes[i], boxes[j]]);
    }
  }
  return found;
}

/** Everything about a layout that has to be reproducible, as one string. */
function fingerprint(boxes: readonly Box[]): string {
  return boxes
    .map((b) => `${b.id}:${b.x.toFixed(4)},${b.y.toFixed(4)},${b.width}x${b.height}:${b.cluster}:${b.angle.toFixed(6)}`)
    .join("|");
}

/* -------------------------------------------------------------------------- */
/* Checks                                                                     */
/* -------------------------------------------------------------------------- */

{
  const empty = layout([]);
  check(
    `board: an empty document is an empty map`,
    empty.boxes.length === 0 && empty.branches.length === 0 && empty.width > 0 && empty.height > 0,
    `${empty.boxes.length} boxes, ${empty.width}x${empty.height}`,
  );

  for (const [what, rows] of [
    ["the board", board()],
    ["a plain outline", outline()],
  ] as [string, MapRow[]][]) {
    const made = layout(rows);

    check(
      `board: every line in ${what} is drawn`,
      made.boxes.length === rows.length && made.byId.size === rows.length,
      `${made.boxes.length} of ${rows.length}`,
    );

    check(
      `board: ${what} is entirely in positive map space`,
      made.boxes.every((b) => b.x >= 0 && b.y >= 0),
      made.boxes
        .filter((b) => b.x < 0 || b.y < 0)
        .map((b) => b.id)
        .join(", "),
    );

    check(
      `board: the extent of ${what} covers every box`,
      made.boxes.every((b) => b.x + b.width <= made.width && b.y + b.height <= made.height),
      `${made.width}x${made.height}`,
    );

    const twice = layout(rows);
    check(
      `board: ${what} laid out twice is the same layout`,
      fingerprint(made.boxes) === fingerprint(twice.boxes),
    );

    // Every branch of the tree has a line to draw it along, and nothing else
    // does. A branch to a box that is not on the map is a curve to nowhere.
    check(
      `board: every branch in ${what} joins two boxes that exist`,
      made.branches.every((b) => made.byId.has(b.from) && made.byId.has(b.to)),
    );

    const parented = rows.filter((r) => r.parentId !== "").length;
    check(
      `board: ${what} draws one branch per parented line`,
      made.branches.length === parented,
      `${made.branches.length} of ${parented}`,
    );
  }
}

for (const [what, rows] of [
  ["the board", board()],
  ["a plain outline", outline()],
] as [string, MapRow[]][]) {
  const made = layout(rows);
  const hits = collisions(made.boxes);
  check(
    `cluster: no two notes overlap on ${what}`,
    hits.length === 0,
    hits
      .slice(0, 4)
      .map(([a, b]) => `${a.id}/${b.id}`)
      .join(", "),
  );
}

/* The colour a note is drawn in is the branch it belongs to, and that has to
   hold for every line under a head -- otherwise a cluster is drawn in two
   colours and reads as two things. */
{
  const rows = board();
  const made = layout(rows);
  const wrong: string[] = [];
  for (const r of rows) {
    if (r.parentId === "" || r.parentId === "root") continue;
    const mine = made.byId.get(r.node.id);
    const parent = made.byId.get(r.parentId);
    if (mine && parent && mine.cluster !== parent.cluster) wrong.push(r.node.id);
  }
  check("cluster: a line is in the same branch as its parent", wrong.length === 0, wrong.join(", "));

  const subject = made.byId.get("root");
  check("cluster: the subject belongs to no branch", subject?.cluster === -1, `${subject?.cluster}`);

  const heads = ["who", "feel", "win", "next"].map((id) => made.byId.get(id)?.cluster);
  check(
    "cluster: each head gets a branch of its own",
    new Set(heads).size === heads.length && heads.every((c) => typeof c === "number" && c >= 0),
    heads.join(", "),
  );

  check(
    "cluster: a glyph is carried onto the box that shows it",
    made.byId.get("root")?.icon === "bulb" && made.byId.get("who")?.icon === "people",
  );
  check(
    "cluster: a note with a glyph is taller than the same note without one",
    (made.byId.get("who")?.height ?? 0) > tier(1).height,
    `${made.byId.get("who")?.height} vs ${tier(1).height}`,
  );
}

/* Wrapping. The estimate is what runs here; the renderer's measurement is more
   accurate and the rules below hold for both. */
{
  const font = tier(2).font;
  const one = wrap("short", 220, font, estimate);
  check("wrap: a short line is one line", one.length === 1 && one[0] === "short", one.join(" / "));

  const many = wrap(
    "a sentence long enough that it cannot possibly fit on one line of a note this narrow " +
      "and goes on well past the point where anybody would still be reading it at a glance",
    220,
    font,
    estimate,
  );
  check("wrap: a long line is cut off rather than growing", many.length === 4, `${many.length}`);
  check("wrap: what is cut off says so", many[many.length - 1].endsWith("…"), many[many.length - 1]);

  const wide = wrap("Supercalifragilisticexpialidociousandthensome", 90, font, estimate);
  check(
    "wrap: a single word wider than the note is broken",
    wide.length > 1 && wide.every((line) => estimate(line, font) <= 90 - 24),
    wide.join(" / "),
  );

  check("wrap: an empty line wraps to nothing", wrap("   ", 220, font, estimate).length === 0);
}

/* A long line makes its note taller. Without this the text is drawn outside the
   paper it belongs to, which is the whole reason heights stopped being fixed. */
{
  const rows = [
    row("short", 0, "", "Short"),
    row(
      "long",
      0,
      "",
      "A line with a great many words on it, enough that it has to wrap onto several lines",
    ),
  ];
  const made = layout(rows);
  const a = made.byId.get("short");
  const b = made.byId.get("long");
  check(
    "a note grows to hold what is written on it",
    (b?.height ?? 0) > (a?.height ?? 0) && (b?.lines.length ?? 0) > 1,
    `${a?.height} vs ${b?.height}, ${b?.lines.length} lines`,
  );
}

/* The measurement is a callback, and a wider one has to produce a taller note.
   If it does not, the renderer's own measurement is being ignored. */
{
  // Short enough that the narrow measurement wraps it without hitting the
  // four-line ceiling -- past that both answers are four lines and the check
  // would pass on a layout that ignored the callback entirely.
  const rows = [row("one", 0, "", "Wraps differently at two widths")];
  const narrow = layout(rows, (text, font) => estimate(text, font) * 2);
  const wide = layout(rows, estimate);
  check(
    "the measurement handed in is the one used",
    (narrow.byId.get("one")?.height ?? 0) > (wide.byId.get("one")?.height ?? 0),
    `${narrow.byId.get("one")?.height} vs ${wide.byId.get("one")?.height}`,
  );
}

/* A card somebody dragged stays where they left it, and the cards nobody
   touched stay arranged around it. This is the whole of dragging: a board that
   re-derived every position would put it back on the next paint. */
{
  const rows = board();
  // The same row with two fields added, rather than a row of its own: a
  // different text is a different box, and a check that moved one card would
  // be measuring the wrapping instead.
  const withFields = (id: string, fields: Record<string, unknown>) =>
    rows.map((r) =>
      r.node.id === id ? { ...r, node: { ...r.node, fields: { ...r.node.fields, ...fields } } } : r,
    );
  const placed = withFields("feel", { x: -900, y: -640 });
  const before = layout(rows);
  const after = layout(placed);

  const moved = after.byId.get("feel");
  const anchorBox = after.byId.get("win");
  const wasAnchor = before.byId.get("win");
  const wasMoved = before.byId.get("feel");
  check(
    "a placed card is not where the layout would have put it",
    !!moved && !!wasMoved && (moved.x !== wasMoved.x || moved.y !== wasMoved.y),
    `${wasMoved?.x},${wasMoved?.y} -> ${moved?.x},${moved?.y}`,
  );

  // Everything is shifted into positive space together, so the distance
  // between two cards nobody placed is the same as it was.
  const other = before.byId.get("next");
  const otherAfter = after.byId.get("next");
  const gapBefore = wasAnchor && other ? other.x - wasAnchor.x : NaN;
  const gapAfter = anchorBox && otherAfter ? otherAfter.x - anchorBox.x : NaN;
  check(
    "the cards nobody moved keep their arrangement",
    Math.abs(gapBefore - gapAfter) < 0.5,
    `${gapBefore} vs ${gapAfter}`,
  );

  check(
    "a board with a card dragged off to the left is still entirely on the map",
    after.boxes.every((b) => b.x >= 0 && b.y >= 0),
    after.boxes
      .filter((b) => b.x < 0 || b.y < 0)
      .map((b) => b.id)
      .join(", "),
  );

  check(
    "the extent covers the card that was dragged out",
    after.boxes.every((b) => b.x + b.width <= after.width && b.y + b.height <= after.height),
    `${after.width}x${after.height}`,
  );

  // Half a coordinate is not a place, and a NaN is nowhere at all.
  const broken = layout(withFields("feel", { x: 40 }));
  check(
    "a card with half a coordinate is laid out as though it had none",
    fingerprint(broken.boxes) === fingerprint(before.boxes),
  );
}

const failed = results.filter((r) => !r.pass);
console.log(`\n${results.length - failed.length}/${results.length} passed`);
// Thrown rather than exited, so the task fails without this file needing node's
// type definitions -- which the frontend does not otherwise carry.
if (failed.length > 0) {
  throw new Error(`map layout: ${failed.map((r) => r.name).join("; ")}`);
}
