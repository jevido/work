import { collapseUnchanged, diffLines, diffStat, type DiffHunkLine } from "./diff";

/** One tool call an agent made, as the console shows it. */
export interface ToolCall {
  id: string;
  name: string;
  /** One line saying what this call was about: a path, a command, a pattern. */
  summary: string;
  /** The arguments, pretty-printed, for the expanded view. */
  input: string;
  /** What the tool returned, once it has. */
  result: string;
  failed: boolean;
  done: boolean;
  /** Rendered diff, for calls that change a file. */
  diff: DiffHunkLine[] | null;
  added: number;
  removed: number;
  /** The file a call touches, when it touches one. */
  path: string;
}

/**
 * Reads a tool's arguments into something worth showing.
 *
 * The interesting field differs per tool -- a path for Read, a command for Bash
 * -- and an edit carries the before and after text, which is what makes a diff
 * possible without going back to disk.
 */
export function describeTool(id: string, name: string, rawInput: string): ToolCall {
  const call: ToolCall = {
    id,
    name,
    summary: "",
    input: rawInput,
    result: "",
    failed: false,
    done: false,
    diff: null,
    added: 0,
    removed: 0,
    path: "",
  };

  let input: Record<string, unknown> = {};
  try {
    const parsed: unknown = JSON.parse(rawInput || "{}");
    if (parsed && typeof parsed === "object") input = parsed as Record<string, unknown>;
    call.input = JSON.stringify(input, null, 2);
  } catch {
    // Unparseable arguments still show as text; nothing else to do.
    return { ...call, summary: shorten(rawInput, 80) };
  }

  const str = (key: string): string => (typeof input[key] === "string" ? (input[key] as string) : "");

  call.path = str("file_path") || str("path") || str("notebook_path");

  switch (name) {
    case "Read":
    case "Write":
    case "NotebookEdit":
      call.summary = tail(call.path);
      break;
    case "Edit":
      call.summary = tail(call.path);
      setDiff(call, str("old_string"), str("new_string"));
      break;
    case "Bash":
      call.summary = shorten(str("command"), 120);
      break;
    case "Grep":
      call.summary = str("pattern") + (str("path") ? ` in ${tail(str("path"))}` : "");
      break;
    case "Glob":
      call.summary = str("pattern");
      break;
    case "WebFetch":
      call.summary = str("url");
      break;
    case "Task":
    case "Agent":
      call.summary = str("description") || str("subagent_type");
      break;
    default:
      call.summary = call.path ? tail(call.path) : firstString(input);
  }

  if (name === "Write") {
    // A new file is every line added; showing it as a diff keeps one shape for
    // "here is what will change on disk".
    setDiff(call, "", str("content"));
  }

  return call;
}

function setDiff(call: ToolCall, before: string, after: string): void {
  if (!before && !after) return;
  const lines = diffLines(before, after);
  const stat = diffStat(lines);
  call.diff = collapseUnchanged(lines);
  call.added = stat.added;
  call.removed = stat.removed;
}

function tail(path: string): string {
  if (!path) return "";
  const parts = path.split("/");
  // Enough of the path to be unambiguous without filling the panel.
  return parts.slice(-2).join("/");
}

function shorten(text: string, max: number): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

function firstString(input: Record<string, unknown>): string {
  for (const value of Object.values(input)) {
    if (typeof value === "string" && value) return shorten(value, 80);
  }
  return "";
}
