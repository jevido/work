/**
 * Reading what somebody pasted into the join field.
 *
 * String handling and nothing else -- there is no HTTP in this directory any
 * more. Joining, creating, pushing and pulling are the Go side's: it owns the
 * queue, it fsyncs, and it keeps going when this webview is not on screen.
 * What is left on this side is turning "a key, or a link that has one in it"
 * into the two arguments `WorkbenchService.JoinWorkspace` takes.
 */

/**
 * The server offered when there is nothing better to offer.
 *
 * A default rather than a requirement: every call that uses it takes the
 * server as an argument, and the field is editable.
 */
export const DEFAULT_SERVER: string =
  import.meta.env.VITE_WORK_SERVER ?? "https://work.jevido.app";

/**
 * A key, or a link with a key in it, as a server and a key.
 *
 * A viewer link carries its key in the fragment and names the server in its
 * origin, so pasting one answers both questions at once. A bare key answers
 * only the second, and `base` is null -- whoever called this supplies the
 * server from the form.
 *
 * The query string is checked as well as the fragment because a link that has
 * been through something which strips fragments still arrives with `?k=`, and
 * refusing it would be refusing a key the person plainly has.
 */
export function parseInvite(input: string): { base: string | null; key: string } | null {
  const text = input.trim();
  if (text === "") return null;

  if (/^https?:\/\//i.test(text)) {
    let url: URL;
    try {
      url = new URL(text);
    } catch {
      return null;
    }
    const key = (
      new URLSearchParams(url.hash.replace(/^#/, "")).get("k") ??
      url.searchParams.get("k") ??
      ""
    ).trim();
    if (key === "") return null;
    return { base: url.origin, key };
  }

  // A bare key. Which server it belongs to is the form's own field.
  return { base: null, key: text };
}

/**
 * True for a key the server would only let read.
 *
 * Used to refuse a join before it is attempted, not to decide anything about
 * access: the server is the only thing that actually knows, and it says so on
 * `GET /v1/workspace`. But a read key in the join field produces a workspace
 * that loads fine and silently refuses every edit -- a state worse than not
 * joining, because it looks like it worked -- and the prefix is enough to say
 * so without a round trip, before a tab opens.
 */
export function looksLikeReadKey(key: string): boolean {
  return key.startsWith("rk_");
}
