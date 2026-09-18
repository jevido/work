/**
 * The map's own colours, and where they come from.
 *
 * The desktop app is one theme and the map's palette could have stayed a table
 * of hex values in the renderer -- it did. The read-only viewer is not: it
 * opens on whatever machine a link was sent to, often a phone, often outdoors,
 * so it has a light theme, and a canvas full of #171c26 boxes on a white page
 * is the one part of a shared workspace that would arrive unreadable.
 *
 * So every colour is a CSS variable with the dark value as its fallback. The
 * desktop app defines none of them and is unchanged; the viewer defines the
 * light half of them in its own stylesheet, next to the rest of its theme,
 * which is where somebody looking for "why is this the wrong colour" will go.
 */

/** How a box is drawn at one depth. Geometry is in layout.ts; this is paint. */
export interface TierPaint {
  fill: string;
  edge: string;
  text: string;
}

/**
 * A paper colour, as a head and as the notes hanging off it.
 *
 * Two shades of one hue rather than two hues: what the pale version says is
 * "this belongs to that", and it can only say it if it is obviously the same
 * colour with the life taken out of it.
 */
export interface ClusterPaint {
  head: TierPaint;
  leaf: TierPaint;
}

export interface Palette {
  tiers: TierPaint[];
  /** The board itself, and the dots on it. */
  board: string;
  grid: string;
  /** The strip across the corner of a cluster head. */
  tape: string;
  tapeEdge: string;
  /** The one note in the middle, when the board found a subject to put there. */
  subject: TierPaint;
  /** The paper, one entry per cluster, taken in order and wrapped around. */
  clusters: ClusterPaint[];
  branch: string;
  twig: string;
  region: string;
  regionEdge: string;
  focusEdge: string;
  focusFill: string;
  focusGlow: string;
  muted: string;
  count: string;
}

/**
 * Six papers, in the order clusters take them.
 *
 * Muted rather than the pastels a paper board would use: these are notes lying
 * on a dark surface, and a sticky note at full saturation on #101319 glows.
 * The order is the one that keeps neighbours apart -- warm, cool, warm, cool --
 * because clusters are laid out around a ring and two greens next to each other
 * read as one cluster that happens to be split.
 */
const PAPERS: ClusterPaint[] = [
  {
    head: { fill: "#3a3016", edge: "#6b5a22", text: "#f0e2bd" },
    leaf: { fill: "#25231a", edge: "#3e3a28", text: "#d8d2be" },
  },
  {
    head: { fill: "#1b2b3e", edge: "#2f5378", text: "#cfe3f7" },
    leaf: { fill: "#1a2029", edge: "#2a3646", text: "#cbd8e6" },
  },
  {
    head: { fill: "#3a1f28", edge: "#6d3a49", text: "#f3d3dd" },
    leaf: { fill: "#251c20", edge: "#3f2f36", text: "#e0cdd4" },
  },
  {
    head: { fill: "#1e3328", edge: "#356047", text: "#cfeddd" },
    leaf: { fill: "#1b241f", edge: "#2c3d33", text: "#c9dcd2" },
  },
  {
    head: { fill: "#3a2620", edge: "#6b4636", text: "#f2d8cc" },
    leaf: { fill: "#241e1b", edge: "#3c322c", text: "#e0d0c8" },
  },
  {
    head: { fill: "#2c2540", edge: "#4f4377", text: "#ded5f5" },
    leaf: { fill: "#201d29", edge: "#35304a", text: "#d4cee2" },
  },
];

/** The dark values, which are the ones the design draws and the app ships. */
export const DARK: Palette = {
  tiers: [
    { fill: "#262b35", edge: "#4a5266", text: "#e6e9ef" },
    { fill: "#232937", edge: "#3d4557", text: "#e6e9ef" },
    { fill: "#1e2430", edge: "#2e3542", text: "#e6e9ef" },
    { fill: "#171c26", edge: "#272d38", text: "#b9c0cd" },
  ],
  board: "#101319",
  grid: "rgba(148, 163, 184, 0.10)",
  tape: "rgba(230, 233, 239, 0.13)",
  tapeEdge: "rgba(230, 233, 239, 0.20)",
  subject: { fill: "#1d2740", edge: "#3b5f96", text: "#e8effa" },
  clusters: PAPERS,
  branch: "rgba(148, 163, 184, 0.55)",
  twig: "rgba(148, 163, 184, 0.38)",
  region: "rgba(96, 165, 250, 0.07)",
  regionEdge: "rgba(96, 165, 250, 0.35)",
  focusEdge: "#f2b544",
  focusFill: "#242a36",
  focusGlow: "rgba(242, 181, 68, 0.12)",
  muted: "rgba(230, 233, 239, 0.6)",
  count: "#8b93a3",
};

/**
 * Reads the palette off an element, falling back to the dark one per value.
 *
 * Per value, not all-or-nothing: a stylesheet that overrides four of these and
 * leaves the rest is a stylesheet that meant to override four of these.
 *
 * Called when the canvas is set up and whenever the colour scheme changes,
 * never per frame. getComputedStyle forces layout, and a repaint that did this
 * would turn a still map into a reflow every time anything moved.
 */
export function readPalette(el: Element): Palette {
  const css = getComputedStyle(el);
  const read = (name: string, fallback: string): string => {
    const value = css.getPropertyValue(name).trim();
    return value === "" ? fallback : value;
  };
  const paper = (name: string, at: number, fallback: TierPaint): TierPaint => ({
    fill: read(`--map-cluster-${at}-${name}-fill`, fallback.fill),
    edge: read(`--map-cluster-${at}-${name}-edge`, fallback.edge),
    text: read(`--map-cluster-${at}-${name}-text`, fallback.text),
  });
  return {
    tiers: DARK.tiers.map((tier, depth) => ({
      fill: read(`--map-fill-${depth}`, tier.fill),
      edge: read(`--map-edge-${depth}`, tier.edge),
      text: read(`--map-text-${depth}`, tier.text),
    })),
    board: read("--map-board", DARK.board),
    grid: read("--map-grid", DARK.grid),
    tape: read("--map-tape", DARK.tape),
    tapeEdge: read("--map-tape-edge", DARK.tapeEdge),
    subject: {
      fill: read("--map-subject-fill", DARK.subject.fill),
      edge: read("--map-subject-edge", DARK.subject.edge),
      text: read("--map-subject-text", DARK.subject.text),
    },
    clusters: DARK.clusters.map((cluster, at) => ({
      head: paper("head", at, cluster.head),
      leaf: paper("leaf", at, cluster.leaf),
    })),
    branch: read("--map-branch", DARK.branch),
    twig: read("--map-twig", DARK.twig),
    region: read("--map-region", DARK.region),
    regionEdge: read("--map-region-edge", DARK.regionEdge),
    focusEdge: read("--map-focus-edge", DARK.focusEdge),
    focusFill: read("--map-focus-fill", DARK.focusFill),
    focusGlow: read("--map-focus-glow", DARK.focusGlow),
    muted: read("--map-muted", DARK.muted),
    count: read("--map-count", DARK.count),
  };
}

/**
 * The paper a box is drawn on.
 *
 * Wrapped rather than clamped: a board with nine clusters and six papers hands
 * the seventh the first paper again, which is what a real board does when it
 * runs out of pads. Clamping would give every cluster past the sixth the same
 * colour, which is worse -- it says "these four are one thing" about four
 * things that are not.
 */
export function paperOf(palette: Palette, cluster: number, head: boolean): TierPaint {
  if (cluster < 0) return palette.subject;
  const papers = palette.clusters;
  const paper = papers[((cluster % papers.length) + papers.length) % papers.length];
  return head ? paper.head : paper.leaf;
}
