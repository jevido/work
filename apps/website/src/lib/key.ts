/**
 * The read key: getting it out of the address bar, and keeping it there.
 *
 * A viewer link is `https://host/#k=rk_…`. The fragment is the right place for
 * it -- a fragment is never sent to the server and never appears in an access
 * log, which a query string does on every request.
 *
 * It stays in the address. A read key is not a secret: it is one of a handful
 * handed out on purpose, it can read a board and change nothing, and the
 * person holding it is usually the person who needs to pass it on. Taking it
 * out of the address made that impossible -- the link somebody had just opened
 * could not be copied back out of the bar it was showing in -- which is a
 * worse trade than the one it was making. It is remembered in local storage as
 * well, so a visit to the bare host still opens, and put back into the address
 * when it comes from there.
 */

const STORAGE_KEY = "work.viewer.key";

/**
 * Reads the key from the address if there is one there, otherwise from
 * storage, and leaves the address showing it either way.
 *
 * Returns null when there is no key anywhere, which is the state the page
 * opens in when somebody visits the host without a link.
 */
export function takeKey(): string | null {
  const fromHash = keyInHash(window.location.hash);
  if (fromHash) {
    remember(fromHash);
    return fromHash;
  }
  const saved = recall();
  if (saved) show(saved);
  return saved;
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
 * Puts a remembered key back into the address, without a history entry.
 *
 * So the bar always shows the whole link, whether this visit arrived with one
 * or is a return to a host that remembered it. `replaceState` rather than
 * setting `location.hash`, which adds a history entry and, in some browsers,
 * scrolls.
 */
function show(key: string): void {
  const hash = `#k=${key}`;
  if (window.location.hash === hash) return;
  history.replaceState(null, "", window.location.pathname + window.location.search + hash);
}
