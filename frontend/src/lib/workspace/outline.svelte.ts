/**
 * The outline's keyboard, and the focus bookkeeping that goes with it.
 *
 * Kept out of the components because the recursive one that draws a line has
 * to reach it, and threading a dozen handlers down through every level of the
 * tree as props is how a recursive component turns into a signature. It is a
 * context object: created by the outline, read by each row.
 *
 * The other half of what is here is focus. Every structural edit rebuilds part
 * of the tree, and a line that moves to another parent is a different element
 * afterwards -- so "keep the caret where it was" cannot be a DOM reference. It
 * is a node id and a caret position, asked for before the edit and applied by
 * whichever input turns up wearing that id.
 */
import { createContext, tick } from "svelte";
import type { Row } from "./model";
import type { Workspace } from "./workspace.svelte";

/** Where to put the caret in the line being focused. */
export type Caret = "start" | "end";

export interface OutlineContext {
  workspace: Workspace;
  /** Registers a line's input, and applies a focus that was waiting for it. */
  register(id: string, el: HTMLInputElement): () => void;
  /** Everything the keyboard does. One handler, because the keys interact. */
  keydown(event: KeyboardEvent, row: Row): void;
  /** True for the line the caret is in, so the row can look active. */
  isFocused(id: string): boolean;
  focused(id: string | null): void;
  toggle(row: Row): void;
  promote(row: Row): void;
  remove(row: Row): void;
}

/**
 * The row's way to reach the outline it is in.
 *
 * `createContext` hands back a get/set pair rather than a key, so there is no
 * string to typo and no `as` at the other end. The rows are recursive, and
 * threading a dozen handlers down through every level as props is how a
 * recursive component turns into a signature.
 */
export const [getOutline, setOutline] = createContext<OutlineContext>();

/**
 * The outline's behaviour, minus the markup.
 *
 * Not a component so that the announcements and the focus queue can be read
 * and driven from the outline's own template as well as from the rows.
 */
export class OutlineKeys {
  /** What was just done, for the live region. Cleared as soon as it is read. */
  said = $state("");

  /** The line the caret is in, or null. Drives nothing but the row's styling. */
  active = $state<string | null>(null);

  #workspace: Workspace;
  #inputs = new Map<string, HTMLInputElement>();
  #want: { id: string; caret: Caret } | null = null;

  constructor(workspace: Workspace) {
    this.#workspace = workspace;
  }

  /**
   * Where the caret goes when somebody presses Escape.
   *
   * The outline swallows Tab in both directions, which is what an outliner is
   * for and also means blurring out of a line leaves focus on the document
   * body -- the next Tab restarts from the top of the window. So Escape hands
   * focus to the outline's own container instead, and Tab from there carries
   * on past the outline the way it would from anything else.
   */
  exit: HTMLElement | null = null;

  context(): OutlineContext {
    return {
      workspace: this.#workspace,
      register: (id, el) => this.register(id, el),
      keydown: (event, row) => this.keydown(event, row),
      isFocused: (id) => this.active === id,
      focused: (id) => {
        this.active = id;
      },
      toggle: (row) => this.toggle(row),
      promote: (row) => this.promote(row),
      remove: (row) => this.remove(row),
    };
  }

  register(id: string, el: HTMLInputElement): () => void {
    this.#inputs.set(id, el);
    // A line created a moment ago asked for the caret before it existed. This
    // is the moment it does.
    if (this.#want?.id === id) this.#apply();
    return () => {
      if (this.#inputs.get(id) === el) this.#inputs.delete(id);
    };
  }

  /**
   * Asks for the caret to land in a line.
   *
   * Applied after Svelte has updated the DOM, not now. Every caller is in the
   * middle of a keydown handler that has just restructured the outline, and
   * the input for that line is about to be moved or replaced -- focusing the
   * element that is still on screen means focusing the one that is about to
   * be taken off it. WebKit blurs an element when it is re-inserted, so the
   * caret ends up on the document body: pressing Alt+Down twice moved the
   * line twice and lost the caret on the second one.
   *
   * Safe to call for a line that does not exist yet, too. The request is held
   * until an input registers under that id, which is what happens when the
   * new line is drawn -- see register().
   */
  focus(id: string, caret: Caret = "end"): void {
    this.#want = { id, caret };
    void tick().then(() => this.#apply());
  }

  #apply(): void {
    const want = this.#want;
    if (!want) return;
    const el = this.#inputs.get(want.id);
    if (!el) return;
    this.#want = null;
    el.focus();
    const at = want.caret === "start" ? 0 : el.value.length;
    el.setSelectionRange(at, at);
    this.active = want.id;
  }

  /**
   * Says something to a screen reader.
   *
   * Every structural key does one. An outline is a shape, and the whole of
   * what indent, move and fold do is change a shape somebody may not be able
   * to see -- without this they are silent keys that appear to do nothing.
   *
   * The space is not decoration: the live region is only announced when its
   * text changes, and pressing Tab twice produces the same sentence twice.
   */
  say(text: string): void {
    this.said = this.said === text ? `${text} ` : text;
  }

  keydown(event: KeyboardEvent, row: Row): void {
    // An input method has the keyboard: Enter commits a candidate, and reading
    // that as "new line" splits the word somebody was choosing.
    if (event.isComposing) return;

    const id = row.node.id;
    const input = event.currentTarget as HTMLInputElement;
    const ws = this.#workspace;

    switch (event.key) {
      case "Enter": {
        event.preventDefault();
        if (event.ctrlKey || event.metaKey) {
          this.promote(row);
          return;
        }
        const created = ws.insertAfter(id);
        if (created) this.focus(created, "start");
        return;
      }

      case "Tab": {
        event.preventDefault();
        const moved = event.shiftKey ? ws.outdent(id) : ws.indent(id);
        if (!moved) {
          this.say(
            event.shiftKey
              ? "Already at the top level."
              : "Nothing above this line to nest it under.",
          );
          return;
        }
        // Re-read the depth rather than adding one to the old: an outdent from
        // a deep branch lands somewhere the arithmetic would not predict.
        this.focus(id, caretOf(input));
        this.say(`${event.shiftKey ? "Outdented" : "Indented"} to level ${this.#levelOf(id)}.`);
        return;
      }

      case "ArrowUp":
      case "ArrowDown": {
        const down = event.key === "ArrowDown";
        if (event.altKey) {
          event.preventDefault();
          if (!ws.moveBy(id, down ? 1 : -1)) {
            this.say(down ? "Already last among its siblings." : "Already first among its siblings.");
            return;
          }
          this.focus(id, caretOf(input));
          this.say(`Moved ${down ? "down" : "up"}.`);
          return;
        }
        // A single-line input does nothing with up and down of its own, so
        // there is no caret movement to preserve and no need to check where
        // the caret is first.
        event.preventDefault();
        this.#step(row, down ? 1 : -1, caretOf(input));
        return;
      }

      case "ArrowLeft":
      case "ArrowRight": {
        if (!event.altKey) return;
        event.preventDefault();
        const open = event.key === "ArrowRight";
        if (row.node.children.length === 0) {
          this.say("Nothing under this line to fold.");
          return;
        }
        if (!ws.setCollapsed(row.node.id, !open)) {
          this.say(open ? "Already unfolded." : "Already folded.");
          return;
        }
        this.say(
          open
            ? `Unfolded, ${count(row.descendants, "line")} shown.`
            : `Folded, ${count(row.descendants, "line")} hidden.`,
        );
        return;
      }

      case "Backspace": {
        // Only on an empty line, with the caret at the very start. Anywhere
        // else it is the browser's -- deleting a character is what Backspace
        // is for, and taking it over would be the worst kind of clever.
        if (input.value !== "" || input.selectionStart !== 0) return;
        event.preventDefault();

        // A line with lines under it takes a second press, held down with
        // Shift. Backspace is a key people hold, and eating a branch of forty
        // lines on the third repeat is how an outline loses an afternoon. The
        // way out is said here rather than in the help line, because the
        // moment somebody presses this is the only moment they will read it.
        if (row.node.children.length > 0 && !event.shiftKey) {
          this.say(
            `This line has ${count(row.descendants, "line")} under it. Shift+Backspace deletes the branch.`,
          );
          return;
        }
        this.remove(row);
        return;
      }

      case "Escape":
        event.preventDefault();
        this.leave(input);
        return;
    }
  }

  toggle(row: Row): void {
    const folded = !isFolded(row);
    this.#workspace.setCollapsed(row.node.id, folded);
    this.say(
      folded
        ? `Folded, ${count(row.descendants, "line")} hidden.`
        : `Unfolded, ${count(row.descendants, "line")} shown.`,
    );
  }

  promote(row: Row): void {
    const ws = this.#workspace;
    const already = ws.taskFor(row.node.id);
    if (already) {
      this.say("Already on the plan.");
      return;
    }
    if (ws.text(row.node.id).trim() === "") {
      this.say("An empty line cannot go on the plan.");
      return;
    }
    ws.promote(row.node.id);
    this.say(`Added to the plan: ${ws.text(row.node.id).trim()}`);
  }

  remove(row: Row): void {
    const ws = this.#workspace;
    const back = this.#neighbour(row, -1) ?? this.#neighbour(row, 1);
    const taken = row.descendants;
    if (!ws.remove(row.node.id)) return;
    this.say(
      taken === 0 ? "Line deleted." : `Line deleted, with ${count(taken, "line")} under it.`,
    );
    if (back) this.focus(back, "end");
  }

  /** Hands focus to the outline's container, so Tab carries on past it. */
  leave(from: HTMLElement): void {
    this.active = null;
    if (this.exit) this.exit.focus();
    else from.blur();
    this.say("Left the outline.");
  }

  /** Moves the caret to the next or previous line on screen. */
  #step(row: Row, delta: -1 | 1, caret: Caret): void {
    const next = this.#neighbour(row, delta);
    if (next) this.focus(next, caret);
  }

  #neighbour(row: Row, delta: -1 | 1): string | null {
    const rows = this.#workspace.rows;
    const at = rows.findIndex((r) => r.node.id === row.node.id);
    if (at < 0) return null;
    return rows[at + delta]?.node.id ?? null;
  }

  #levelOf(id: string): number {
    const row = this.#workspace.rows.find((r) => r.node.id === id);
    return (row?.depth ?? 0) + 1;
  }
}

function isFolded(row: Row): boolean {
  return row.node.fields.collapsed === true;
}

/**
 * Which end of the line the caret is nearer.
 *
 * Carried across a move so that walking down a list with the caret at the end
 * of each line does not silently reset it to the start halfway through.
 */
function caretOf(input: HTMLInputElement): Caret {
  return (input.selectionStart ?? 0) === 0 ? "start" : "end";
}

function count(n: number, noun: string): string {
  return `${n} ${n === 1 ? noun : `${noun}s`}`;
}
