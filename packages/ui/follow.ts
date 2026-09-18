/**
 * Keeping a transcript scrolled to its newest line.
 *
 * Both the console and the desk panel show a conversation that grows while it
 * is being read, and both want the same thing: follow the stream, and stop the
 * moment the reader scrolls up.
 *
 * Watched at the DOM rather than through the entries. The alternative -- an
 * effect that reads every part of every turn so that it reruns when one of
 * them grows -- costs the whole transcript on every frame of a stream, which
 * makes a long conversation stream slower than a fresh one for no reason
 * anybody can see. An observer is woken by the change itself, so following six
 * hundred parts costs what following none does.
 */

/**
 * Marks an element as something the reader opened, not something the run
 * produced. Put it on a disclosure body inside a followed scroller; see
 * ToolRow.svelte, which is the only thing that sets it.
 */
const DISCLOSURE = "data-expand";

/**
 * Whether a batch of mutations is nothing but a disclosure opening or closing.
 *
 * Expanding a tool row grows the transcript exactly as an arriving token does,
 * and geometry cannot tell them apart: in both cases the content got taller at
 * the end. The difference is who asked. Following a stream to the bottom is
 * what the reader wants; doing it when they expand a long result scrolls the
 * top of that result -- the part they opened it for -- straight out of view.
 */
function onlyDisclosure(records: MutationRecord[]): boolean {
  return records.every((record) => {
    if (record.type !== "childList") return false;
    const changed = [...record.addedNodes, ...record.removedNodes];
    return (
      changed.length > 0 &&
      changed.every(
        (node) => node instanceof Element && node.hasAttribute(DISCLOSURE),
      )
    );
  });
}

/**
 * Scrolls `el` to the bottom whenever the run adds to it, for as long as
 * `pinned` says the reader is still at the bottom. Returns a teardown.
 *
 * `pinned` is read at the moment the content changes rather than captured, so
 * a reader who scrolls away mid-stream is left where they went.
 */
export function followTail(el: HTMLElement, pinned: () => boolean): () => void {
  const observer = new MutationObserver((records) => {
    if (!pinned() || onlyDisclosure(records)) return;
    el.scrollTop = el.scrollHeight;
  });
  observer.observe(el, { childList: true, subtree: true, characterData: true });
  return () => observer.disconnect();
}
