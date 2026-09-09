/**
 * Drives the change review now that it is a window rather than a block in the
 * transcript, and reports what actually happened.
 *
 * The point of the move is measurable, so it is measured: the conversation's
 * column must not change width or get shorter when a run lands changes in it,
 * and the diff must end up wider than the column it used to be squeezed into.
 */
const results: { name: string; pass: boolean; detail: string }[] = [];
const V = () => (window as any).__verify;

function check(name: string, pass: boolean, detail = "") {
  results.push({ name, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"} ${name}${detail ? " -- " + detail : ""}`);
}

const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
async function settle(n = 12) { for (let i = 0; i < n; i++) await frame(); }

const console_ = () => document.querySelector<HTMLElement>("section.console")!;
const scroller = () => document.querySelector<HTMLElement>("section.console .scroller")!;
const pill = () => document.querySelector<HTMLButtonElement>("button.changes");
const composer = () => document.querySelector<HTMLTextAreaElement>("section.console textarea")!;
const dialog = () => document.querySelector<HTMLElement>('[role="dialog"][aria-label="Changes on disk"]');
const rows = () => [...(dialog()?.querySelectorAll<HTMLElement>(".file") ?? [])];
const rowFor = (path: string) =>
  rows().find((r) => (r.querySelector(".path")?.textContent ?? "").trim() === path);
const openRowPath = () => {
  const row = rows().find((r) => r.querySelector(".diff, .binary"));
  return (row?.querySelector(".path")?.textContent ?? "").trim() || null;
};

/**
 * Presses an element the way a mouse does: pointerdown first.
 *
 * The dialog's header is a drag handle, and a bare click() would skip the very
 * event that could swallow a press on the close button sitting inside it.
 */
function press(el: HTMLElement) {
  const r = el.getBoundingClientRect();
  const at = { clientX: r.left + r.width / 2, clientY: r.top + r.height / 2 };
  const pointer = {
    bubbles: true, pointerId: 1, isPrimary: true, button: 0, buttons: 1,
    pointerType: "mouse", ...at,
  } as any;
  el.dispatchEvent(new PointerEvent("pointerdown", pointer));
  el.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0, buttons: 1, ...at }));
  el.dispatchEvent(new PointerEvent("pointerup", { ...pointer, buttons: 0 }));
  el.dispatchEvent(new MouseEvent("mouseup", { bubbles: true, button: 0, buttons: 0, ...at }));
  el.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0, ...at }));
}

function escape(el: HTMLElement) {
  el.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
}

const THREE = [
  { path: "internal/workbench/workbench.go", status: "modified", patch: patch(40) },
  { path: "frontend/src/components/ChangeReview.svelte", status: "modified", patch: patch(6) },
  { path: "internal/config/config.go", status: "added", patch: patch(4) },
];

/** A patch tall enough that reading it scrolls the list it sits in. */
function patch(hunks: number): string {
  let out = "--- a/x\n+++ b/x\n";
  for (let i = 0; i < hunks; i++) {
    out += `@@ -${i * 10 + 1},3 +${i * 10 + 1},4 @@\n context ${i}\n-gone ${i}\n+new ${i}\n+also ${i}\n`;
  }
  return out;
}

async function run() {
  await settle(30);
  check("the app came up", !!document.querySelector("canvas") && !!console_());
  if (!console_()) return;

  V().started();
  // Enough of a conversation to overflow the column.
  //
  // Deliberate: scrollHeight only reports content height once the content is
  // taller than the box. On a two-line transcript it reports the box, so the
  // header growing by a pixel when the review button appears would read as the
  // transcript shrinking -- measuring the wrong thing and failing for it.
  for (let i = 0; i < 120; i++) V().say(`Line ${i} of looking at the workbench.\n`);
  await settle(12);

  // --- 1. The conversation keeps its column ------------------------------
  const widthBefore = scroller().clientWidth;
  const contentBefore = scroller().scrollHeight;
  const headerBefore = console_().querySelector<HTMLElement>("header")!.getBoundingClientRect().height;
  check("the transcript overflows its column, so its height is its content",
    contentBefore > scroller().clientHeight,
    `${contentBefore}px of content in a ${scroller().clientHeight}px box`);

  V().land(THREE);
  await settle(20);

  check("changes do not put anything in the transcript",
    !scroller().querySelector(".file") && !scroller().querySelector(".revert"),
    scroller().querySelector(".file") ? "a file row is still inline" : "");
  check("the conversation's column is not narrowed by them",
    scroller().clientWidth === widthBefore, `${widthBefore} -> ${scroller().clientWidth}px`);
  check("and the conversation is not pushed around by them",
    scroller().scrollHeight === contentBefore, `${contentBefore} -> ${scroller().scrollHeight}px`);
  // The button appearing must not resize the header it appears in: the header
  // sits above the transcript, so a header that grows shortens the transcript
  // and shifts every line of the conversation down to announce itself.
  const headerAfter = console_().querySelector<HTMLElement>("header")!.getBoundingClientRect().height;
  check("the header does not change height when the affordance appears",
    headerAfter === headerBefore, `${headerBefore} -> ${headerAfter}px`);

  // --- 2. The affordance it left behind ----------------------------------
  check("a review affordance appears in the console header", !!pill());
  if (!pill()) return;
  check("it is in the header, not the transcript",
    !!pill()!.closest("header") && !scroller().contains(pill()!));
  check("it says how many files changed",
    /3\s+changed files/.test(pill()!.textContent ?? ""), (pill()!.textContent ?? "").trim());
  check("it reads as news until it has been opened",
    pill()!.classList.contains("unseen") && !!pill()!.querySelector(".pip"));
  check("it announces itself to a screen reader",
    (document.querySelector('[role="status"]')?.textContent ?? "").includes("review pending"),
    (document.querySelector('[role="status"]')?.textContent ?? "").trim());
  check("it says it opens a dialog", pill()!.getAttribute("aria-haspopup") === "dialog");
  check("no diff is on screen before it is asked for", !dialog());

  // --- 3. Opening it ------------------------------------------------------
  press(pill()!);
  await settle(20);
  check("pressing it opens the review", !!dialog());
  if (!dialog()) return;
  check("the affordance says the dialog is open", pill()!.getAttribute("aria-expanded") === "true");
  check("opening it is reading the news",
    !pill()!.classList.contains("unseen") && !pill()!.querySelector(".pip"));

  const dialogWidth = dialog()!.getBoundingClientRect().width;
  check("the diff is no longer held to the console's width",
    dialogWidth > scroller().clientWidth * 1.5,
    `${Math.round(dialogWidth)}px dialog vs ${scroller().clientWidth}px column`);
  check("it is a window over the app, not inside the console",
    !console_().contains(dialog()!) && getComputedStyle(dialog()!).position === "fixed");
  check("nothing behind it is disabled -- the run carries on",
    !composer().disabled && !!document.querySelector("canvas"));

  check("all three files are listed", rows().length === 3, `${rows().length} rows`);
  check("it opens on a diff rather than on rows to be clicked",
    openRowPath() === THREE[0].path, String(openRowPath()));
  check("the row being read stays put while its diff scrolls",
    getComputedStyle(rowFor(THREE[0].path)!.querySelector(".row")!).position === "sticky",
    getComputedStyle(rowFor(THREE[0].path)!.querySelector(".row")!).position);

  // A tall diff has to scroll the window, not a box inside it: two scrollers a
  // few pixels apart mean the wheel does different things depending on where
  // the pointer happens to be.
  const lines = dialog()!.querySelector<HTMLElement>(".diff .lines")!;
  const body = dialog()!.querySelector<HTMLElement>(".body")!;
  check("a tall diff is not given its own scrollbar inside the window",
    lines.scrollHeight <= lines.clientHeight + 1,
    `${lines.scrollHeight}px of diff in a ${lines.clientHeight}px box`);
  check("the window is what scrolls it", body.scrollHeight > body.clientHeight,
    `${body.scrollHeight}px in a ${body.clientHeight}px window`);

  // --- 4. Reading a different one ----------------------------------------
  press(rowFor(THREE[2].path)!.querySelector<HTMLElement>(".open")!);
  await settle(8);
  check("opening another file's diff shows it", openRowPath() === THREE[2].path, String(openRowPath()));
  check("and closes the one that was open", rows().filter((r) => r.querySelector(".diff")).length === 1);

  // --- 5. A file landing mid-read -----------------------------------------
  const FOUR = [...THREE, { path: "main.go", status: "modified", patch: patch(3) }];
  V().land(FOUR);
  await settle(20);
  check("a file landing while the review is open joins the list", rows().length === 4);
  check("it does not move the diff being read out from under you",
    openRowPath() === THREE[2].path, String(openRowPath()));
  check("and the affordance behind it goes back to reading as news",
    pill()!.classList.contains("unseen"));

  // --- 6. Escape ----------------------------------------------------------
  escape(dialog()!);
  await settle(10);
  check("Escape puts the window away", !dialog());
  check("Escape does not cancel the run",
    !V().calls.some((c: any) => c.id === V().CANCEL), "no Cancel call");
  check("focus goes back to what opened it", document.activeElement === pill(),
    (document.activeElement as HTMLElement)?.className ?? "nothing focused");

  // --- 7. Reverting -------------------------------------------------------
  // One file the backend refuses, the way it does mid-run.
  V().land(FOUR, "main.go");
  await settle(20);
  press(pill()!);
  await settle(15);
  check("the review reopens", !!dialog() && rows().length === 4);
  if (!dialog()) return;

  press(rowFor("main.go")!.querySelector<HTMLElement>(".revert")!);
  await settle(30);
  check("a refused revert says so on the row it was asked for",
    !!rowFor("main.go")?.querySelector(".error"),
    rowFor("main.go")?.querySelector(".error")?.textContent?.trim() ?? "no error shown");
  check("a refused revert leaves the file in the list", rows().length === 4);

  press(rowFor(THREE[1].path)!.querySelector<HTMLElement>(".revert")!);
  await settle(30);
  check("a revert takes the file out of the list", rows().length === 3 && !rowFor(THREE[1].path));
  check("the backend was actually asked", !V().treeNow().some((c: any) => c.path === THREE[1].path));
  check("the affordance follows the count down",
    /3\s+changed files/.test(pill()?.textContent ?? ""), (pill()?.textContent ?? "").trim());

  // --- 8. Reverting the last one ------------------------------------------
  for (const path of ["main.go", THREE[0].path, THREE[2].path]) {
    const row = rowFor(path);
    if (!row) continue;
    // main.go is the refused one; land a tree that does not refuse it.
    if (path === "main.go") { V().land(V().treeNow()); await settle(15); }
    press(rowFor(path)!.querySelector<HTMLElement>(".revert")!);
    await settle(30);
  }
  check("reverting everything empties the list", rows().length === 0, `${rows().length} rows`);
  check("the window stays and says the job is done rather than vanishing",
    !!dialog() && !!dialog()!.querySelector(".empty"),
    dialog()?.querySelector(".empty")?.textContent?.trim() ?? "no message");
  check("the affordance is gone once there is nothing to review", !pill());

  escape(dialog()!);
  await settle(10);
  check("closing it with its opener gone hands focus to the composer",
    !dialog() && document.activeElement === composer(),
    (document.activeElement as HTMLElement)?.tagName ?? "nothing focused");
}

run()
  .catch((err) => check("harness crashed", false, String(err?.stack ?? err)))
  .finally(() => {
    void fetch("http://127.0.0.1:7788/", {
      method: "POST", headers: { "content-type": "text/plain" }, body: JSON.stringify(results),
    });
  });
