/**
 * The document, as the server hands it over.
 *
 * This file used to be an alias onto `frontend/src/lib/workspace` -- the
 * viewer replayed the op log and merged it with the same TypeScript the
 * desktop app merged with. That is gone. A browser cannot run
 * `internal/ops`, and the only way to show a document without it was a second
 * merge implementation; two merges that drift apart show two different
 * documents for one log, and there is no correct side and no way to see it
 * from either.
 *
 * So the server merges, `GET /v1/document` hands back the result, and what is
 * left here is the small half that was never the merge: which field names
 * mean what, and the two views read out of one tree. See server/README.md,
 * "Applying the log".
 */

/** A node as `GET /v1/document` writes it, before anything normalises it. */
interface WireNode {
  id: string;
  parent?: string;
  position?: string;
  fields?: Record<string, unknown> | null;
  children?: WireNode[] | null;
}

/**
 * A node with its optional halves filled in.
 *
 * The contract says `parent`, `position` and `fields` travel only when they
 * are set, and `children` only when a node has any -- so every reader would
 * otherwise need the same three `?? {}`. They are filled in once, here, at the
 * edge, and everything below this line can index a field and walk children
 * without checking first.
 */
export interface DocNode {
  id: string;
  parent: string;
  position: string;
  fields: Record<string, unknown>;
  children: DocNode[];
}

/** The whole answer. `head` is the sequence this was merged through. */
export interface Document {
  head: number;
  tree: DocNode[];
  detached: DocNode[];
}

/**
 * Turns the wire's shape into the one above.
 *
 * Checked rather than cast at every step. This is a JSON body from a network,
 * written by a server that may be newer than this page, and a `tree` that came
 * back as a string should render an empty outline rather than throw inside a
 * component.
 */
export function readDocument(body: unknown): Document {
  const r = asRecord(body);
  return {
    head: typeof r?.head === "number" && Number.isFinite(r.head) ? r.head : 0,
    tree: readNodes(r?.tree),
    // Flat, always -- the contract says detached is never nested, even when
    // one detached node is another's parent.
    detached: readNodes(r?.detached).map((n) => ({ ...n, children: [] })),
  };
}

function readNodes(value: unknown): DocNode[] {
  if (!Array.isArray(value)) return [];
  const out: DocNode[] = [];
  for (const entry of value) {
    const node = readNode(entry);
    if (node) out.push(node);
  }
  return out;
}

function readNode(value: unknown): DocNode | null {
  const r = asRecord(value) as WireNode | null;
  if (!r || typeof r.id !== "string" || r.id === "") return null;
  return {
    id: r.id,
    parent: typeof r.parent === "string" ? r.parent : "",
    position: typeof r.position === "string" ? r.position : "",
    fields: asRecord(r.fields) ?? {},
    children: readNodes(r.children),
  };
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

/* -------------------------------------------------------------------------- */
/* The field names                                                            */
/* -------------------------------------------------------------------------- */

/**
 * Conventions, not protocol. server/README.md is deliberately silent on what a
 * field means, so these are the app's agreement with itself -- and the viewer
 * only ever reads them.
 */
const FIELD_TYPE = "type";
const FIELD_TEXT = "text";
const FIELD_COLLAPSED = "collapsed";
/** Reserved by the protocol: the two ends of an extraction. */
const FIELD_EXTRACTED_FROM = "extractedFrom";
/** The two ends of a link, and the region a node is in. */
const FIELD_FROM = "from";
const FIELD_TO = "to";
const FIELD_REGION = "region";
/** What the workbench's own roots carry: a tab has kind "tab" and a name. */
const FIELD_KIND = "kind";
const FIELD_NAME = "name";
const KIND_TAB = "tab";

/**
 * The four kinds of node one document holds.
 *
 * An edge and a region are nodes rather than new op kinds, which is what makes
 * them safe for a viewer to meet: a build of this page made before they existed
 * shows an unrecognised node as nothing at all. Keep that for anything it still
 * does not know -- the next type will arrive the same way.
 */
const TYPE_TASK = "task";
const TYPE_EDGE = "edge";
const TYPE_REGION = "region";

export function textOf(node: DocNode | null | undefined): string {
  const value = node?.fields[FIELD_TEXT];
  return typeof value === "string" ? value : "";
}

export function isTask(node: DocNode): boolean {
  return node.fields[FIELD_TYPE] === TYPE_TASK;
}

export function isEdge(node: DocNode): boolean {
  return node.fields[FIELD_TYPE] === TYPE_EDGE;
}

export function isRegion(node: DocNode): boolean {
  return node.fields[FIELD_TYPE] === TYPE_REGION;
}

/**
 * Whether a node is a line the outline draws.
 *
 * By what it is, not by what it is not. "Everything except a task" was right
 * when there were two types and became wrong the moment there were four --
 * silently, with an edge rendered as a line with no text in it.
 *
 * Note what this deliberately does *not* test: FieldKind. The workbench's own
 * roots -- a tab, and a branch's record of its parent -- carry a kind and no
 * type, so they read as ideas here. For a tab that is correct and load-bearing:
 * the outline lives underneath one, and a filter that skipped tabs at the root
 * would walk into nothing and draw an empty page. Anything else the workbench
 * puts at the root has to carry a `type` this build does not recognise, which
 * is the rule above and the one every other unknown node already follows.
 */
export function isOutlineNode(node: DocNode): boolean {
  const type = node.fields[FIELD_TYPE];
  return type === undefined || type === "idea";
}

/** The region a node says it is in, if any. */
export function regionIdOf(node: DocNode): string | null {
  const value = node.fields[FIELD_REGION];
  return typeof value === "string" && value !== "" ? value : null;
}

/** The two ends of an edge. */
export function endsOf(node: DocNode): { from: string; to: string } {
  const from = node.fields[FIELD_FROM];
  const to = node.fields[FIELD_TO];
  return {
    from: typeof from === "string" ? from : "",
    to: typeof to === "string" ? to : "",
  };
}

export function isCollapsed(node: DocNode): boolean {
  return node.fields[FIELD_COLLAPSED] === true;
}

/**
 * The outline, flattened to rows, for the map to place.
 *
 * The same shape the desktop app's `outlineRows` produces, because both feed
 * the same layout code -- see frontend/src/lib/mindmap/layout.ts, which types
 * its input structurally for exactly this reason. Everything that makes a Row
 * in the desktop app is about editing (fold state, sibling counts, how far up
 * "move" may go); a map needs an id, some text, a depth and a parent, and that
 * is all this produces.
 *
 * Folded branches are skipped, so the map and the outline on the same page
 * agree about what is on screen. A viewer that quietly opened everything would
 * be showing a shape nobody in the workspace is looking at.
 */
export function mapRows(
  tree: readonly DocNode[],
): { node: { id: string; fields: Record<string, unknown> }; depth: number; parentId: string }[] {
  const rows: { node: DocNode; depth: number; parentId: string }[] = [];
  walk(tree.filter(isOutlineNode), "", 0);
  return rows;

  function walk(nodes: readonly DocNode[], parentId: string, depth: number) {
    for (const node of nodes) {
      rows.push({ node, depth, parentId });
      if (!isCollapsed(node) && node.children.length > 0) {
        walk(node.children.filter(isOutlineNode), node.id, depth + 1);
      }
    }
  }
}

/** The idea a task came out of, if it came out of one. */
export function sourceIdOf(node: DocNode): string | null {
  const value = node.fields[FIELD_EXTRACTED_FROM];
  return typeof value === "string" && value !== "" ? value : null;
}

/**
 * What to call a node when something else has to name it.
 *
 * A blank line is a real state in an outliner -- you get one the moment you
 * press Enter -- so everything that labels a node needs an answer that is not
 * the empty string. These end up in button names, so they are trimmed and
 * clipped.
 */
export function labelOf(node: DocNode | null | undefined, limit = 60): string {
  const text = textOf(node).trim();
  if (text === "") return "Untitled line";
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}

/* -------------------------------------------------------------------------- */
/* Tabs                                                                       */
/* -------------------------------------------------------------------------- */

/** One tab of the workspace, as this page needs it: something to name and open. */
export interface TabEntry {
  id: string;
  name: string;
}

/**
 * The workspace's tabs, which are the roots of the document.
 *
 * A tab is a node like any other and carries `kind: "tab"` -- the outline of
 * each one lives underneath it. That is why this page has to know about them
 * at all: the desktop app draws one tab, and a viewer that drew the document
 * whole would put a nameless box in the middle of the board with everybody's
 * tabs clustered around it, which is a picture of the file rather than of the
 * thing somebody was sent a link to.
 */
export function tabsOf(tree: readonly DocNode[]): TabEntry[] {
  const out: TabEntry[] = [];
  for (const node of tree) {
    if (node.fields[FIELD_KIND] !== KIND_TAB) continue;
    const name = node.fields[FIELD_NAME];
    out.push({ id: node.id, name: typeof name === "string" && name !== "" ? name : "Untitled" });
  }
  return out;
}

/**
 * What one tab holds, which is what the desktop app has on screen.
 *
 * A document with no tab at its root is answered whole. That is not a
 * fallback for a broken document: a workspace written by a build older than
 * tabs has its outline at the root, and so does one this page has not learned
 * the shape of yet. Drawing it is better than drawing nothing.
 */
export function contentsOf(tree: readonly DocNode[], tab: string): DocNode[] {
  const tabs = tabsOf(tree);
  if (tabs.length === 0) return [...tree];
  const wanted = tabs.some((t) => t.id === tab) ? tab : tabs[0].id;
  const node = tree.find((n) => n.id === wanted);
  return node ? [...node.children] : [];
}

/* -------------------------------------------------------------------------- */
/* The two views                                                              */
/* -------------------------------------------------------------------------- */

/** The outline: everything that is not a task. */
export function outlineNodes(tree: readonly DocNode[]): DocNode[] {
  return tree.filter(isOutlineNode);
}

/**
 * The plan, in order.
 *
 * Tasks sit at the top level of the same tree, and the merge has already put
 * siblings in position order -- there is nothing to sort here, only something
 * to pick out.
 */
export function planTasks(tree: readonly DocNode[]): DocNode[] {
  return tree.filter(isTask);
}

