/**
 * Photographs the two dialogs a card and a vocabulary open into.
 *
 * Against a stub document rather than a real Workspace. What is being checked
 * is the panel -- whether a card with four guidelines, three interested
 * parties, two captioned links and a paragraph of detail still fits, and what
 * it looks like when it holds none of those -- and none of that needs an op
 * log, a replica or a Wails bridge behind it. The stub is a plain object with
 * the handful of methods the two components call; a method they start calling
 * that is not here fails loudly the moment this is run.
 *
 * Run it with `task verify:cards`.
 */
import { mount, unmount } from "svelte";
import CardDialog from "../src/components/CardDialog.svelte";
import VocabularyDialog from "../src/components/VocabularyDialog.svelte";

interface Node {
  id: string;
  fields: Record<string, unknown>;
  children: Node[];
}

const CARDS: Node[] = [
  { id: "c1", fields: { text: "Founders lose the thread between sessions" }, children: [] },
  { id: "c2", fields: { text: "Turn insights into a roadmap" }, children: [] },
  { id: "c3", fields: { text: "Validate with real users" }, children: [] },
];

const GUIDELINES = new Map([
  ["g1", "improves performance"],
  ["g2", "improves market share"],
  ["g3", "improves scalability"],
  ["g4", "improves internationalisation"],
]);

const PARTIES = new Map([
  ["p1", "Sales"],
  ["p2", "the Berlin pilot"],
  ["p3", "Anna"],
]);

let guided = [
  { join: "j1", id: "g1", name: "improves performance" },
  { join: "j2", id: "g3", name: "improves scalability" },
];
let wanted = [
  { join: "j3", id: "p1", name: "Sales" },
  { join: "j4", id: "p2", name: "the Berlin pilot" },
];

const DETAIL =
  "They come back two days later and re-read their own notes to work out what " +
  "they meant. The board is meant to carry the context between sessions, which " +
  "is the whole reason it is a board and not a document.";

/** Just enough of a Workspace for the two panels, and no more. */
const workspace = {
  rows: CARDS.map((node, i) => ({ node, depth: i === 0 ? 0 : 1, parentId: i === 0 ? "" : "c1" })),
  guidelines: [...GUIDELINES].map(([id, name], i) => ({ id, name, count: [7, 2, 4, 0][i] })),
  parties: [...PARTIES].map(([id, name], i) => ({ id, name, count: [11, 3, 1][i] })),
  graph: { guidelines: GUIDELINES, parties: PARTIES },
  text: (id: string) => String(CARDS.find((c) => c.id === id)?.fields.text ?? ""),
  detail: () => DETAIL,
  setDetail: () => true,
  setText: () => undefined,
  enter: () => undefined,
  leave: () => undefined,
  iconOf: () => "bulb",
  setIcon: () => true,
  regionOf: () => ({ id: "r1", name: "Who we are for" }),
  taskFor: () => null,
  guidelinesOf: () => guided,
  partiesOf: () => wanted,
  unguide: () => true,
  removeInterest: () => true,
  guide: () => "j",
  addInterest: () => "j",
  linksOf: () => [
    { edge: "e1", other: "c2", text: "the same promise, twice", dangling: false },
    { edge: "e2", other: "c3", text: "", dangling: false },
  ],
  setLinkText: () => true,
  link: () => "e",
  unlink: () => true,
  cardsUnder: () => CARDS,
  addGuideline: () => "g5",
  renameGuideline: () => true,
  removeGuideline: () => true,
  addParty: () => "p4",
  renameParty: () => true,
  removeParty: () => true,
};

const outline = {
  keydown: () => undefined,
  register: () => undefined,
  promote: () => undefined,
  remove: () => undefined,
};

const row = { node: CARDS[0], depth: 0, parentId: "", index: 0, siblings: 1, descendants: 0 };

const app = document.getElementById("app")!;

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

function settled(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

async function run() {
  const card = mount(CardDialog, {
    target: app,
    props: {
      workspace: workspace as never,
      row: row as never,
      outline: outline as never,
      onclose: () => {},
      onmanage: () => {},
    },
  });
  await settled();
  await shot("card-full");

  // The other half of the question: what the same panel looks like on a card
  // that carries none of it, which is what every card looks like on day one.
  guided = [];
  wanted = [];
  workspace.detail = () => "";
  workspace.iconOf = () => "";
  workspace.linksOf = () => [];
  workspace.regionOf = () => null as never;
  void unmount(card);
  const empty = mount(CardDialog, {
    target: app,
    props: {
      workspace: workspace as never,
      row: row as never,
      outline: outline as never,
      onclose: () => {},
      onmanage: () => {},
    },
  });
  await settled();
  await shot("card-empty");

  void unmount(empty);
  mount(VocabularyDialog, {
    target: app,
    props: {
      workspace: workspace as never,
      which: "guidelines",
      onclose: () => {},
      onreveal: () => {},
    },
  });
  await settled();
  await shot("vocabulary");

  console.log("cards harness done");
}

void run();
