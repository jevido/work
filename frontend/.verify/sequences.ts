/**
 * Drives the office's scripted sequences with no canvas and no browser, and
 * reports what actually happened.
 *
 * The other harnesses in here mount the real app in WebKitGTK, because what
 * they check is layout, focus and effect-flush order -- things only an engine
 * can answer. These two sequences are not that: a handoff and a rally are a
 * state machine over a floorplan, they take tens of seconds of wall clock to
 * play out, and the interesting questions about them ("does the folder come
 * back", "does exactly one player win") are answered by stepping the simulation
 * faster than real time and reading the agents afterwards.
 *
 * So this imports the simulation directly -- agent.ts, handoff.ts, idle.ts,
 * props.ts, world.ts -- and steps it by hand. Nothing here touches the
 * renderer: it owns the drawing and a canvas context, and none of what is
 * checked below is drawing.
 *
 * Run it with `task verify:office`.
 */
import { OfficeAgent, type IdleActivity } from "../src/lib/office/agent";
import { HandoffDirector } from "../src/lib/office/handoff";
import { IdleDirector } from "../src/lib/office/idle";
import { furnitureObstacles, placeProps, waitSeat } from "../src/lib/office/props";
import { deskObstacle } from "../src/lib/office/world";

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

/**
 * The seating internal/agents/layout.go actually produces for a coordinator
 * and two specialists: the boss centred at the front, one row of two behind.
 *
 * Written out rather than computed, so a change to the Go layout shows up here
 * as a harness that no longer matches the app rather than as one that quietly
 * agrees with itself.
 */
const CAST = [
  {
    id: "anton",
    name: "Anton",
    colour: "#6ea8fe",
    boss: true,
    deskX: 500,
    deskY: 140,
    seatX: 500,
    seatY: 208,
  },
  { id: "kim", name: "Kim", colour: "#5bc8a0", deskX: 320, deskY: 430, seatX: 320, seatY: 498 },
  { id: "raj", name: "Raj", colour: "#ef6f6c", deskX: 680, deskY: 430, seatX: 680, seatY: 498 },
];

function build() {
  const agents = CAST.map((s) => new OfficeAgent(s));
  const obstacles = CAST.map((s) => deskObstacle(s.deskX, s.deskY, s.boss ?? false));
  const props = placeProps(obstacles);
  obstacles.push(...furnitureObstacles(props));
  for (const agent of agents) agent.setObstacles(obstacles);
  return { agents, obstacles, props };
}

/** One frame, at the rate the renderer caps a busy office to. */
const DT = 1 / 30;

interface Director {
  update(dt: number): void;
}

function step(agents: readonly OfficeAgent[], dirs: readonly Director[]): void {
  for (const agent of agents) agent.update(DT);
  // The order the renderer uses, and it matters: the handoff claims an agent
  // on the frame their task ends, before the idle director can offer them a
  // coffee they have no free hand for.
  for (const dir of dirs) dir.update(DT);
}

function run(agents: readonly OfficeAgent[], dirs: readonly Director[], seconds: number): void {
  const frames = Math.round(seconds / DT);
  for (let i = 0; i < frames; i++) step(agents, dirs);
}

/**
 * Runs until something is true, or the time is up. Returns whether it fired.
 *
 * The stopping is the point. A rally lasts seconds inside a simulation that has
 * to run for minutes before the director starts one, so a loop that always ran
 * to the end would always be looking at whatever the office did next instead.
 */
function runUntil(
  agents: readonly OfficeAgent[],
  dirs: readonly Director[],
  seconds: number,
  done: () => boolean,
): boolean {
  const frames = Math.round(seconds / DT);
  for (let i = 0; i < frames; i++) {
    step(agents, dirs);
    if (done()) return true;
  }
  return false;
}

// --------------------------------------------------------------- handoff ---
// The whole round trip, with the backend's own events arriving through it.
{
  const { agents, obstacles } = build();
  const [anton, kim] = agents;
  const said: string[] = [];
  const handoff = new HandoffDirector((a, text) => said.push(`${a.id}: ${text}`));
  handoff.setScene(agents, obstacles);

  check(
    "the coordinator's own turn is not handed to him",
    handoff.claim(anton, "work") === false,
  );
  check("a planning turn is not handed over either", handoff.claim(kim, "plan") === false);

  check("a specialist's work turn is claimed", handoff.claim(kim, "work") === true);
  check(
    "the claimed agent stands by rather than walking to their desk",
    kim.errand === "hold" && kim.state === "idle" && kim.intent === "desk",
    `errand=${kim.errand} state=${kim.state} intent=${kim.intent}`,
  );

  run(agents, [handoff], 0.5);
  check(
    "the coordinator takes a folder and sets off",
    anton.holding === "files" && anton.errand === "walk",
    `holding=${anton.holding} errand=${anton.errand}`,
  );

  let met = false;
  runUntil(agents, [handoff], 14, () => (met = kim.holding === "files"));
  check("the folder reaches the agent", met, `kim holding=${kim.holding}`);
  check(
    "the coordinator hands it over rather than keeping hold of it",
    anton.holding === "none",
    `holding=${anton.holding}`,
  );
  check(
    "the agent sets off for their own desk with it",
    kim.holding === "files" && kim.intent === "desk",
    `holding=${kim.holding} intent=${kim.intent}`,
  );
  check(
    "the exchange is spoken from both sides",
    said.some((l) => l.startsWith("anton:")) && said.some((l) => l.startsWith("kim:")),
    said.join(" | "),
  );

  // The event the backend sends the moment real output starts.
  kim.work();
  check("a work event does not confiscate the folder", kim.holding === "files");
  run(agents, [handoff], 2);
  check(
    "the agent is seated at their own desk, working",
    kim.state === "working" &&
      Math.abs(kim.x - kim.seatX) < 2 &&
      Math.abs(kim.y - kim.seatY) < 2,
    `state=${kim.state} at ${Math.round(kim.x)},${Math.round(kim.y)}`,
  );

  const spot = waitSeat(anton.deskX, anton.seatY);
  const waited = runUntil(agents, [handoff], 10, () => anton.sitting && anton.errand === "hold");
  check(
    "the coordinator waits seated at the end of his desk",
    waited && Math.abs(anton.x - spot.x) < 2 && Math.abs(anton.y - spot.y) < 2,
    `at ${Math.round(anton.x)},${Math.round(anton.y)} want ${spot.x},${spot.y}`,
  );
  check("he faces back along the desk, where the board hangs", anton.facing === -1);
  check(
    "his chair went with him",
    Math.abs(handoff.chairX - spot.x) < 1,
    `chair at ${Math.round(handoff.chairX)}`,
  );

  // Task done. The finished flourish is the beat the scribble is drawn in.
  kim.finish();
  check(
    "the folder is still in hand for the scribble",
    kim.state === "finished" && kim.holding === "files",
  );
  run(agents, [handoff], 2.5);
  check(
    "the agent walks the folder back",
    kim.errand === "walk" && kim.holding === "files",
    `errand=${kim.errand} state=${kim.state}`,
  );

  run(agents, [handoff], 16);
  check(
    "the folder lands on the coordinator's in-tray",
    handoff.tray === 1 && kim.holding === "none",
    `tray=${handoff.tray} kim holding=${kim.holding}`,
  );
  check(
    "the agent is handed back to wandering",
    kim.errand === "none" && kim.activity === "none",
    `errand=${kim.errand} activity=${kim.activity}`,
  );

  run(agents, [handoff], 3);
  check(
    "the chair comes home once nothing is owed",
    !anton.sitting && Math.abs(handoff.chairX - anton.seatX) < 1,
    `sitting=${anton.sitting} chair at ${Math.round(handoff.chairX)}`,
  );

  anton.work();
  run(agents, [handoff], 6);
  check("the tray empties when he sits down to read them", handoff.tray === 0);
}

// Two steps of one plan: he makes the rounds, because a delivery costs less
// time than his patience allows.
{
  const { agents, obstacles } = build();
  const [, kim, raj] = agents;
  const handoff = new HandoffDirector(() => {});
  handoff.setScene(agents, obstacles);

  handoff.claim(kim, "work");
  handoff.claim(raj, "work");
  run(agents, [handoff], 18);
  check(
    "two folders at once are both hand-delivered, in turn",
    kim.holding === "files" && raj.holding === "files",
    `kim=${kim.holding} raj=${raj.holding}`,
  );
}

// Nobody free to walk it over, so he rings instead.
{
  const { agents, obstacles } = build();
  const [anton, kim] = agents;
  const said: string[] = [];
  const handoff = new HandoffDirector((a, text) => said.push(`${a.id}: ${text}`));
  handoff.setScene(agents, obstacles);

  handoff.claim(kim, "work");
  anton.work();
  const phoned = runUntil(agents, [handoff], 9, () => kim.holding === "phone");
  check("a busy coordinator rings instead of walking", phoned, said.join(" | "));
  check(
    "his own turn is not interrupted to do it",
    anton.state === "working" && anton.holding === "none",
    `state=${anton.state} holding=${anton.holding}`,
  );

  run(agents, [handoff], 4);
  check(
    "a phoned agent goes to their desk with nothing to bring back",
    kim.holding === "none" && kim.intent === "desk" && handoff.tray === 0,
    `holding=${kim.holding} intent=${kim.intent} tray=${handoff.tray}`,
  );
}

// ------------------------------------------------------------- ping pong ---
{
  const { agents, obstacles, props } = build();
  const said: string[] = [];

  // Seeded rather than fixed. A constant would be simpler and never returns:
  // the line pools draw again when they would repeat themselves.
  const real = Math.random;
  let seed = 20260909;
  Math.random = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };

  try {
    const director = new IdleDirector((a, text) => said.push(`${a.id}: ${text}`));
    director.setScene(agents, obstacles, props);
    check(
      "the bullpen had room for a table",
      props.pong !== null,
      props.pong ? `${props.pong.x},${props.pong.y}` : "none",
    );

    let pair: [OfficeAgent, OfficeAgent] | null = null;
    const started = runUntil(agents, [director], 300, () => {
      const bit = director.bits.find((b) => b.kind === ("pingpong" as IdleActivity));
      if (bit?.a && bit.b) pair = [bit.a, bit.b];
      return pair !== null;
    });
    check(
      "a rally started",
      started,
      pair ? `${pair[0].id} v ${pair[1].id}` : "none in five minutes",
    );

    if (pair) {
      const [a, b] = pair as [OfficeAgent, OfficeAgent];
      let cheer: OfficeAgent | null = null;
      let sob: OfficeAgent | null = null;
      const decided = runUntil(agents, [director], 60, () => {
        if (a.emote === "cheer") cheer = a;
        if (b.emote === "cheer") cheer = b;
        if (a.emote === "sob") sob = a;
        if (b.emote === "sob") sob = b;
        return cheer !== null && sob !== null;
      });
      check(
        "the rally ends with one winner and one loser",
        decided && cheer !== sob,
        `cheer=${cheer?.id} sob=${sob?.id}`,
      );
      check(
        "and they are the two who were playing",
        (cheer === a || cheer === b) && (sob === a || sob === b),
      );
      check(
        "both are still at the table while it plays out",
        a.activity === "pingpong" && b.activity === "pingpong",
        `${a.id}=${a.activity} ${b.id}=${b.activity}`,
      );

      // Long enough for the cheer to run out, short enough that the director
      // cannot have talked either of them into something else yet.
      run(agents, [director], 1.6);
      check(
        "the reactions run out on their own",
        a.emote === "none" && b.emote === "none",
        `${a.id}=${a.emote} ${b.id}=${b.emote}`,
      );
      check(
        "both leave the table, bats back on it",
        a.activity !== "pingpong" &&
          b.activity !== "pingpong" &&
          a.holding !== "paddle" &&
          b.holding !== "paddle",
        `${a.id}=${a.activity}/${a.holding} ${b.id}=${b.activity}/${b.holding}`,
      );
    }
  } finally {
    Math.random = real;
  }
}

const failed = results.filter((r) => !r.pass);
console.log(`\n${results.length - failed.length}/${results.length} passed`);
// Thrown rather than exited, so the task fails without this file needing node's
// type definitions -- which the frontend does not otherwise carry.
if (failed.length > 0) {
  throw new Error(`office sequences: ${failed.map((r) => r.name).join("; ")}`);
}
