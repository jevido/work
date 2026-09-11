/**
 * The read key: getting it out of the address bar, and keeping it out.
 *
 * A viewer link is `https://host/#k=rk_…`. The fragment is the right place
 * for it -- a fragment is never sent to the server and never appears in an
 * access log, which a query string does on every request -- but it is still
 * in the address bar, in the title bar, in a screenshot, and in whatever the
 * browser offers the next person who types the first letter of the host.
 *
 * So the key is read once, written to local storage, and removed from the
 * address with replaceState. Removed rather than pushed over, so Back does
 * not go to the version of this page that still had the key in it.
 */

const STORAGE_KEY = "work.viewer.key";

/**
 * Reads the key from the address if there is one there, otherwise from
 * storage, and leaves the address without it either way.
 *
 * Returns null when there is no key anywhere, which is the state the page
 * opens in when somebody visits the host without a link.
 */
export function takeKey(): string | null {
  const fromHash = keyInHash(window.location.hash);
  if (fromHash) {
    remember(fromHash);
    strip();
    return fromHash;
  }
  strip();
  return recall();
}

export function recall(): string | null {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    return saved && saved.trim() !== "" ? saved : null;
  } catch {
    // A browser that refuses storage still works; it just needs the link
    // again next time.
    return null;
  }
}

export function remember(key: string): void {
  try {
    localStorage.setItem(STORAGE_KEY, key);
  } catch {
    // See recall(). Nothing here is worth interrupting a reader over.
  }
}

/**
 * Forgets the key.
 *
 * Worth a button of its own: this is a page that remembers a credential
 * across visits, on machines that are not always one person's, and "close the
 * tab" is not the same as "stop being able to read this".
 */
export function forget(): void {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Nothing to do. The key was not stored in the first place.
  }
}

/** Pulls `k` out of a fragment, which may hold other things beside it. */
function keyInHash(hash: string): string | null {
  const value = new URLSearchParams(hash.replace(/^#/, "")).get("k");
  return value && value.trim() !== "" ? value.trim() : null;
}

/**
 * Takes the fragment off the address without adding a history entry.
 *
 * `replaceState` with the path and query, rather than setting
 * `location.hash = ""` -- which leaves a bare `#` behind and, in some
 * browsers, scrolls.
 */
function strip(): void {
  if (window.location.hash === "") return;
  history.replaceState(null, "", window.location.pathname + window.location.search);
}
