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

export interface Palette {
  tiers: TierPaint[];
  branch: string;
  twig: string;
  link: string;
  dangling: string;
  region: string;
  regionEdge: string;
  focusEdge: string;
  focusFill: string;
  focusGlow: string;
  dropFill: string;
  muted: string;
  count: string;
}

/** The dark values, which are the ones the design draws and the app ships. */
export const DARK: Palette = {
  tiers: [
    { fill: "#262b35", edge: "#4a5266", text: "#e6e9ef" },
    { fill: "#232937", edge: "#3d4557", text: "#e6e9ef" },
    { fill: "#1e2430", edge: "#2e3542", text: "#e6e9ef" },
    { fill: "#171c26", edge: "#272d38", text: "#b9c0cd" },
  ],
  branch: "rgba(148, 163, 184, 0.55)",
  twig: "rgba(148, 163, 184, 0.38)",
  link: "rgba(96, 165, 250, 0.85)",
  dangling: "rgba(148, 163, 184, 0.4)",
  region: "rgba(96, 165, 250, 0.07)",
  regionEdge: "rgba(96, 165, 250, 0.35)",
  focusEdge: "#f2b544",
  focusFill: "#242a36",
  focusGlow: "rgba(242, 181, 68, 0.12)",
  dropFill: "rgba(96, 165, 250, 0.25)",
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
  return {
    tiers: DARK.tiers.map((tier, depth) => ({
      fill: read(`--map-fill-${depth}`, tier.fill),
      edge: read(`--map-edge-${depth}`, tier.edge),
      text: read(`--map-text-${depth}`, tier.text),
    })),
    branch: read("--map-branch", DARK.branch),
    twig: read("--map-twig", DARK.twig),
    link: read("--map-link", DARK.link),
    dangling: read("--map-dangling", DARK.dangling),
    region: read("--map-region", DARK.region),
    regionEdge: read("--map-region-edge", DARK.regionEdge),
    focusEdge: read("--map-focus-edge", DARK.focusEdge),
    focusFill: read("--map-focus-fill", DARK.focusFill),
    focusGlow: read("--map-focus-glow", DARK.focusGlow),
    dropFill: read("--map-drop-fill", DARK.dropFill),
    muted: read("--map-muted", DARK.muted),
    count: read("--map-count", DARK.count),
  };
}
