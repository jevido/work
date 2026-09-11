/**
 * Sort keys for siblings, as strings.
 *
 * The op protocol positions a node with a string compared as a string (see
 * server/README.md, `position`), which is what lets a line be inserted between
 * two neighbours without renumbering anybody -- renumbering would be an op per
 * sibling, every one a chance to collide with somebody else's edit.
 *
 * The obvious way to do that is to treat a key as a fraction and hand back the
 * midpoint. It works, and it has a flaw that only shows up in the one thing
 * people do most: appending. Midpointing towards infinity converges -- "V",
 * "l", "t", "x", "z", and then it has to grow a character -- so writing an
 * outline top to bottom lengthens the key about one character per five lines.
 * At 1275 lines it crosses the protocol's 256-byte cap and the op is refused,
 * which is a strange way to be told an outline is too long.
 *
 * So a key here is in two parts, as in David Greenspan's scheme and Rocicorp's
 * implementation of it:
 *
 *   - an *integer part*, whose first character says how long it is: `a` means
 *     one digit follows, `b` two, up to `z`; `Z` down to `A` do the same for
 *     the other direction. Because the length is in the leading character, and
 *     the leading characters are themselves ordered, longer integers sort
 *     after shorter ones -- which plain string comparison would otherwise get
 *     backwards.
 *   - an optional *fractional part*, used only when there is no room left
 *     between two integers.
 *
 * Appending increments the integer part: a0, a1, ... az, b00, ... So a
 * thousand appends is a three-character key, not a thousand-character one, and
 * only inserting repeatedly between the same two neighbours grows a key at all
 * -- which is bounded by how much room base 62 gives you, about one character
 * per six insertions.
 *
 * Two replicas can still choose the same key for different nodes. That is not
 * a corruption: the merge orders siblings by position and then by node id, so
 * a tie renders identically everywhere. It just means neither inserted
 * "before" the other.
 */

/** Ordered by their own byte values, so string comparison is digit comparison. */
const DIGITS = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";
const BASE = DIGITS.length;
const ZERO = DIGITS[0];

/** The smallest and largest integer parts, past which there is nowhere to go. */
const SMALLEST_INTEGER = `A${ZERO.repeat(26)}`;

/**
 * A key strictly between `before` and `after`.
 *
 * An empty `before` means "before everything" and an empty `after` means
 * "after everything", which is how the two ends of a list are asked for
 * without a call for each.
 *
 * Never throws. Keys arrive over the network from replicas running other
 * versions of this code, and refusing to insert a line because somebody else's
 * sort key is not in a shape this file recognises would be a worse failure
 * than putting the line in a plausible place. Anything unparseable falls back
 * to the plain midpoint below, which copes with any base-62 string.
 */
export function between(before: string, after: string): string {
  const a = before === "" ? null : before;
  const b = after === "" ? null : after;
  try {
    return keyBetween(a, b);
  } catch {
    return fallbackBetween(before, after);
  }
}

/* -------------------------------------------------------------------------- */
/* The integer part                                                           */
/* -------------------------------------------------------------------------- */

/**
 * How many characters an integer part has, read from its first one.
 *
 * `a`..`z` count upwards from two characters, `Z`..`A` downwards. That is what
 * makes a longer integer sort after a shorter one without any padding.
 */
function integerLength(head: string): number {
  if (head >= "a" && head <= "z") return head.charCodeAt(0) - 97 + 2;
  if (head >= "A" && head <= "Z") return 90 - head.charCodeAt(0) + 2;
  throw new Error(`not an order key: ${head}`);
}

function integerPart(key: string): string {
  const length = integerLength(key[0]);
  if (length > key.length) throw new Error(`order key is too short: ${key}`);
  return key.slice(0, length);
}

function checkInteger(int: string): void {
  if (int.length !== integerLength(int[0])) throw new Error(`bad integer part: ${int}`);
}

/** Rejects anything the arithmetic below cannot take. */
function checkKey(key: string): void {
  if (key === SMALLEST_INTEGER) throw new Error("the smallest key has no room below it");
  const int = integerPart(key);
  const fraction = key.slice(int.length);
  // A trailing zero is a second spelling of the same value, and the midpoint
  // relies on there being only one.
  if (fraction.endsWith(ZERO)) throw new Error(`trailing zero: ${key}`);
  for (const ch of key.slice(1)) if (!DIGITS.includes(ch)) throw new Error(`not base 62: ${key}`);
}

/** The next integer up, or null when there is no more room. */
function incrementInteger(int: string): string | null {
  checkInteger(int);
  const head = int[0];
  const digits = int.slice(1).split("");
  let carry = true;
  for (let i = digits.length - 1; carry && i >= 0; i--) {
    const next = DIGITS.indexOf(digits[i]) + 1;
    if (next === BASE) digits[i] = ZERO;
    else {
      digits[i] = DIGITS[next];
      carry = false;
    }
  }
  if (!carry) return head + digits.join("");

  // The digits rolled over, so the integer needs to be one longer -- which
  // means the next head along, since the head is the length.
  if (head === "z") return null;
  if (head === "Z") return `a${ZERO}`;
  const nextHead = String.fromCharCode(head.charCodeAt(0) + 1);
  if (nextHead > "a") digits.push(ZERO);
  else digits.pop();
  return nextHead + digits.join("");
}

/** The next integer down, or null when there is no more room. */
function decrementInteger(int: string): string | null {
  checkInteger(int);
  const head = int[0];
  const digits = int.slice(1).split("");
  let borrow = true;
  for (let i = digits.length - 1; borrow && i >= 0; i--) {
    const next = DIGITS.indexOf(digits[i]) - 1;
    if (next === -1) digits[i] = DIGITS[BASE - 1];
    else {
      digits[i] = DIGITS[next];
      borrow = false;
    }
  }
  if (!borrow) return head + digits.join("");

  if (head === "A") return null;
  if (head === "a") return `Z${DIGITS[BASE - 1]}`;
  const nextHead = String.fromCharCode(head.charCodeAt(0) - 1);
  if (nextHead < "Z") digits.push(DIGITS[BASE - 1]);
  else digits.pop();
  return nextHead + digits.join("");
}

/* -------------------------------------------------------------------------- */

/** The whole rule, with null for the open ends. */
function keyBetween(a: string | null, b: string | null): string {
  if (a !== null) checkKey(a);
  if (b !== null) checkKey(b);
  if (a !== null && b !== null && a >= b) throw new Error(`${a} is not before ${b}`);

  if (a === null) {
    if (b === null) return `a${ZERO}`;
    const int = integerPart(b);
    const fraction = b.slice(int.length);
    // Nowhere below this integer, so make room inside it instead.
    if (int === SMALLEST_INTEGER) return int + midpoint("", fraction);
    // b has a fractional part, so its own integer is already below it.
    if (int < b) return int;
    const down = decrementInteger(int);
    if (down === null) throw new Error("no room below");
    return down;
  }

  if (b === null) {
    const int = integerPart(a);
    const fraction = a.slice(int.length);
    const up = incrementInteger(int);
    // The common case, and the whole point of the integer part: appending is
    // one increment, and the key stays the length it was.
    return up === null ? int + midpoint(fraction, null) : up;
  }

  const intA = integerPart(a);
  const intB = integerPart(b);
  if (intA === intB) return intA + midpoint(a.slice(intA.length), b.slice(intB.length));

  const up = incrementInteger(intA);
  if (up === null) throw new Error("no room above");
  if (up < b) return up;
  return intA + midpoint(a.slice(intA.length), null);
}

/* -------------------------------------------------------------------------- */
/* The fractional part                                                        */
/* -------------------------------------------------------------------------- */

/**
 * A fraction strictly between `a` and `b`, where null means no upper bound.
 *
 * Greenspan's midpoint. Short and fiddly, and worth using rather than an
 * obvious one because the obvious ones do not terminate when the two bounds
 * are adjacent digits. Neither argument may have a trailing zero, which is
 * what checkKey guarantees.
 */
function midpoint(a: string, b: string | null): string {
  if (b !== null) {
    // Copy the common prefix off and recurse on what is left: at the first
    // digit the two bounds look identical and there is no room between them.
    let n = 0;
    while ((a[n] ?? ZERO) === b[n]) n++;
    if (n > 0) return b.slice(0, n) + midpoint(a.slice(n), b.slice(n));
  }

  const digitA = a === "" ? 0 : DIGITS.indexOf(a[0]);
  const digitB = b === null ? BASE : DIGITS.indexOf(b[0]);
  if (digitA < 0 || digitB < 0) throw new Error("fraction is not base 62");

  if (digitB - digitA > 1) return DIGITS[Math.round(0.5 * (digitA + digitB))];
  // The digits are adjacent, so the answer is longer than either bound.
  if (b !== null && b.length > 1) return b.slice(0, 1);
  return DIGITS[digitA] + midpoint(a.slice(1), null);
}

/**
 * What to do with a key this file did not write.
 *
 * A plain base-62 midpoint, ignoring the integer part entirely: it cannot
 * produce a well-formed order key, but it can always produce a string in the
 * right place in the ordering, which is what the caller actually needs. Only
 * reached for keys from another implementation, or an older one of this.
 */
function fallbackBetween(before: string, after: string): string {
  const lower = strip(before);
  const upper = after === "" ? null : strip(after);
  if (upper !== null && lower >= upper) return lower + DIGITS[Math.floor(BASE / 2)];
  try {
    return midpoint(lower, upper);
  } catch {
    return lower + DIGITS[Math.floor(BASE / 2)];
  }
}

function strip(key: string): string {
  let out = "";
  for (const ch of key) if (DIGITS.includes(ch)) out += ch;
  return out.replace(/0+$/, "");
}
