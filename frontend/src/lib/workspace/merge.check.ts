/**
 * A check on the merge, because it is a port.
 *
 * ./ops.ts reimplements dev.jevido/work/internal/ops in TypeScript so that a
 * browser can replay an op log -- see the note at the top of that file for
 * why there are two implementations at all. Two implementations of a merge
 * that disagree show two different documents for the same log, and the
 * disagreement would surface as somebody's line quietly missing rather than
 * as anything that looks like a bug. So the properties the Go package
 * promises are asserted here: order independence, idempotence, the
 * last-write-wins tiebreak, delete beating an edit in either direction, and
 * what happens to a node whose parent went away.
 *
 * Run it, from frontend/:
 *
 *   npx rolldown src/lib/workspace/merge.check.ts -d node_modules/.cache/merge \
 *     --format esm --platform node \
 *     && node node_modules/.cache/merge/merge.check.js
 *
 * Plain assertions and no test framework: the frontend has no JavaScript test
 * runner and one merge is not worth adding one for. It is bundled with the
 * rolldown that Vite already brings, so it costs no dependency either. Worth
 * a `check:merge` script in package.json and a line in the Taskfile's `check`
 * -- neither of which is this branch's file to edit.
 */
import { afterIndex, atEnd, atStart, keyFor, stepped, type Placed } from "./bounds";
import { State, type Op } from "./ops";
import { between } from "./position";
import { outlineRows, planTasks } from "./model";

let n = 0;
const ok = (label: string, cond: boolean) => {
  n++;
  if (!cond) throw new Error(`FAIL: ${label}`);
};

/** A list of siblings as a readable string, for the failure message. */
const order = (list: readonly { position: string }[]) => list.map((p) => p.position).join(" ");

// --- positions ---------------------------------------------------------
//
// The property that matters is not just "ordered" but "stays short". A key is
// capped at 256 bytes by the protocol, and the first version of this grew one
// character per five appends -- so writing an outline top to bottom broke it
// somewhere past a thousand lines, as an op the server refused.

{
  const cap = 256;

  // Appending, which is how an outline is written.
  let key = between("", "");
  let longest = key.length;
  const appended: string[] = [key];
  for (let i = 0; i < 20000; i++) {
    key = between(key, "");
    longest = Math.max(longest, key.length);
    appended.push(key);
  }
  ok(`20000 appends stay under the cap (longest ${longest})`, longest < cap);
  ok("20000 appends stay short", longest <= 6);
  ok("appends are strictly ascending", appended.every((k, i) => i === 0 || k > appended[i - 1]));

  // Prepending, the same in the other direction.
  let head = between("", "");
  let headLongest = head.length;
  const prepended: string[] = [head];
  for (let i = 0; i < 20000; i++) {
    head = between("", head);
    headLongest = Math.max(headLongest, head.length);
    prepended.push(head);
  }
  ok(`20000 prepends stay under the cap (longest ${headLongest})`, headLongest < cap);
  ok("prepends are strictly descending", prepended.every((k, i) => i === 0 || k < prepended[i - 1]));

  // The worst case for the fractional part: always inserting between the same
  // two neighbours. This one has to grow, but only about a character per six.
  let lo = between("", "");
  let hi = between(lo, "");
  let midLongest = 0;
  for (let i = 0; i < 1000; i++) {
    const mid = between(lo, hi);
    ok(`insert ${i} lands between`, lo < mid && mid < hi);
    midLongest = Math.max(midLongest, mid.length);
    hi = mid;
  }
  ok(`1000 inserts at one spot stay under the cap (longest ${midLongest})`, midLongest < cap);

  // A list built by random insertion anywhere stays correctly ordered and
  // short, which is the realistic mix.
  let list = [between("", "")];
  let anyLongest = 0;
  for (let i = 0; i < 3000; i++) {
    const at = Math.floor(Math.random() * (list.length + 1));
    const b4 = at === 0 ? "" : list[at - 1];
    const af = at === list.length ? "" : list[at];
    const mid = between(b4, af);
    anyLongest = Math.max(anyLongest, mid.length);
    list.splice(at, 0, mid);
  }
  ok(`3000 random inserts stay under the cap (longest ${anyLongest})`, anyLongest < cap);
  ok("random inserts keep the list ordered", list.every((k, i) => i === 0 || k > list[i - 1]));

  // Keys from another implementation must not throw, and must still land in
  // the right place.
  for (const [b4, af] of [["V", "m"], ["zzz", ""], ["", "0001"], ["!!", "~~"], ["", ""]]) {
    const mid = between(b4, af);
    ok(`foreign keys (${b4 || "start"}, ${af || "end"}) produce something`, mid.length > 0);
    if (b4 && /^[0-9A-Za-z]+$/.test(b4)) ok(`foreign key stays above ${b4}`, mid > b4);
  }
}

let keys: string[] = [between("", "")];
for (let i = 0; i < 200; i++) keys.push(between(keys.at(-1)!, ""));
ok("append stays ordered", keys.every((k, i) => i === 0 || k > keys[i - 1]));

// Repeatedly insert between the first two, the classic degenerate case.
let a = between("", "");
let b = between(a, "");
for (let i = 0; i < 200; i++) {
  const mid = between(a, b);
  ok(`midpoint ${i} is between`, a < mid && mid < b);
  b = mid;
}

// Prepend repeatedly.
let head = between("", "");
for (let i = 0; i < 100; i++) {
  const before = between("", head);
  ok(`prepend ${i}`, before < head);
  head = before;
}

// --- where a node lands --------------------------------------------------
//
// The list operations, driven the way the app drives them, checked by reading
// the resulting order back. Written after `outdent` shipped with a node id
// where a sort key belonged -- a three-element tuple destructured as two,
// which type-checks and produces a position that is merely strange.

/** A list of siblings, as the merge keeps them: ascending by position. */
type List = Placed[];

const sorted = (list: List): List =>
  [...list].sort((x, y) => (x.position < y.position ? -1 : x.position > y.position ? 1 : 0));

/** Appends `n` items the way the app appends: one at a time, at the end. */
function build(n: number): List {
  let list: List = [];
  for (let i = 0; i < n; i++) list = [...list, { position: keyFor(atEnd(list)) }];
  return list;
}

const insert = (list: List, position: string): List => sorted([...list, { position }]);
const relocate = (list: List, from: number, position: string): List =>
  sorted(list.map((p, i) => (i === from ? { position } : p)));

{
  const list = build(2);
  const [one, two] = list.map((p) => p.position);

  const startKey = keyFor(atStart(list));
  ok("atStart lands first", order(insert(list, startKey)) === [startKey, one, two].join(" "));

  const endKey = keyFor(atEnd(list));
  ok("atEnd lands last", order(insert(list, endKey)) === [one, two, endKey].join(" "));

  const midKey = keyFor(afterIndex(list, 0));
  ok("afterIndex lands between", order(insert(list, midKey)) === [one, midKey, two].join(" "));

  const goneKey = keyFor(afterIndex(list, 9));
  ok(
    "afterIndex past the end lands last",
    order(insert(list, goneKey)) === [one, two, goneKey].join(" "),
  );
}

// Every single-step move from every position in a list of four, checked
// against the swap it is meant to be. Four so that "the gap on the far side
// of the neighbour being stepped over" has somewhere to be wrong.
{
  const labels = ["a", "b", "c", "d"];
  const list = build(labels.length);
  const named = new Map(list.map((p, i) => [p.position, labels[i]]));
  const read = (l: List) => l.map((p) => named.get(p.position) ?? "?").join("");
  ok("built in order", read(list) === "abcd");

  for (let i = 0; i < labels.length; i++) {
    for (const delta of [-1, 1] as const) {
      const bounds = stepped(list, i, delta);
      const target = i + delta;
      if (target < 0 || target >= labels.length) {
        ok(`${labels[i]} cannot step ${delta < 0 ? "up" : "down"} off the end`, bounds === null);
        continue;
      }
      const key = keyFor(bounds!);
      named.set(key, labels[i]);
      const want = [...labels];
      [want[i], want[target]] = [want[target], want[i]];
      ok(
        `${labels[i]} steps ${delta < 0 ? "up" : "down"} to ${want.join("")}`,
        read(relocate(list, i, key)) === want.join(""),
      );
    }
  }
}

// A hundred random single-step moves in a row. A rule that only breaks once
// the keys have been subdivided a few times does not show up in one move.
{
  let list = build(6);
  const tag = new Map(list.map((p, i) => [p.position, String(i)]));
  const read = (l: List) => l.map((p) => tag.get(p.position) ?? "?").join("");

  for (let n = 0; n < 100; n++) {
    const i = Math.floor(Math.random() * list.length);
    const delta: -1 | 1 = Math.random() < 0.5 ? -1 : 1;
    const bounds = stepped(list, i, delta);
    if (!bounds) continue;

    const before = read(list);
    const key = keyFor(bounds);
    tag.set(key, before[i]);
    list = relocate(list, i, key);

    const want = [...before];
    [want[i], want[i + delta]] = [want[i + delta], want[i]];
    ok(`random step ${n}: ${before} -> ${want.join("")}`, read(list) === want.join(""));
    ok(`random step ${n} loses nobody`, new Set(read(list)).size === 6);
  }
}

// --- merge --------------------------------------------------------------
const op = (o: Partial<Op> & { id: string; kind: Op["kind"]; node: string }): Op => ({
  actor: "a",
  clock: 1,
  ...o,
} as Op);

// Order independence: apply a set of ops in several orders, compare trees.
const ops: Op[] = [
  op({ id: "1", kind: "create-node", node: "r", clock: 1, position: "V", fields: { text: "root" } }),
  op({ id: "2", kind: "create-node", node: "c1", parent: "r", clock: 2, position: "V", fields: { text: "one" } }),
  op({ id: "3", kind: "create-node", node: "c2", parent: "r", clock: 3, position: "m", fields: { text: "two" } }),
  op({ id: "4", kind: "set-fields", node: "c1", clock: 4, fields: { text: "ONE" } }),
  op({ id: "5", kind: "move-node", node: "c2", parent: "", clock: 5, position: "g" }),
  op({ id: "6", kind: "extract-to-task", node: "c1", task: "t1", clock: 6, position: "V", fields: { type: "task", text: "do one", status: "todo" } }),
];

function render(order: Op[]) {
  const s = new State();
  s.applyAll(order);
  return JSON.stringify(s.tree());
}
const base = render(ops);
for (let i = 0; i < 30; i++) {
  const shuffled = [...ops].sort(() => Math.random() - 0.5);
  ok(`order independent #${i}`, render(shuffled) === base);
}

// Idempotence.
const s1 = new State();
ok("applyAll counts new", s1.applyAll(ops) === ops.length);
ok("replay is a no-op", s1.applyAll(ops) === 0);
ok("replay same tree", JSON.stringify(s1.tree()) === base);

// Last-write-wins per field, decided by clock then actor.
const s2 = new State();
s2.applyAll([
  op({ id: "a1", kind: "create-node", node: "x", clock: 1, position: "V", fields: { text: "first" } }),
  { id: "a2", kind: "set-fields", node: "x", actor: "zz", clock: 5, fields: { text: "high clock" } },
  { id: "a3", kind: "set-fields", node: "x", actor: "aa", clock: 5, fields: { text: "same clock lower actor" } },
]);
ok("higher actor wins a clock tie", s2.node("x")!.fields.text === "high clock");

// Delete wins in either order.
for (const order of [["d", "e"], ["e", "d"]]) {
  const s = new State();
  const map: Record<string, Op> = {
    d: { id: `del-${order.join("")}`, kind: "delete-node", node: "y", actor: "a", clock: 2 },
    e: { id: `set-${order.join("")}`, kind: "set-fields", node: "y", actor: "a", clock: 9, fields: { text: "late" } },
  };
  s.applyAll([
    op({ id: `mk-${order.join("")}`, kind: "create-node", node: "y", clock: 1, position: "V", fields: { text: "y" } }),
    ...order.map((k) => map[k]),
  ]);
  ok(`delete wins (${order.join(",")})`, s.tree().length === 0 && s.node("y")!.deleted);
}

// Extract links both ends.
const task = s1.node("t1")!;
ok("task links back", task.fields.extractedFrom === "c1");
ok("idea links forward", s1.node("c1")!.fields.taskId === "t1");

// Views split the tree.
const tree = s1.tree();
ok("plan holds only tasks", planTasks(tree).every((t) => t.fields.type === "task"));
ok("outline excludes tasks", outlineRows(tree).every((r) => r.node.fields.type !== "task"));

// A child of a deleted parent is detached, not lost.
const s3 = new State();
s3.applyAll([
  op({ id: "p1", kind: "create-node", node: "p", clock: 1, position: "V", fields: { text: "p" } }),
  op({ id: "p2", kind: "create-node", node: "k", parent: "p", clock: 2, position: "V", fields: { text: "k" } }),
  op({ id: "p3", kind: "delete-node", node: "p", clock: 3 }),
]);
ok("tree drops the branch", s3.tree().length === 0);
ok("child is surfaced as detached", s3.detached().map((d) => d.id).join() === "k");

// Round-trip through storage keeps the stamps.
const restored = State.fromJSON(JSON.parse(JSON.stringify(s1.toJSON())));
ok("round-trip tree", JSON.stringify(restored.tree()) === base);
ok("round-trip clock", restored.clock === s1.clock);
ok("round-trip idempotence", restored.applyAll(ops) === 0);
const beaten = new State();
beaten.applyAll(ops);
const late: Op = { id: "late", kind: "set-fields", node: "c1", actor: "a", clock: 2, fields: { text: "stale" } };
restored.apply(late);
ok("restored stamps still beat a stale write", restored.node("c1")!.fields.text === "ONE");

// A cycle from concurrent moves is surfaced, not spun on.
const s4 = new State();
s4.applyAll([
  op({ id: "m1", kind: "create-node", node: "u", clock: 1, position: "V" }),
  op({ id: "m2", kind: "create-node", node: "v", clock: 2, position: "m" }),
  op({ id: "m3", kind: "move-node", node: "u", parent: "v", clock: 3, position: "V" }),
  op({ id: "m4", kind: "move-node", node: "v", parent: "u", clock: 4, position: "V" }),
]);
ok("cycle leaves an empty tree", s4.tree().length === 0);
ok("cycle nodes are detached", s4.detached().length === 2);

console.log(`ok — ${n} assertions`);
