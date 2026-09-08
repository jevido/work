/**
 * A minimal line differ, so a proposed edit can be read as a diff rather than
 * as two walls of text.
 *
 * Longest-common-subsequence over lines, which is what `diff` itself does. The
 * inputs here are one file edit at a time, so the quadratic table is fine; a
 * guard falls back to "replace everything" on anything pathologically large.
 */
export type DiffKind = "same" | "add" | "remove";

export interface DiffLine {
  kind: DiffKind;
  text: string;
  /** Line number in the old text, when it has one. */
  oldLine: number | null;
  /** Line number in the new text, when it has one. */
  newLine: number | null;
}

/** Above this many lines on either side, the table is not worth building. */
const MAX_LINES = 2000;

export function diffLines(before: string, after: string): DiffLine[] {
  const a = before.length ? before.split("\n") : [];
  const b = after.length ? after.split("\n") : [];

  if (a.length > MAX_LINES || b.length > MAX_LINES) {
    return [
      ...a.map((text, i) => ({ kind: "remove" as const, text, oldLine: i + 1, newLine: null })),
      ...b.map((text, i) => ({ kind: "add" as const, text, oldLine: null, newLine: i + 1 })),
    ];
  }

  // lcs[i][j] = length of the longest common subsequence of a[i:] and b[j:].
  const lcs: number[][] = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i][j] =
        a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }

  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i], oldLine: i + 1, newLine: j + 1 });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: "remove", text: a[i], oldLine: i + 1, newLine: null });
      i++;
    } else {
      out.push({ kind: "add", text: b[j], oldLine: null, newLine: j + 1 });
      j++;
    }
  }
  while (i < a.length) {
    out.push({ kind: "remove", text: a[i], oldLine: i + 1, newLine: null });
    i++;
  }
  while (j < b.length) {
    out.push({ kind: "add", text: b[j], oldLine: null, newLine: j + 1 });
    j++;
  }
  return out;
}

/**
 * Drops long runs of unchanged lines, keeping a few for context, the way a
 * unified diff does. Returns the kept lines with gaps marked.
 */
export interface DiffHunkLine extends DiffLine {
  /** Set on a placeholder standing in for skipped unchanged lines. */
  skipped?: number;
}

export function collapseUnchanged(lines: DiffLine[], context = 3): DiffHunkLine[] {
  const keep = new Array<boolean>(lines.length).fill(false);
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].kind === "same") continue;
    for (let j = Math.max(0, i - context); j <= Math.min(lines.length - 1, i + context); j++) {
      keep[j] = true;
    }
  }

  const out: DiffHunkLine[] = [];
  let skipped = 0;
  for (let i = 0; i < lines.length; i++) {
    if (keep[i]) {
      if (skipped > 0) {
        out.push({ kind: "same", text: "", oldLine: null, newLine: null, skipped });
        skipped = 0;
      }
      out.push(lines[i]);
    } else {
      skipped++;
    }
  }
  if (skipped > 0) {
    out.push({ kind: "same", text: "", oldLine: null, newLine: null, skipped });
  }
  return out;
}

/** Counts added and removed lines, for a one-line summary. */
export function diffStat(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const line of lines) {
    if (line.kind === "add") added++;
    else if (line.kind === "remove") removed++;
  }
  return { added, removed };
}

/**
 * Parses a unified diff, as produced by `git diff`, into displayable lines.
 *
 * Only what the view needs is read: hunk headers give the starting line
 * numbers, and each following line's first character says whether it was added,
 * removed or left alone. File headers are skipped -- the caller already knows
 * which file this is.
 */
export function parseUnifiedDiff(patch: string): DiffHunkLine[] {
  if (!patch.trim()) return [];

  const out: DiffHunkLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;

  for (const raw of patch.split("\n")) {
    if (raw.startsWith("@@")) {
      // @@ -oldStart,oldCount +newStart,newCount @@
      const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(raw);
      if (match) {
        if (inHunk) {
          out.push({ kind: "same", text: "", oldLine: null, newLine: null, skipped: 0 });
        }
        oldLine = Number(match[1]);
        newLine = Number(match[2]);
        inHunk = true;
      }
      continue;
    }
    if (!inHunk) continue;
    // Splitting on newlines yields one empty entry for the patch's trailing
    // newline. A real context line is a single space, never empty.
    if (raw === "") continue;

    // Everything git says about the files themselves, rather than their
    // contents, is noise here.
    if (
      raw.startsWith("diff ") ||
      raw.startsWith("index ") ||
      raw.startsWith("--- ") ||
      raw.startsWith("+++ ") ||
      raw.startsWith("new file") ||
      raw.startsWith("deleted file") ||
      raw.startsWith("similarity ") ||
      raw.startsWith("rename ") ||
      raw.startsWith("Binary files ") ||
      raw.startsWith("\\ No newline")
    ) {
      continue;
    }

    const marker = raw[0] ?? " ";
    const text = raw.slice(1);
    if (marker === "+") {
      out.push({ kind: "add", text, oldLine: null, newLine: newLine++ });
    } else if (marker === "-") {
      out.push({ kind: "remove", text, oldLine: oldLine++, newLine: null });
    } else {
      out.push({ kind: "same", text, oldLine: oldLine++, newLine: newLine++ });
    }
  }
  return out;
}
