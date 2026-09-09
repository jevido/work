import { MONITOR_TEXT_LINES } from "./world";

/**
 * The last few lines of what an agent is writing, wrapped to fit a monitor.
 *
 * Two things keep this cheap enough to sit under the render loop. The wrap
 * width is measured in world units, so it never changes with the window and a
 * resize costs no re-layout at all. And streamed text only ever grows, so each
 * update folds in the characters that just arrived and leaves the lines above
 * them alone -- which is also why the readout scrolls like a terminal instead
 * of reflowing under itself every time a word lands.
 *
 * Nothing here is reactive and nothing here is called from the draw loop: the
 * loop only reads `lines` and `current`, both already the right shape.
 */
export class MonitorTail {
  /** Completed lines, oldest first. At most one fewer than fits the glass. */
  readonly lines: string[] = [];

  /** The line being written. Drawn last, with the caret after it. */
  current = "";

  /**
   * Which run of prose the folded characters came from. A turn's text arrives
   * in several runs -- a tool call interrupts one and the next words start
   * another -- and the id is how a continuation is told from a new run.
   */
  private sourceId = "";

  /** How many characters of the current run have been folded in. */
  private consumed = 0;

  /** True when there is anything worth drawing. */
  get active(): boolean {
    return this.current.length > 0 || this.lines.length > 0;
  }

  /**
   * Folds in whatever is new, and nothing else: text that has not grown since
   * the last call costs one length comparison and no work. Returns true when
   * the visible lines changed.
   */
  update(sourceId: string, text: string, columns: number): boolean {
    let changed = false;

    if (sourceId !== this.sourceId) {
      // A new run of prose after a tool call is a line break, not a fresh
      // screen: what came before it is still the last thing they said.
      changed = this.breakLine();
      this.sourceId = sourceId;
      this.consumed = 0;
    }

    if (text.length <= this.consumed) return changed;
    this.fold(text, columns);
    this.consumed = text.length;
    return true;
  }

  /** Blanks the readout. Returns true if there was something to blank. */
  clear(): boolean {
    if (!this.active && !this.sourceId) return false;
    this.lines.length = 0;
    this.current = "";
    this.sourceId = "";
    this.consumed = 0;
    return true;
  }

  private fold(text: string, columns: number): void {
    for (let i = this.consumed; i < text.length; i++) {
      const ch = text[i];

      if (ch === "\n") {
        // Blank lines are dropped rather than drawn. Prose is full of them and
        // the glass is only three rows tall, so spending one on nothing costs
        // a third of the readout.
        this.breakLine();
        continue;
      }
      if (ch === "\r") continue;

      this.current += ch === "\t" ? " " : ch;
      if (this.current.length <= columns) continue;

      // Break at the last space so words stay whole. A word wider than the
      // glass gets cut mid-way, which is what a terminal would do with it.
      const space = this.current.lastIndexOf(" ");
      if (space > 0) {
        this.pushLine(this.current.slice(0, space));
        this.current = this.current.slice(space + 1);
      } else {
        this.pushLine(this.current.slice(0, columns));
        this.current = this.current.slice(columns);
      }
    }
  }

  private breakLine(): boolean {
    if (!this.current) return false;
    this.pushLine(this.current);
    this.current = "";
    return true;
  }

  /** Keeps only as much scrollback as the glass can show above the live line. */
  private pushLine(line: string): void {
    this.lines.push(line);
    while (this.lines.length > MONITOR_TEXT_LINES - 1) this.lines.shift();
  }
}
