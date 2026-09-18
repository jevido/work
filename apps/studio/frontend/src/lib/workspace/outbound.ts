/**
 * The bridge an outline edit crosses to become an op.
 *
 * Until this existed, the Orientation and Isolation outline was a document in
 * localStorage: `Workspace` minted ops, applied them and forgot them, and
 * nothing anywhere ever sent one. Two people on one workspace did not share an
 * outline from the desktop at all -- Go's workspace document was written only
 * by the board and by what a run discovered.
 *
 * What crosses is intent, not an op. The actor, the clock and the op id are the
 * three things only one writer per machine may mint, and the writer is the Sync
 * that owns the workspace. A frontend that minted its own -- which is what the
 * code here used to do -- is a second replica on the same machine, with a clock
 * that has never seen the other one, deciding contested fields arbitrarily.
 */
import type {
  Document,
  Edit,
} from "../../../bindings/dev.jevido/work/apps/studio/internal/workbench/models.js";
import type {
  Node as WireNode,
  TreeNode as WireTreeNode,
} from "../../../bindings/dev.jevido/work/packages/ops/models.js";
import type { Op } from "./ops";

/**
 * editOf projects an op onto the edit that describes it.
 *
 * Everything the frontend legitimately decides survives: which node, where it
 * goes among its siblings, what it says. Everything only the owning replica may
 * decide is dropped on the floor here rather than sent and ignored, so there is
 * no chance of a stale clock being read off the wire by something later.
 */
export function editOf(op: Op): Edit {
  // The kinds are the same five words on both sides; the two declarations of
  // them are generated and hand-written, and TypeScript will not take one
  // for the other.
  const edit: Edit = { kind: op.kind as Edit["kind"] };
  if (op.node) edit.node = op.node;
  if (op.parent) edit.parent = op.parent;
  if (op.position) edit.position = op.position;
  if (op.task) edit.task = op.task;
  if (op.fields && Object.keys(op.fields).length > 0) {
    // Ordinary JSON, because that is what crosses a webview bridge. Go
    // re-encodes it into an op's raw fields on the way in.
    edit.fields = { ...op.fields } as Record<string, unknown>;
  }
  return edit;
}

/**
 * opsOf turns a merged document back into the ops that would produce it.
 *
 * Needed because the two sides speak different things: Go returns a document —
 * a tree — and the frontend's optimistic layer is a [State], which is built
 * from ops and has no way to be handed a tree. Rather than keep a second
 * document model in sync with the first, the tree is replayed as creates.
 *
 * The ops are synthetic and never leave this function's caller. Their ids are
 * derived from the node ids so that replaying the same document twice is
 * idempotent, and their clocks ascend in document order so that a later local
 * edit — which takes a clock above all of them — always wins over the seed.
 */
export function opsOf(doc: Document | null, tab: string): Op[] {
  if (!doc) return [];
  const out: Op[] = [];

  // A document is the whole workspace: every tab, each hanging under a root
  // node whose id is the tab's. One Workspace here is one tab, so what it
  // adopts is that root's children -- adopting the document whole would put
  // every other tab's outline on this tab's screen, as top-level lines.
  const root = (doc.tree ?? []).find((n) => n.id === tab);

  const push = (node: WireNode, parent: string) => {
    out.push({
      id: `seed:${node.id}`,
      kind: "create-node" as Op["kind"],
      actor: "seed",
      // One above the last, so document order is clock order and nothing in
      // the seed can outrank anything a person types afterwards.
      clock: out.length + 1,
      node: node.id,
      parent,
      position: node.position ?? "",
      fields: { ...(node.fields ?? {}) } as Op["fields"],
    });
  };

  const walk = (nodes: readonly WireTreeNode[], parent: string) => {
    for (const node of nodes) {
      push(node, parent);
      walk(node.children ?? [], node.id);
    }
  };

  walk(root?.children ?? [], "");
  // Detached nodes are not an error case to hide: they are what an eventually
  // consistent tree looks like while it converges, and sometimes once it has.
  // They keep the parent they were given, which is the node that is not there.
  //
  // Not filtered by tab, because a detached node is one whose parent cannot be
  // found -- so which tab it belongs to is exactly the question that cannot be
  // answered. Showing it in the tab that is open is better than showing it
  // nowhere.
  for (const node of doc.detached ?? []) push(node, node.parent ?? "");

  return out;
}
