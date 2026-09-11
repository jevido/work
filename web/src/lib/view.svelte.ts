/**
 * What this browser is doing with the workspace, as opposed to what the
 * workspace says.
 *
 * Folding is the interesting one. `collapsed` is a field on a node, so it is
 * part of the shared document -- somebody folding a branch in the desktop app
 * is saying "this part is settled" to everybody. A viewer that wrote to it
 * would be a read-only page that changes other people's screens, so it does
 * not: the document's fold is respected, and unfolding here is remembered
 * here and nowhere else.
 *
 * Nothing in this file, or anywhere else in web/, sends anything to the
 * server.
 */
import { createContext } from "svelte";
import { isCollapsed } from "@doc/model";
import type { TreeNode } from "@doc/ops";

export class ViewState {
  /** Branches this reader has opened, over the document's own folds. */
  #opened = $state<Record<string, true>>({});
  /** Branches this reader has closed that the document has open. */
  #closed = $state<Record<string, true>>({});

  /** A line to draw attention to, after following a link to it. */
  highlight = $state<string | null>(null);

  #fade: ReturnType<typeof setTimeout> | null = null;

  /** Whether a branch's children are drawn. The document, then this reader. */
  isOpen(node: TreeNode): boolean {
    if (this.#opened[node.id]) return true;
    if (this.#closed[node.id]) return false;
    return !isCollapsed(node);
  }

  /** Whether this reader has overridden what the document says. */
  isOverridden(node: TreeNode): boolean {
    return this.#opened[node.id] === true || this.#closed[node.id] === true;
  }

  toggle(node: TreeNode): void {
    const open = this.isOpen(node);
    delete this.#opened[node.id];
    delete this.#closed[node.id];
    if (open === isCollapsed(node)) return;
    if (open) this.#closed[node.id] = true;
    else this.#opened[node.id] = true;
  }

  /**
   * Opens everything down to a line and marks it, so a link from the plan
   * lands somewhere visible.
   *
   * The ancestors are opened for this reader only, which is the whole reason
   * a link into a folded branch works here at all.
   */
  jumpTo(tree: readonly TreeNode[], id: string): void {
    for (const ancestor of chainTo(tree, id)) {
      delete this.#closed[ancestor.id];
      this.#opened[ancestor.id] = true;
    }
    this.highlight = id;
    if (this.#fade !== null) clearTimeout(this.#fade);
    // The mark is a hint, not a selection. Leaving it on turns into a second
    // kind of highlight that never goes away and means nothing an hour later.
    this.#fade = setTimeout(() => {
      this.highlight = null;
      this.#fade = null;
    }, 4000);
  }
}

export const [getView, setView] = createContext<ViewState>();

function chainTo(tree: readonly TreeNode[], id: string): TreeNode[] {
  const path: TreeNode[] = [];
  const dig = (nodes: readonly TreeNode[]): boolean => {
    for (const node of nodes) {
      if (node.id === id) return true;
      path.push(node);
      if (dig(node.children)) return true;
      path.pop();
    }
    return false;
  };
  return dig(tree) ? path : [];
}
