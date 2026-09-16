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
import { isCollapsed, type DocNode } from "./doc";

/**
 * What a row needs to know about the parts of the map that are not the tree.
 *
 * Supplied by App from the Viewer, because OutlineBranch recurses into itself
 * and threading it as a prop would mean passing it through every level for the
 * benefit of the rows that happen to have a relation.
 */
export interface Relations {
  tree: readonly DocNode[];
  linksOf(id: string): { other: string; text: string; dangling: boolean }[];
  regionOf(id: string): string | null;
}

export class ViewState {
  /** Set once by App. Empty until then, which is what a first paint sees. */
  relations: Relations = { tree: [], linksOf: () => [], regionOf: () => null };

  /** Scrolls to a line and marks it, the way the plan's link to an idea does. */
  reveal(id: string): void {
    this.jumpTo(this.relations.tree, id);
  }

  /** Branches this reader has opened, over the document's own folds. */
  #opened = $state<Record<string, true>>({});
  /** Branches this reader has closed that the document has open. */
  #closed = $state<Record<string, true>>({});

  /** A line to draw attention to, after following a link to it. */
  highlight = $state<string | null>(null);

  #fade: ReturnType<typeof setTimeout> | null = null;

  /** Whether a branch's children are drawn. The document, then this reader. */
  isOpen(node: DocNode): boolean {
    if (this.#opened[node.id]) return true;
    if (this.#closed[node.id]) return false;
    return !isCollapsed(node);
  }

  /** Whether this reader has overridden what the document says. */
  isOverridden(node: DocNode): boolean {
    return this.#opened[node.id] === true || this.#closed[node.id] === true;
  }

  toggle(node: DocNode): void {
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
  jumpTo(tree: readonly DocNode[], id: string): void {
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

function chainTo(tree: readonly DocNode[], id: string): DocNode[] {
  const path: DocNode[] = [];
  const dig = (nodes: readonly DocNode[]): boolean => {
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
