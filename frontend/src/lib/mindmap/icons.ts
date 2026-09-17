/**
 * The glyphs a cluster head can carry.
 *
 * Vector paths rather than emoji, which is the whole reason this file exists.
 * An emoji is a font lookup, and the font that answers it is different on every
 * machine -- a board drawn with 🏆 on one desktop and a flat monochrome outline
 * on another is one workspace that looks like two, and the viewer on a phone is
 * a third. These are the same eleven curves everywhere.
 *
 * Ten names, and no way to add an eleventh from the UI. An icon is a label you
 * can find at a glance across a whole board, and a set you can pick anything
 * from stops being that at about fifteen.
 *
 * Drawn in the note's own text colour, so they belong to the paper rather than
 * sitting on top of it.
 */

export const ICON_NAMES = [
  "people",
  "heart",
  "trophy",
  "signpost",
  "warning",
  "check",
  "bulb",
  "flag",
  "question",
  "timing",
] as const;

export type IconName = (typeof ICON_NAMES)[number];

/** What each one is for, for the picker's title. */
export const ICON_LABELS: Record<IconName, string> = {
  people: "People",
  heart: "Feeling",
  trophy: "Winning",
  signpost: "What next",
  warning: "Risk",
  check: "Decided",
  bulb: "Idea",
  flag: "Goal",
  question: "Open question",
  timing: "Timing",
};

/** Every path is drawn in a 24-by-24 box and scaled to wherever it lands. */
export const ICON_BOX = 24;

const PATHS: Record<IconName, string> = {
  people:
    "M9 11.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z M2.5 20.5c0-3.4 2.9-5.6 6.5-5.6s6.5 2.2 6.5 5.6 M16.8 11.6a3 3 0 1 0 0-6 M18 14.9c2 .6 3.5 2.3 3.5 4.5",
  heart:
    "M12 20.6S3.4 15 3.4 9.2a4.7 4.7 0 0 1 8.6-2.7 4.7 4.7 0 0 1 8.6 2.7c0 5.8-8.6 11.4-8.6 11.4z",
  trophy:
    "M7 3.5h10V9a5 5 0 0 1-10 0V3.5z M7 5.5H4V7a3.5 3.5 0 0 0 3 3.5 M17 5.5h3V7a3.5 3.5 0 0 1-3 3.5 M12 14v5 M9 20.5h6",
  signpost: "M12 3v18 M4.5 6.5h12l2 2.6-2 2.6h-12z M7.5 14h9l2 2.6-2 2.6h-9z",
  warning: "M12 3.8 2.4 20.6h19.2L12 3.8z M12 10v4.6 M12 17.6v.2",
  check: "M12 21.2a9.2 9.2 0 1 0 0-18.4 9.2 9.2 0 0 0 0 18.4z M7.8 12.3l2.9 2.9 5.5-5.6",
  bulb:
    "M12 2.6a6 6 0 0 1 3.6 10.8c-.7.5-1.1 1.3-1.1 2.1H9.5c0-.8-.4-1.6-1.1-2.1A6 6 0 0 1 12 2.6z M9.6 18.4h4.8 M10.4 21.3h3.2",
  flag: "M5.4 21.3V3 M5.4 4.4h12.2l-2.6 4 2.6 4H5.4",
  question:
    "M12 21.2a9.2 9.2 0 1 0 0-18.4 9.2 9.2 0 0 0 0 18.4z M9.3 9.5a2.8 2.8 0 0 1 5.4.9c0 1.9-2.7 2.4-2.7 4 M12 17.6v.2",
  timing: "M12 21.2a9.2 9.2 0 1 0 0-18.4 9.2 9.2 0 0 0 0 18.4z M12 6.8v5.6l3.4 2",
};

/*
 * Built once and kept. A Path2D is parsed from its string when it is
 * constructed, and building ten of them on every frame of a pan would be ten
 * parses sixty times a second for a picture that has not changed.
 */
const built = new Map<string, Path2D | null>();

/**
 * The path for a name, or null for a name this build does not know.
 *
 * Null rather than a fallback glyph, deliberately. A newer client can write an
 * icon name this one has never heard of, and drawing a question mark for it
 * would put a wrong label on somebody's note -- where drawing nothing loses
 * only the decoration and keeps the line itself exactly as it was written.
 */
export function iconPath(name: string): Path2D | null {
  if (name === "") return null;
  const known = built.get(name);
  if (known !== undefined) return known;
  const data = (PATHS as Record<string, string | undefined>)[name];
  const path = data === undefined ? null : new Path2D(data);
  built.set(name, path);
  return path;
}

export function isIconName(name: string): name is IconName {
  return Object.prototype.hasOwnProperty.call(PATHS, name);
}
