/**
 * Drives the rewired workspace surface and reports what actually happened.
 *
 * The frontend's own sync stack is gone -- transport.ts, sync.svelte.ts,
 * replica's outbox and the storage that held a write key -- and everything the
 * tab strip, the sync badge and the join dialog show now comes from
 * WorkbenchService over IPC. The checks below are in three groups:
 *
 *   1. nothing on the wire, and the workspace read over bindings;
 *   2. the DOM identities that must survive a rewire -- the same <canvas>,
 *      the same <textarea>, a mode toggle that is still checked;
 *   3. the states a person has to be able to tell apart: refused from
 *      offline, a tab with no folder from one that has one.
 *
 * Nothing here reads a flag the app set about itself. Requests are counted by
 * wrapping fetch, paints by wrapping the 2d context, and identity by holding
 * the element and comparing it with ===.
 */

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

const $ = <T extends Element>(sel: string) => document.querySelector<T>(sel);
const $$ = <T extends Element>(sel: string) => [...document.querySelectorAll<T>(sel)];

const tabs = () => $$<HTMLButtonElement>('[role="tab"]');
const tabNamed = (name: string) => tabs().find((t) => t.textContent?.trim() === name) ?? null;
const canvas = () => $<HTMLCanvasElement>("canvas");
const composer = () => $<HTMLTextAreaElement>("aside textarea");
const badge = () => $<HTMLElement>(".badge");
const modeRadios = () => $$<HTMLInputElement>('.modebar input[type="radio"]');
const checkedMode = () => modeRadios().find((r) => r.checked)?.value ?? null;

/** Presses a radio the way a person does: the change event, not the property. */
function pickMode(value: string) {
  const radio = modeRadios().find((r) => r.value === value);
  if (!radio) throw new Error(`no ${value} radio`);
  radio.click();
}

function key(el: Element, k: string, extra: KeyboardEventInit = {}) {
  el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, ...extra }));
}

async function run() {
  await settle(30);

  /* ---------------------------------------------------------------------- */
  /* 1. The rewire: bindings in, nothing on the wire                         */
  /* ---------------------------------------------------------------------- */

  const ids = () => V().calls.map((c: any) => c.id);
  check(
    "the workspace was read over bindings, not fetched",
    ids().includes(1679766508) && ids().includes(1753161623) && ids().includes(1767437727),
    `Workspaces/Workspace/SyncStatus in ${ids().length} calls`,
  );

  check(
    "the tab strip is the backend's tabs",
    tabs().map((t) => t.textContent?.trim()).join(",") === "work,planning",
    tabs().map((t) => t.textContent?.trim()).join(",") || "no tabs",
  );

  const shot = canvas();
  // Notes taken before the outline had anywhere to go are carried into the
  // workspace the first time the tab has somewhere to send to. Checked early:
  // it happens at startup, and once.
  {
    const carried = V()
      .calls.filter((c: any) => c.id === 1305658848)
      .flatMap((c: any) => (c.args?.[1] ?? []) as { kind: string; node?: string; fields?: any }[]);
    const texts = carried.map((e) => e.fields?.text).filter(Boolean);
    check(
      "notes taken before the move are carried into the workspace",
      texts.includes("a note from before the move"),
      texts.join(" | ").slice(0, 80) || "nothing carried",
    );
    check("with their structure", texts.includes("and one nested under it"));
    check(
      "and their ids, so a task extracted from one still points at it",
      carried.some((e) => e.node === "kept1"),
    );
  }

  check("the office is mounted", !!shot);
  if (!shot) return;

  /* ---------------------------------------------------------------------- */
  /* 2. Identities that must survive the rewire                              */
  /* ---------------------------------------------------------------------- */

  const canvasBefore = canvas();
  const composerBefore = composer();
  check("the console has a composer", !!composerBefore);

  // A mode switch. Idea and planning mount and unmount; work keeps its box.
  pickMode("idea");
  await settle(20);
  check("switching to idea draws the outline", !!$(".idea"));
  check(
    "the same <canvas> element after a mode switch",
    canvas() === canvasBefore,
    canvas() === canvasBefore ? "identical node" : "the office was remounted",
  );
  check(
    "the same <textarea> after a mode switch",
    composer() === composerBefore,
    composer() === composerBefore ? "identical node" : "the console was remounted",
  );

  // Nothing painted while the office is off screen. Measured on the context,
  // so this is what the engine was asked to do rather than what a flag says.
  const paintedAtSwitch = V().paints(canvasBefore);
  await settle(45);
  const paintedAfter = V().paints(canvasBefore);
  check(
    "0 draw calls into the canvas while it is hidden",
    paintedAfter === paintedAtSwitch,
    `${paintedAfter - paintedAtSwitch} draw calls over 45 frames of idea mode`,
  );

  // And it starts again when it is shown, or the check above proves nothing.
  pickMode("work");
  await settle(20);
  check(
    "drawing resumes when the office is shown again",
    V().paints(canvasBefore) > paintedAfter,
    `${V().paints(canvasBefore) - paintedAfter} draw calls after coming back`,
  );

  /* ---------------------------------------------------------------------- */
  /* 3. The mode toggle across a tab switch                                  */
  /* ---------------------------------------------------------------------- */

  pickMode("planning");
  await settle(20);
  check("planning is the checked mode before switching tabs", checkedMode() === "planning");

  const other = tabNamed("planning");
  check("there is a second tab to switch to", !!other);
  if (!other) return;
  other.click();
  await settle(25);

  check(
    "a tab switch keeps a checked mode in the toggle",
    checkedMode() !== null,
    checkedMode() ?? "nothing checked -- the radio group lost its selection",
  );
  check(
    "the same <canvas> element after a tab switch",
    canvas() === canvasBefore,
    canvas() === canvasBefore ? "identical node" : "the office was remounted",
  );
  check(
    "the same <textarea> after a tab switch",
    composer() === composerBefore,
    composer() === composerBefore ? "identical node" : "the console was remounted",
  );

  // The second tab has no folder here, so agents cannot run in it -- and the
  // backend refused ActivateTab. That must be visible rather than silent.
  check(
    "a tab with no folder on this machine says so",
    (($(".tab-notice")?.textContent ?? "")).includes("no folder"),
    $(".tab-notice")?.textContent?.trim().slice(0, 90) ?? "no notice",
  );
  check(
    "and offers the folder picker",
    !!$$<HTMLButtonElement>(".tab-notice button").find(
      (b) => b.textContent?.trim() === "Choose a folder",
    ),
  );

  /* ---------------------------------------------------------------------- */
  /* 4. The outline: focus through a structural edit, and detached nodes     */
  /* ---------------------------------------------------------------------- */

  tabNamed("work")!.click();
  await settle(20);
  pickMode("idea");
  await settle(20);

  let lines = () => $$<HTMLInputElement>('input[aria-label="Outline line"]');
  if (lines().length === 0) {
    $<HTMLButtonElement>(".idea .primary")?.click();
    await settle(20);
  }
  check("the outline has a line to type in", lines().length > 0, `${lines().length} lines`);
  if (lines().length === 0) return;

  const first = lines()[0];
  first.focus();
  first.value = "a line";
  first.dispatchEvent(new Event("input", { bubbles: true }));
  await settle(6);

  // Enter splits the outline: a structural edit that replaces the row list.
  key(first, "Enter");
  await settle(20);
  check(
    "Enter adds a line",
    lines().length >= 2,
    `${lines().length} lines`,
  );
  check(
    "focus is on the new line, not lost to the document",
    document.activeElement instanceof HTMLInputElement &&
      document.activeElement.getAttribute("aria-label") === "Outline line",
    document.activeElement?.nodeName ?? "nothing",
  );

  // Tab indents: another structural edit, on the line that has focus.
  const focused = document.activeElement as HTMLInputElement;
  focused.value = "under it";
  focused.dispatchEvent(new Event("input", { bubbles: true }));
  await settle(6);
  key(focused, "Tab");
  await settle(20);
  check(
    "focus survives an indent",
    document.activeElement === focused,
    document.activeElement === focused ? "same input" : (document.activeElement?.nodeName ?? "nothing"),
  );


  /* ---------------------------------------------------------------------- */
  /* 4b. A proposal from Claude: reviewed, not applied                       */
  /* ---------------------------------------------------------------------- */

  // Everything here goes in as a `claude:tool` call, which is how a proposal
  // actually arrives -- there is no proposal event and no endpoint for one.
  // The point of the section is the thing that is easiest to get wrong and
  // worst to get wrong: nothing may reach the document before Apply.

  const nodeIds = () => $$<HTMLElement>(".idea [data-node]").map((el) => el.dataset.node!);
  const reviewPanel = () => $<HTMLElement>("section.review");
  const reviewRows = () => $$<HTMLLIElement>("section.review ol li");
  const reviewBoxes = () => $$<HTMLInputElement>("section.review ol input[type=checkbox]");
  const applyButton = () =>
    $$<HTMLButtonElement>("section.review footer button").find((b) =>
      (b.textContent ?? "").startsWith("Apply"),
    ) ?? null;
  const outlineText = () => lines().map((l) => l.value).join(" | ");

  // ---- Asking for one ----------------------------------------------------
  //
  // The half before a proposal exists. Everything below this point tests what
  // happens once one has arrived; without this, the control that causes one
  // could be removed and the suite would not notice.

  const askButton = () =>
    $$<HTMLButtonElement>(".ask button").find((b) =>
      (b.textContent ?? "").startsWith("Ask Claude"),
    ) ?? null;

  check("idea mode offers a way to ask", !!askButton(), askButton()?.textContent?.trim() ?? "no button");

  askButton()?.click();
  await settle(6);
  const askField = () => $<HTMLInputElement>(".ask input");
  check("asking opens a composer rather than firing", !!askField());

  // An empty request may not be sent: "reorganise this" with nothing else said
  // is a coin toss, and the field is the whole reason the control is not a
  // one-click button.
  const submit = () =>
    $$<HTMLButtonElement>(".ask button").find((b) => b.textContent?.trim() === "Ask") ?? null;
  check("an empty request cannot be sent", submit()?.disabled === true);

  const askInput = askField();
  if (askInput) {
    askInput.value = "group these by area";
    askInput.dispatchEvent(new Event("input", { bubbles: true }));
  }
  await settle(4);
  check("a typed request can be sent", submit()?.disabled === false);

  submit()?.click();
  await settle(10);

  const asked = V().calls.filter((c: any) => c.id === 3325402576);
  check("asking reaches the backend once", asked.length === 1, `${asked.length} calls`);
  check(
    "it is told which mode it was asked in",
    asked[0]?.args?.[1] === "idea",
    String(asked[0]?.args?.[1]),
  );
  check(
    "and what was actually typed",
    String(asked[0]?.args?.[2] ?? "").includes("group these by area"),
    String(asked[0]?.args?.[2] ?? "").slice(0, 60),
  );

  check("while it runs, the control says so", !askButton() && !!$(".ask .thinking"));

  V().finishRestructure();
  await settle(8);
  check("when the run ends, it can be asked again", !!askButton());

  // A proposal that tries to place a node itself. Refused whole: something
  // that believes it owns sort keys cannot be trusted with the rest either.
  V().propose({ summary: "no", ops: [{ kind: "insert", parent: "", after: null, text: "x", position: "m" }] });
  await settle(15);
  check(
    "a proposal carrying a sort key is refused, not cleaned up",
    !reviewPanel() && (($(".refused")?.textContent ?? "").includes("position")),
    $(".refused .detail")?.textContent?.trim().slice(0, 90) ?? "nothing said",
  );
  $$<HTMLButtonElement>(".refused button").find((b) => b.textContent?.trim() === "Dismiss")?.click();
  await settle(10);

  const nodes = nodeIds();
  check("the outline has nodes a proposal can name", nodes.length >= 2, `${nodes.length} nodes`);
  if (nodes.length < 2) return;

  const textBefore = outlineText();

  V().propose({
    summary: "Group the networking lines together.",
    ops: [
      { kind: "insert", ref: "head", parent: "", after: nodes[0], text: "Networking" },
      { kind: "move", node: nodes[1], parent: "head", after: null },
      { kind: "set-text", node: nodes[0], text: "The first thought" },
      // A line nothing knows about: the document moved on under the proposal.
      { kind: "delete", node: "n_no_such_node" },
    ],
  });
  await settle(20);

  check("a proposal opens a review panel", !!reviewPanel());
  if (!reviewPanel()) return;

  check(
    "one row per operation",
    reviewRows().length === 4,
    `${reviewRows().length} rows`,
  );

  const says = reviewRows().map((li) => li.querySelector(".says")?.textContent?.trim() ?? "");
  check(
    "rows are in English, naming lines by their text",
    says.some((t) => t.includes("Networking")) &&
      says.some((t) => t.startsWith("Move") && t.includes("under")) &&
      says.some((t) => t.startsWith("Reword")),
    says.join(" / ").slice(0, 160),
  );
  check(
    "no row is written in protocol terms",
    !says.some((t) => /set-fields|move-node|create-node|delete-node|extract-to-task/.test(t)),
    says.join(" / ").slice(0, 120),
  );

  // The stale row. Shown and unapplicable, rather than quietly dropped.
  const staleRow = reviewRows().find((li) => li.querySelector(".why"));
  check(
    "an operation the document has outgrown is shown with the reason",
    !!staleRow && (staleRow.querySelector(".why")?.textContent ?? "").includes("deleted"),
    staleRow?.querySelector(".why")?.textContent?.trim() ?? "no blocked row",
  );
  check(
    "and cannot be ticked",
    !!staleRow?.querySelector<HTMLInputElement>("input[type=checkbox]")?.disabled,
  );

  // The whole contract: a proposal on screen has changed nothing.
  check(
    "nothing has been applied to the outline yet",
    outlineText() === textBefore,
    `${textBefore} -> ${outlineText()}`,
  );

  // Unticking the insert has to block the move that was going under it: the
  // move names a line the insert was going to create.
  const boxes = reviewBoxes();
  boxes[0].checked = false;
  boxes[0].dispatchEvent(new Event("change", { bubbles: true }));
  await settle(15);
  const movedRow = reviewRows()[1];
  check(
    "unticking an insert blocks the move that depended on it",
    !!movedRow.querySelector(".why"),
    movedRow.querySelector(".why")?.textContent?.trim() ?? "the move is still applicable",
  );

  // Put it back, and take the reword out instead -- a row with nothing
  // depending on it, so Apply should land three and leave one.
  boxes[0].checked = true;
  boxes[0].dispatchEvent(new Event("change", { bubbles: true }));
  await settle(10);
  const rewordBox = reviewBoxes()[2];
  rewordBox.checked = false;
  rewordBox.dispatchEvent(new Event("change", { bubbles: true }));
  await settle(15);

  check(
    "Apply counts only what it will actually write",
    (applyButton()?.textContent ?? "").includes("2"),
    applyButton()?.textContent?.trim() ?? "no apply button",
  );

  applyButton()!.click();
  await settle(25);

  check(
    "applying writes the ticked rows into the outline",
    outlineText().includes("Networking"),
    outlineText(),
  );
  check(
    "and leaves the unticked one alone",
    !outlineText().includes("The first thought"),
    outlineText(),
  );
  check(
    "it says what it did",
    ($("section.review .outcome")?.textContent ?? "").includes("Applied 2"),
    $("section.review .outcome")?.textContent?.trim() ?? "said nothing",
  );
  check(
    "a second press cannot apply the same insert twice",
    applyButton()?.disabled === true,
    applyButton()?.textContent?.trim() ?? "no apply button",
  );

  // A proposal that arrives while the office is on screen has nowhere to
  // draw itself. It must not be dropped and must not yank anybody out of a
  // run they are watching, so it says it is there and offers the way to it.
  pickMode("work");
  await settle(15);
  const parked = $$<HTMLElement>(".refused").find((n) =>
    (n.textContent ?? "").includes("suggested changes"),
  );
  check(
    "a proposal held while the office is up says so instead of vanishing",
    !!parked && !reviewPanel(),
    parked?.textContent?.trim().slice(0, 80) ?? "nothing said",
  );
  const goReview = $$<HTMLButtonElement>(".refused button").find((b) =>
    (b.textContent ?? "").startsWith("Review them in"),
  );
  check("and offers the way back to it", !!goReview, goReview?.textContent?.trim() ?? "no button");
  goReview?.click();
  await settle(20);
  check("which opens the review", !!reviewPanel(), checkedMode() ?? "no mode");

  $$<HTMLButtonElement>("section.review .discard").at(0)?.click();
  await settle(15);
  check("Discard puts the panel away", !reviewPanel());

  /* ---------------------------------------------------------------------- */
  /* 5. Refused is not offline                                              */
  /* ---------------------------------------------------------------------- */

  pickMode("work");
  await settle(10);

  V().sync({ state: "offline", pending: 3, error: "dial tcp: connection refused" });
  await settle(15);
  const offlineState = badge()?.dataset.state ?? "";
  const offlineWords = badge()?.textContent?.trim() ?? "";
  const offlineAction = $$<HTMLButtonElement>(".badge button").map((b) => b.textContent?.trim());
  check("offline reads as offline", offlineState === "offline", `${offlineState}: ${offlineWords}`);
  check(
    "offline offers a retry and not a key",
    offlineAction.includes("Retry now") && !offlineAction.includes("Use another key"),
    offlineAction.join(" | "),
  );

  V().sync({ state: "rejected", pending: 3, error: "unknown or expired key" });
  await settle(15);
  const rejectedState = badge()?.dataset.state ?? "";
  const rejectedWords = badge()?.textContent?.trim() ?? "";
  const rejectedAction = $$<HTMLButtonElement>(".badge button").map((b) => b.textContent?.trim());
  check(
    "a refused key is a different state from offline",
    rejectedState === "rejected" && rejectedState !== offlineState,
    `${offlineState} -> ${rejectedState}`,
  );
  check(
    "and different words, not just a different colour",
    rejectedWords !== offlineWords && rejectedWords.includes("Key refused"),
    `${offlineWords} -> ${rejectedWords}`,
  );
  check(
    'a refused key offers "Use another key"',
    rejectedAction.includes("Use another key"),
    rejectedAction.join(" | "),
  );
  check(
    "the live region says the changes are safe meanwhile",
    ($('[role="status"][aria-live="polite"]')?.textContent ?? "").length > 0 &&
      $$<HTMLElement>('[role="status"]').some((el) =>
        (el.textContent ?? "").includes("Your changes are safe"),
      ),
  );

  /* ---------------------------------------------------------------------- */
  /* 6. A read key is refused before a tab opens                            */
  /* ---------------------------------------------------------------------- */

  const tabsBefore = tabs().length;
  const joinCallsBefore = V().calls.filter((c: any) => c.id === 1719684323).length;

  $$<HTMLButtonElement>(".badge button").find((b) => b.textContent?.trim() === "Use another key")!.click();
  await settle(20);

  const dialog = $<HTMLDialogElement>("dialog[aria-labelledby='workspace-dialog-title']");
  check("the rekey dialog opened", !!dialog);
  if (!dialog) return;

  const field = dialog.querySelector<HTMLInputElement>('input[placeholder^="wk_"]');
  check("the dialog has a key field", !!field);
  if (!field) return;

  field.value = "rk_" + "a".repeat(32);
  field.dispatchEvent(new Event("input", { bubbles: true }));
  await settle(8);
  check(
    "typing a read key warns before anything is submitted",
    (dialog.querySelector(".warn")?.textContent ?? "").includes("read"),
    dialog.querySelector(".warn")?.textContent?.trim().slice(0, 70) ?? "no warning",
  );

  dialog.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await settle(25);

  check(
    "a read key never reaches the backend",
    V().calls.filter((c: any) => c.id === 1719684323).length === joinCallsBefore,
    `${V().calls.filter((c: any) => c.id === 1719684323).length - joinCallsBefore} JoinWorkspace calls`,
  );
  check(
    "and no tab opened for it",
    tabs().length === tabsBefore,
    `${tabsBefore} -> ${tabs().length}`,
  );
  check(
    "it says which kind of key it wanted",
    (dialog.querySelector('[role="alert"]')?.textContent ?? "").includes("write key"),
    dialog.querySelector('[role="alert"]')?.textContent?.trim().slice(0, 90) ?? "no error",
  );

  // The same field with a write key gets through, so the refusal above is
  // about the key and not about the form being broken.
  field.value = "wk_" + "b".repeat(32);
  field.dispatchEvent(new Event("input", { bubbles: true }));
  dialog.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await settle(30);
  check(
    "a write key does reach the backend",
    V().calls.filter((c: any) => c.id === 1719684323).length === joinCallsBefore + 1,
    `${V().calls.filter((c: any) => c.id === 1719684323).length - joinCallsBefore} JoinWorkspace calls`,
  );

  /* ---------------------------------------------------------------------- */
  /* 7. Closing a tab says what it does                                     */
  /* ---------------------------------------------------------------------- */

  await settle(20);
  const toClose = tabNamed("planning");
  if (toClose) {
    toClose.focus();
    key(toClose, "Delete");
    await settle(20);
    const confirm = $$<HTMLDialogElement>("dialog").find((d) =>
      (d.textContent ?? "").includes("for everyone"),
    );
    check(
      "Delete on a tab asks before retiring it, and says it is for everyone",
      !!confirm,
      confirm?.querySelector("h2")?.textContent?.trim() ?? "no confirm",
    );
    check(
      "and says the local notes stay",
      (confirm?.textContent ?? "").includes("stay on this machine"),
    );
    confirm?.querySelector<HTMLButtonElement>(".ghost")?.click();
    await settle(10);
  }

  /* ---------------------------------------------------------------------- */
  /* 8. The whole point: not one request, for the whole session             */
  /* ---------------------------------------------------------------------- */

  await settle(30);
  const net = V().requests as { how: string; url: string }[];
  // The page itself is served over HTTP by the dev server, and Vite's module
  // graph is fetched by the engine rather than by script -- neither goes
  // through window.fetch. Anything in this list was issued by code.
  const mine = net.filter((r) => !/\/@vite|\/node_modules|\.ts$|\.svelte/.test(r.url));
  check(
    "the frontend issued no requests of its own",
    mine.length === 0,
    mine.map((r) => `${r.how} ${r.url}`).join(" | ") || "none",
  );
  check(
    "and nothing at all went to a /v1 endpoint",
    !net.some((r) => r.url.includes("/v1")),
    net.filter((r) => r.url.includes("/v1")).map((r) => `${r.how} ${r.url}`).join(" | ") || "none",
  );
  /* ---------------------------------------------------------------------- */
  /* 4c. The opt-out: applied without asking, and said out loud              */
  /* ---------------------------------------------------------------------- */

  // The default is the point of the panel, so it is checked before the
  // opt-out: a suite that only ever ran with the setting on would not notice
  // the gate disappearing.
  const settingsToggle = () =>
    $$<HTMLInputElement>('input[type=checkbox]').find((i) =>
      (i.closest("label")?.textContent ?? "").includes("without asking"),
    ) ?? null;

  const settingsButton = $$<HTMLButtonElement>("button").find((b) =>
    (b.getAttribute("aria-label") ?? b.textContent ?? "").toLowerCase().includes("setting"),
  );
  settingsButton?.click();
  await settle(6);

  const toggle = settingsToggle();
  check("the approval gate can be turned off from settings", !!toggle);
  check("and it is on by default", toggle?.checked === false);

  if (toggle) {
    toggle.checked = true;
    toggle.dispatchEvent(new Event("change", { bubbles: true }));
    await settle(10);
  }
  settingsButton?.click();
  await settle(6);

  // Back to the outline first. By this point the suite has switched modes,
  // closed a tab and had a key refused, and a proposal needs a document on
  // screen with ids in it to name.
  pickMode("idea");
  await settle(10);

  const target = nodeIds()[0];
  check("there is a line for an unreviewed proposal to rename", !!target, target ?? "none");

  const before = outlineText();
  if (target) {
    V().propose({
      summary: "Rename the first line.",
      ops: [{ kind: "set-text", node: target, text: "Applied without asking" }],
    });
    await settle(20);
  }

  check("with the setting on, nothing waits to be reviewed", !reviewPanel());
  check(
    "the change lands anyway",
    outlineText() !== before && outlineText().includes("Applied without asking"),
    outlineText(),
  );
  check(
    "and it says why the outline moved on its own",
    ($$("p.announce").map((p) => p.textContent ?? "").join(" ")).includes("without asking"),
    $$("p.announce").map((p) => (p.textContent ?? "").trim()).filter(Boolean).join(" | ").slice(0, 120),
  );
  // Back off again. A setting that only works in one direction is the usual
  // bug here, and the off leg is the one nobody tests because it is the
  // default they started from.
  settingsButton?.click();
  await settle(6);
  const again = settingsToggle();
  check("the toggle shows it is on", again?.checked === true);
  if (again) {
    again.checked = false;
    again.dispatchEvent(new Event("change", { bubbles: true }));
    await settle(10);
  }
  settingsButton?.click();
  await settle(6);

  const secondTarget = nodeIds()[0];
  if (secondTarget) {
    V().propose({
      summary: "And another.",
      ops: [{ kind: "set-text", node: secondTarget, text: "Should have waited" }],
    });
    await settle(20);
  }
  check("turning it back off makes the next proposal wait again", !!reviewPanel());
  check(
    "and nothing was applied while it waited",
    !outlineText().includes("Should have waited"),
    outlineText().slice(0, 80),
  );


  /* ---------------------------------------------------------------------- */
  /* 4d. A write of ours that did not survive                               */
  /* ---------------------------------------------------------------------- */

  pickMode("idea");
  await settle(10);

  const noteBox = () => $("section.notes");
  const noteText = () => $$("section.notes li p").map((p) => p.textContent ?? "").join(" | ");
  const noteButton = (label: string) =>
    $$<HTMLButtonElement>("section.notes button").find((b) => b.textContent?.trim() === label) ?? null;

  const lostNode = nodeIds()[0];
  check("there is a line to lose", !!lostNode, lostNode ?? "none");

  const textNow = () => lines().map((l) => l.value)[0] ?? "";
  const replaced = textNow();

  V().conflict({ node: lostNode, yours: "what I wrote", now: replaced });
  await settle(10);

  check("a lost write shows a note", !!noteBox(), noteText().slice(0, 80));
  check("the note quotes what was written", noteText().includes("what I wrote"));
  check("and what it says instead", noteText().includes(replaced));

  // The whole design, checked in the one place it would be easiest to break.
  check(
    "the note names nobody",
    !/\bby\b|author|actor|machine|someone|somebody/i.test(noteText()),
    noteText().slice(0, 100),
  );

  // Undo is an ordinary edit, not a rollback: it goes out as an op like any
  // keystroke, so the other side receives it and can undo in turn.
  const editsBefore = V().calls.filter((c: any) => c.id === 1305658848).length;
  noteButton("Undo")?.click();
  await settle(20);

  check("Undo puts the text back", textNow() === "what I wrote", textNow());

  // Undo goes through setText, which is the debounced typing path, so the op is
  // written once the caret has settled rather than on the click.
  //
  // This check is why the outline moved onto Go's queue at all: it failed, and
  // chasing it found that nothing in the frontend called ApplyWorkspaceEdits.
  // Undo is an ordinary edit, so if edits reach Go, this one does.
  await new Promise((r) => setTimeout(r, 900));
  await settle(20);
  check(
    "and does it by writing an op, not locally",
    V().calls.filter((c: any) => c.id === 1305658848).length > editsBefore,
    `${V().calls.filter((c: any) => c.id === 1305658848).length - editsBefore} edits`,
  );
  check("the note goes when it is undone", !noteBox());

  // A second loss on the same field replaces the first rather than stacking.
  V().conflict({ node: lostNode, yours: "first", now: "theirs" });
  await settle(8);
  V().conflict({ node: lostNode, yours: "second", now: "theirs again" });
  await settle(8);
  check("one note per field, not a queue", $$("section.notes li").length === 1, noteText().slice(0, 60));
  check("and it is the newest", noteText().includes("second"));

  noteButton("Dismiss")?.click();
  await settle(8);
  check("a note can be dismissed", !noteBox());

  // A deleted line has nothing to undo -- a delete is permanent by design --
  // but the text is still worth keeping.
  V().conflict({ node: lostNode, yours: "typed into a doomed line", deleted: true });
  await settle(10);
  check("a deleted line says so", noteText().includes("deleted"), noteText().slice(0, 80));
  check("and still gives back what was typed", noteText().includes("typed into a doomed line"));
  check("with no Undo, because there is nothing to undo", !noteButton("Undo"));
  noteButton("Dismiss")?.click();
  await settle(6);

  /* ---------------------------------------------------------------------- */
  /* 4e. Every outline edit becomes an op                                   */
  /* ---------------------------------------------------------------------- */

  const EDITS = 1305658848;
  const editCalls = () => V().calls.filter((c: any) => c.id === EDITS);
  const editsSince = (n: number) =>
    editCalls()
      .slice(n)
      .flatMap((c: any) => (c.args?.[1] ?? []) as { kind: string; node?: string }[]);
  const kindsSince = (n: number) => editsSince(n).map((e) => e.kind).join(",") || "none";

  pickMode("idea");
  await settle(10);

  // Typing. The character is on screen before the call resolves, which is the
  // property that makes the outline feel like a text editor rather than a form.
  const beforeType = editCalls().length;
  const firstRow = lines()[0];
  check("there is a line to type into", !!firstRow);
  if (firstRow) {
    firstRow.value = "typed into a synced outline";
    firstRow.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(2);
    check("the character is on screen immediately", lines()[0].value.includes("synced"));
    await new Promise((r) => setTimeout(r, 900));
    await settle(10);
  }
  check(
    "typing becomes a set-fields edit",
    editsSince(beforeType).some((e) => e.kind === "set-fields"),
    kindsSince(beforeType),
  );

  const beforeCreate = editCalls().length;
  if (firstRow) key(firstRow, "Enter");
  await settle(15);
  check(
    "a new line becomes a create-node edit",
    editsSince(beforeCreate).some((e) => e.kind === "create-node"),
    kindsSince(beforeCreate),
  );
  // The id travels with it, so what Go mints and what is on screen are one node
  // rather than two.
  check(
    "and it carries the id the frontend chose",
    editsSince(beforeCreate).some((e) => e.kind === "create-node" && !!e.node),
  );

  const beforeMove = editCalls().length;
  const secondRow = lines()[1];
  if (secondRow) {
    secondRow.value = "a child";
    secondRow.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(4);
    key(secondRow, "Tab");
    await settle(15);
  }
  check(
    "an indent becomes a move-node edit",
    editsSince(beforeMove).some((e) => e.kind === "move-node"),
    kindsSince(beforeMove),
  );

  const beforeExtract = editCalls().length;
  const toPromote = lines()[1] ?? lines()[0];
  if (toPromote) key(toPromote, "Enter", { ctrlKey: true });
  await settle(20);
  check(
    "sending a line to the plan becomes an extract-to-task edit",
    editsSince(beforeExtract).some((e) => e.kind === "extract-to-task"),
    kindsSince(beforeExtract),
  );

  /* ---------------------------------------------------------------------- */
  /* 4f. Somebody else's edit arriving                                      */
  /* ---------------------------------------------------------------------- */

  // The first version of this app in which two desktops share an outline. What
  // makes it true is that the frontend now fetches the merged document when the
  // cursor moves, and gives each tab its own part of it.

  pickMode("idea");
  await settle(10);

  const node = (id: string, text: string, position: string, children: unknown[] = []) => ({
    id,
    position,
    fields: { type: "idea", text },
    children,
  });

  // A document for the tab that is open, with a line nothing on this machine
  // typed.
  V().remote({
    cursor: 99,
    tree: [
      {
        id: "tab-a",
        position: "m",
        fields: { text: "Work" },
        children: [
          node("r1", "written on another machine", "m"),
          node("r2", "and a second one", "n", [node("r3", "nested under it", "m")]),
        ],
      },
      // Another tab's outline, which must not appear in this one.
      {
        id: "tab-b",
        position: "n",
        fields: { text: "planning" },
        children: [node("x1", "belongs to the other tab", "m")],
      },
    ],
    detached: [],
  });
  await settle(25);

  const shown = () => lines().map((l) => l.value);
  check(
    "a colleague's line appears without anybody reloading",
    shown().includes("written on another machine"),
    shown().join(" | ").slice(0, 90),
  );
  check("nesting comes with it", shown().includes("nested under it"));
  check(
    "another tab's outline stays in the other tab",
    !shown().includes("belongs to the other tab"),
    shown().join(" | ").slice(0, 90),
  );

  // A line being typed into is not replaced by a document arriving. The draft
  // is what is on screen until the caret leaves, which is the property that
  // makes remote edits survivable while somebody is mid-word.
  // Within the typing window on purpose. A draft is what is on screen until it
  // settles, and this is the moment a document arriving would be most
  // destructive -- mid-word, before the edit has been committed to anything.
  // What happens to a *committed* line that changes under a caret is task 06's
  // question, and it has a different answer: mark it, do not silently keep
  // either side.
  const typing = lines()[0];
  if (typing) {
    typing.focus();
    typing.value = "half a thought";
    typing.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(2);
  }
  V().remote({
    cursor: 100,
    tree: [
      {
        id: "tab-a",
        position: "m",
        fields: { text: "Work" },
        children: [node("r1", "changed under the caret", "m")],
      },
    ],
    detached: [],
  });
  await settle(8);
  check(
    "a document arriving does not overwrite what is being typed",
    lines()[0]?.value === "half a thought",
    lines()[0]?.value ?? "gone",
  );

  // And once it settles, what was typed is what goes out -- the draft was not
  // a way of ignoring the document, only of not losing a word to it.
  await new Promise((r) => setTimeout(r, 900));
  await settle(10);
  check(
    "and what was typed is still what this machine sent",
    editsSince(editCalls().length - 1).some((e) => e.kind === "set-fields"),
    kindsSince(editCalls().length - 1),
  );

  /* ---------------------------------------------------------------------- */
  /* 4g. A line that changes under the caret is marked, not taken           */
  /* ---------------------------------------------------------------------- */

  pickMode("idea");
  await settle(10);

  const marker = () => $("span.changed .what");
  const useTheirs = () =>
    $$<HTMLButtonElement>("span.changed button").find((b) => b.textContent?.trim() === "Use theirs") ?? null;

  const caretRow = lines()[0];
  check("there is a line to put the caret in", !!caretRow);
  if (caretRow) {
    caretRow.focus();
    caretRow.value = "mine, mid-word";
    caretRow.dispatchEvent(new Event("input", { bubbles: true }));
    // Past the debounce on purpose. Before this task, a settled draft was
    // deleted and the next document to arrive replaced what was on screen --
    // which is the failure this is about, and it needs the draft to be gone
    // for the old behaviour to show.
    await new Promise((r) => setTimeout(r, 900));
    await settle(10);
  }

  const caretId = nodeIds()[0];
  V().remote({
    cursor: 501,
    tree: [
      {
        id: "tab-a",
        position: "m",
        fields: { text: "Work" },
        children: [
          { id: caretId, position: "m", fields: { type: "idea", text: "theirs, arriving" }, children: [] },
        ],
      },
    ],
    detached: [],
  });
  await settle(25);

  check(
    "what was typed is still in the box",
    lines()[0]?.value === "mine, mid-word",
    lines()[0]?.value ?? "gone",
  );
  check("and the row says the line changed elsewhere", !!marker(), marker()?.textContent?.trim() ?? "no marker");
  check(
    "naming what it says now",
    (marker()?.textContent ?? "").includes("theirs, arriving"),
    marker()?.textContent?.trim() ?? "",
  );
  // Nobody, again. The marker is one of the three places a name would be easy
  // to add and wrong to have.
  check(
    "and naming nobody",
    !/\bby\b|author|actor|machine|someone|somebody/i.test(marker()?.textContent ?? ""),
  );

  check("taking theirs is offered, not done", !!useTheirs());
  useTheirs()?.click();
  await settle(15);
  check(
    "taking theirs swaps the line",
    lines()[0]?.value === "theirs, arriving",
    lines()[0]?.value ?? "gone",
  );
  check("and the marker goes with it", !marker());

  /* ---------------------------------------------------------------------- */
  /* 4h. Work mode takes the next task                                      */
  /* ---------------------------------------------------------------------- */

  pickMode("work");
  await settle(12);

  const nextBox = () => $("section.next");
  const nextButton = () => $<HTMLButtonElement>("section.next button");

  check("with nothing on the plan, nothing is offered", !nextBox());

  V().setNextTask({
    id: "t9",
    text: "Mount the static handler",
    status: "todo",
    fromText: "Serve the viewer",
  });
  await settle(20);

  check("the next task is named", !!nextBox(), nextBox()?.textContent?.replace(/\s+/g, " ").slice(0, 80) ?? "");
  check(
    "and the idea it came from with it",
    (nextBox()?.textContent ?? "").includes("Serve the viewer"),
  );
  // A button that does not say what it will run is one nobody presses twice.
  check(
    "the button says what it will run",
    (nextButton()?.textContent ?? "").includes("Mount the static handler"),
    nextButton()?.textContent?.trim() ?? "no button",
  );

  nextButton()?.click();
  await settle(20);
  check("starting it runs the one that was offered", V().started()?.text === "Mount the static handler");
  check("and it stops being offered once it is running", !nextBox());

  /* ---------------------------------------------------------------------- */
  /* 4i. A transcript per mode, in one console                              */
  /* ---------------------------------------------------------------------- */

  const composerOf = () => $<HTMLTextAreaElement>("textarea");

  pickMode("idea");
  await settle(10);
  const consoleBox = composerOf();
  const ideaDraft = "a half-typed thought about shape";
  if (consoleBox) {
    consoleBox.value = ideaDraft;
    consoleBox.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(6);
  }

  pickMode("planning");
  await settle(10);
  // The console must not have been torn down: that is what keeps scroll, focus
  // and a draft alive across a switch, and a {#key} on the mode would undo it
  // in one line.
  check("the same composer element after switching mode", composerOf() === consoleBox);
  check("but planning's composer is empty", composerOf()?.value === "", composerOf()?.value ?? "gone");

  const planningDraft = "and one about execution";
  if (composerOf()) {
    composerOf()!.value = planningDraft;
    composerOf()!.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(6);
  }

  pickMode("idea");
  await settle(10);
  check(
    "coming back to idea finds what was typed there",
    composerOf()?.value === ideaDraft,
    composerOf()?.value ?? "gone",
  );

  pickMode("planning");
  await settle(10);
  check("and planning still has its own", composerOf()?.value === planningDraft, composerOf()?.value ?? "gone");

  // Output belongs to the conversation that asked for it. Planning is on screen
  // when this run starts, so it is planning's -- and it stays planning's after
  // switching away, because a run takes a while and nobody watches it.
  const answer = "this answer belongs to planning";
  V().stream(answer);
  await settle(20);
  const pageText = () => document.body.innerText;
  check("the answer appears in the mode that asked", pageText().includes(answer));

  pickMode("work");
  await settle(20);
  check(
    "and not in the one that did not",
    !pageText().includes(answer),
    pageText().includes(answer) ? "work has planning's output" : "",
  );
  V().endStream();
  await settle(8);

  /* ---------------------------------------------------------------------- */
  /* 4j. Links across branches, and regions                                 */
  /* ---------------------------------------------------------------------- */

  // A mindmap is not a tree. Until there is a canvas these are the only place a
  // link is visible at all, so they are on the row.

  pickMode("idea");
  await settle(12);

  // An outline with something in it. Earlier sections left this tab holding a
  // single line, and a link needs two ends.
  V().remote({
    cursor: 900,
    tree: [
      {
        id: "tab-a",
        position: "m",
        fields: { text: "Work" },
        children: [
          { id: "g1", position: "m", fields: { type: "idea", text: "a static handler" }, children: [] },
          { id: "g2", position: "n", fields: { type: "idea", text: "where the files come from" }, children: [] },
          { id: "g3", position: "o", fields: { type: "idea", text: "somewhere else entirely" }, children: [] },
        ],
      },
    ],
    detached: [],
  });
  await settle(25);

  const outlineLines = () => lines().map((l) => l.value);
  check("there are at least two lines to link", lines().length >= 2, outlineLines().join(" | "));

  const linkButtons = () =>
    $$<HTMLButtonElement>("button").filter((b) => b.textContent?.trim() === "Link");
  check("a row offers to link", linkButtons().length > 0);

  linkButtons()[0]?.click();
  await settle(10);
  const picker = () => $<HTMLInputElement>(".picker input");
  check("linking opens a picker, not a box for an id", !!picker());

  const candidate = () => $$<HTMLButtonElement>(".candidates button")[0] ?? null;
  const candidateText = candidate()?.textContent?.trim() ?? "";
  check("the picker lists lines to link to", !!candidate(), candidateText);
  candidate()?.click();
  await settle(15);

  const relations = () => $$("p.relations").map((el) => el.textContent ?? "").join(" | ");
  check("the link shows on the row", relations().includes("links to"), relations().slice(0, 100));
  // Both ends, because one relationship should not look like two different
  // things depending on which row you are reading.
  check(
    "and on the row at the other end",
    $$("p.relations").length >= 2,
    `${$$("p.relations").length} rows show a relation`,
  );

  const unlink = () => $$<HTMLButtonElement>("p.relations button").find((b) => b.textContent?.trim() === "\u00d7");
  check("a link can be removed", !!unlink());
  unlink()?.click();
  await settle(15);
  check("and removing it takes it off both rows", !relations().includes("links to"), relations().slice(0, 80));

  // Regions. The caret has to be somewhere for a branch to be grouped.
  lines()[0]?.focus();
  await settle(8);
  const groupButton = () =>
    $$<HTMLButtonElement>("button").find((b) => b.textContent?.trim() === "Group branch") ?? null;
  check("grouping a branch is offered when the caret is in one", !!groupButton());

  groupButton()?.click();
  await settle(8);
  const namer = () => $<HTMLInputElement>(".grouping input");
  check("a region is named when it is made, not afterwards", !!namer());
  if (namer()) {
    namer()!.value = "Networking";
    namer()!.dispatchEvent(new Event("input", { bubbles: true }));
    await settle(6);
    $<HTMLButtonElement>(".grouping button")?.click();
    await settle(15);
  }
  check("the branch says which region it is in", relations().includes("Networking"), relations().slice(0, 100));

  // And neither a link nor a region is an outline line.
  check(
    "links and regions are not lines in the outline",
    !outlineLines().includes("Networking"),
    outlineLines().join(" | ").slice(0, 90),
  );

  // A proposal can make them too, and reads as English when it does.
  V().propose({
    summary: "Relate the two handlers.",
    ops: [
      { kind: "link", node: nodeIds()[0], other: nodeIds()[1] },
      { kind: "group", node: nodeIds()[2], name: "Storage" },
    ],
  });
  await settle(20);

  const proposedRows = () => $$("section.review ol li").map((el) => el.textContent ?? "").join(" | ");
  check("a proposal can link and group", !!$("section.review"), proposedRows().slice(0, 100));
  check("and says so in English", proposedRows().includes("Link"), proposedRows().slice(0, 100));
  check("naming the region it would make", proposedRows().includes("Storage"));

  const applyIt = () =>
    $$<HTMLButtonElement>("section.review footer button").find((b) =>
      (b.textContent ?? "").startsWith("Apply"),
    ) ?? null;
  applyIt()?.click();
  await settle(20);
  check(
    "applying a proposed link puts it on the row",
    $$("p.relations").map((el) => el.textContent ?? "").join(" ").includes("links to"),
  );

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
