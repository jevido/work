/**
 * The office's avatar images, decoded once and kept.
 *
 * The renderer must not allocate or await inside its frame loop, so nothing
 * about an avatar is discovered while drawing: an agent either has a decoded
 * image to blit or it draws the colour blob it always did. This is where the
 * asynchronous half lives -- ask for a URL, get the image if it is already in
 * hand, and get called back when it is not.
 *
 * The cache is module-level rather than owned by the renderer because it
 * outlives one: the office is torn down and rebuilt whenever its component
 * remounts, and re-decoding the same handful of pictures for that is waste.
 */

/**
 * How many decoded images to keep.
 *
 * Every reload of the config folder asks for a new URL for each agent (the
 * revision changes, so a replaced picture is actually re-read), which means
 * entries accumulate with reloads rather than with agents. A few teams' worth
 * is plenty, and the oldest entry is always the one nobody will ask for again.
 */
const MAX_CACHED = 32;

/**
 * URL to decoded image, or null for a URL that failed.
 *
 * A failure is cached as deliberately as a success. The common reason for one
 * is that the file is gone -- the backend answers a missing avatar with a 404 --
 * and retrying that on every frame or every desk would be pointless traffic.
 */
const decoded = new Map<string, HTMLImageElement | null>();

/** Callbacks waiting on a URL that is still loading. */
const waiting = new Map<string, ((image: HTMLImageElement | null) => void)[]>();

/**
 * The image for a URL.
 *
 * Returns the image if it is already decoded, null if that URL is known to
 * have failed, and undefined while it is on its way -- in which case `done` is
 * called once, with the image or with null. Callers get to tell "no picture"
 * from "not yet", which is what lets the office keep drawing the avatar it
 * already has while a reload re-fetches it instead of blinking to a blob.
 */
export function avatarImage(
  url: string,
  done: (image: HTMLImageElement | null) => void,
): HTMLImageElement | null | undefined {
  const known = decoded.get(url);
  if (known !== undefined) return known;

  const queue = waiting.get(url);
  if (queue) {
    queue.push(done);
    return undefined;
  }
  waiting.set(url, [done]);

  const image = new Image();
  // A decoded image with no dimensions cannot be drawn from -- drawImage would
  // throw -- so it counts as a failure rather than as a picture.
  image.onload = () => settle(url, image.naturalWidth > 0 ? image : null);
  image.onerror = () => settle(url, null);
  image.src = url;
  return undefined;
}

/** Records the outcome for a URL and tells everyone who was waiting. */
function settle(url: string, image: HTMLImageElement | null): void {
  if (decoded.size >= MAX_CACHED) {
    // Map iterates in insertion order, so the first key is the oldest.
    const oldest = decoded.keys().next();
    if (!oldest.done) decoded.delete(oldest.value);
  }
  decoded.set(url, image);

  const queue = waiting.get(url) ?? [];
  waiting.delete(url);
  for (const done of queue) done(image);
}
