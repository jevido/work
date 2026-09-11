package workbench

import (
	"errors"
	"fmt"
	"strings"
)

// Sort keys for siblings, as strings.
//
// This is a port of frontend/src/lib/workspace/position.ts, and it is a port
// rather than a second design on purpose: both ends write positions into the
// same document, so a key one of them mints has to be a key the other can
// insert next to. Keeping them the same algorithm is what makes that true
// without either side having to detect the other.
//
// The op protocol positions a node with a string compared as a string (see
// server/README.md, `position`), which is what lets a line be inserted between
// two neighbours without renumbering anybody -- renumbering would be an op per
// sibling, every one a chance to collide with somebody else's edit.
//
// The obvious way to do that is to treat a key as a fraction and hand back the
// midpoint. It works, and it has a flaw that only shows up in the one thing
// people do most: appending. Midpointing towards infinity converges -- "V",
// "l", "t", "x", "z", and then it has to grow a character -- so writing an
// outline top to bottom lengthens the key about one character per five lines.
// At 1275 lines it crosses [ops.MaxPositionLen] and the op is refused, which is
// a strange way to be told an outline is too long.
//
// So a key here is in two parts, as in David Greenspan's scheme and Rocicorp's
// implementation of it:
//
//   - an *integer part*, whose first character says how long it is: `a` means
//     one digit follows, `b` two, up to `z`; `Z` down to `A` do the same for the
//     other direction. Because the length is in the leading character, and the
//     leading characters are themselves ordered, longer integers sort after
//     shorter ones -- which plain string comparison would otherwise get
//     backwards.
//   - an optional *fractional part*, used only when there is no room left
//     between two integers.
//
// Appending increments the integer part: a0, a1, ... az, b00, ... So a thousand
// appends is a three-character key, not a thousand-character one, and only
// inserting repeatedly between the same two neighbours grows a key at all --
// which is bounded by how much room base 62 gives you, about one character per
// six insertions.
//
// Two replicas can still choose the same key for different nodes. That is not a
// corruption: the merge orders siblings by position and then by node ID, so a
// tie renders identically everywhere. It just means neither inserted "before"
// the other.

// digits are ordered by their own byte values, so string comparison is digit
// comparison. Every byte here is ASCII, which is why this file indexes strings
// by byte throughout rather than ranging over runes.
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const (
	base = len(digits)
	// zero is digits[0], spelled out because indexing a constant string is not
	// a constant expression in Go. TestZeroIsTheFirstDigit pins the two
	// together so this cannot drift from the alphabet above it.
	zero = byte('0')
)

// firstKey is the key handed out for the first node in an empty list, and the
// smallest well-formed key this file will mint.
const firstKey = "a0"

// smallestInteger is the least integer part there is, past which there is
// nowhere below to go.
var smallestInteger = "A" + strings.Repeat(string(zero), 26)

// between returns a key strictly between before and after.
//
// An empty before means "before everything" and an empty after means "after
// everything", which is how the two ends of a list are asked for without a call
// for each. An empty string is never itself a valid key, so using it as the
// open end costs no ambiguity.
//
// Never fails. Keys arrive over the network from replicas running other
// versions of this code, and refusing to place a card because somebody else's
// sort key is not in a shape this file recognises would be a worse failure than
// putting the card in a plausible place. Anything unparseable falls back to the
// plain midpoint below, which copes with any base-62 string.
func between(before, after string) string {
	if key, err := keyBetween(before, after); err == nil {
		return key
	}
	return fallbackBetween(before, after)
}

// keyBetween is the whole rule, with "" for the open ends.
func keyBetween(a, b string) (string, error) {
	if a != "" {
		if err := checkKey(a); err != nil {
			return "", err
		}
	}
	if b != "" {
		if err := checkKey(b); err != nil {
			return "", err
		}
	}
	if a != "" && b != "" && a >= b {
		return "", fmt.Errorf("workbench: %q is not before %q", a, b)
	}

	if a == "" {
		if b == "" {
			return firstKey, nil
		}
		intB, err := integerPart(b)
		if err != nil {
			return "", err
		}
		// Nowhere below this integer, so make room inside it instead.
		if intB == smallestInteger {
			frac, err := midpoint("", b[len(intB):], true)
			if err != nil {
				return "", err
			}
			return intB + frac, nil
		}
		// b has a fractional part, so its own integer is already below it.
		if intB < b {
			return intB, nil
		}
		down, ok := decrementInteger(intB)
		if !ok {
			return "", errors.New("workbench: no room below the smallest key")
		}
		return down, nil
	}

	if b == "" {
		intA, err := integerPart(a)
		if err != nil {
			return "", err
		}
		// The common case, and the whole point of the integer part: appending
		// is one increment, and the key stays the length it was.
		if up, ok := incrementInteger(intA); ok {
			return up, nil
		}
		frac, err := midpoint(a[len(intA):], "", false)
		if err != nil {
			return "", err
		}
		return intA + frac, nil
	}

	intA, err := integerPart(a)
	if err != nil {
		return "", err
	}
	intB, err := integerPart(b)
	if err != nil {
		return "", err
	}
	if intA == intB {
		frac, err := midpoint(a[len(intA):], b[len(intB):], true)
		if err != nil {
			return "", err
		}
		return intA + frac, nil
	}

	up, ok := incrementInteger(intA)
	if !ok {
		return "", errors.New("workbench: no room above the largest key")
	}
	if up < b {
		return up, nil
	}
	frac, err := midpoint(a[len(intA):], "", false)
	if err != nil {
		return "", err
	}
	return intA + frac, nil
}

/* -------------------------------------------------------------------------- */
/* The integer part                                                           */
/* -------------------------------------------------------------------------- */

// integerLength is how many bytes an integer part has, read from its first one.
//
// 'a'..'z' count upwards from two bytes, 'Z'..'A' downwards. That is what makes
// a longer integer sort after a shorter one without any padding.
func integerLength(head byte) (int, error) {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2, nil
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2, nil
	}
	return 0, fmt.Errorf("workbench: not an order key: %q", head)
}

func integerPart(key string) (string, error) {
	if key == "" {
		return "", errors.New("workbench: order key is empty")
	}
	n, err := integerLength(key[0])
	if err != nil {
		return "", err
	}
	if n > len(key) {
		return "", fmt.Errorf("workbench: order key is too short: %q", key)
	}
	return key[:n], nil
}

func checkInteger(in string) error {
	if in == "" {
		return errors.New("workbench: integer part is empty")
	}
	n, err := integerLength(in[0])
	if err != nil {
		return err
	}
	if n != len(in) {
		return fmt.Errorf("workbench: bad integer part: %q", in)
	}
	return nil
}

// checkKey rejects anything the arithmetic below cannot take.
func checkKey(key string) error {
	if key == smallestInteger {
		return errors.New("workbench: the smallest key has no room below it")
	}
	in, err := integerPart(key)
	if err != nil {
		return err
	}
	// A trailing zero is a second spelling of the same value, and the midpoint
	// relies on there being only one.
	if strings.HasSuffix(key[len(in):], string(zero)) {
		return fmt.Errorf("workbench: trailing zero: %q", key)
	}
	for i := 1; i < len(key); i++ {
		if digitIndex(key[i]) < 0 {
			return fmt.Errorf("workbench: not base 62: %q", key)
		}
	}
	return nil
}

// incrementInteger returns the next integer up, and false when there is no more
// room.
func incrementInteger(in string) (string, bool) {
	if err := checkInteger(in); err != nil {
		return "", false
	}
	head := in[0]
	ds := []byte(in[1:])
	carry := true
	for i := len(ds) - 1; carry && i >= 0; i-- {
		next := digitIndex(ds[i]) + 1
		if next == base {
			ds[i] = zero
		} else {
			ds[i] = digits[next]
			carry = false
		}
	}
	if !carry {
		return string(head) + string(ds), true
	}

	// The digits rolled over, so the integer needs to be one longer -- which
	// means the next head along, since the head is the length.
	if head == 'z' {
		return "", false
	}
	if head == 'Z' {
		return firstKey, true
	}
	nextHead := head + 1
	if nextHead > 'a' {
		ds = append(ds, zero)
	} else {
		ds = ds[:len(ds)-1]
	}
	return string(nextHead) + string(ds), true
}

// decrementInteger returns the next integer down, and false when there is no
// more room.
func decrementInteger(in string) (string, bool) {
	if err := checkInteger(in); err != nil {
		return "", false
	}
	head := in[0]
	ds := []byte(in[1:])
	borrow := true
	for i := len(ds) - 1; borrow && i >= 0; i-- {
		next := digitIndex(ds[i]) - 1
		if next < 0 {
			ds[i] = digits[base-1]
		} else {
			ds[i] = digits[next]
			borrow = false
		}
	}
	if !borrow {
		return string(head) + string(ds), true
	}

	if head == 'A' {
		return "", false
	}
	if head == 'a' {
		return "Z" + string(digits[base-1]), true
	}
	nextHead := head - 1
	if nextHead < 'Z' {
		ds = append(ds, digits[base-1])
	} else {
		ds = ds[:len(ds)-1]
	}
	return string(nextHead) + string(ds), true
}

/* -------------------------------------------------------------------------- */
/* The fractional part                                                        */
/* -------------------------------------------------------------------------- */

// midpoint returns a fraction strictly between a and b, where bounded false
// means there is no upper bound and b is ignored.
//
// Greenspan's midpoint. Short and fiddly, and worth using rather than an
// obvious one because the obvious ones do not terminate when the two bounds are
// adjacent digits. Neither argument may have a trailing zero, which is what
// checkKey guarantees.
func midpoint(a, b string, bounded bool) (string, error) {
	if bounded {
		// Copy the common prefix off and recurse on what is left: at the first
		// digit the two bounds look identical and there is no room between
		// them.
		n := 0
		for n < len(b) {
			ac := zero
			if n < len(a) {
				ac = a[n]
			}
			if ac != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			rest, err := midpoint(sliceFrom(a, n), b[n:], true)
			if err != nil {
				return "", err
			}
			return b[:n] + rest, nil
		}
	}

	digitA := 0
	if a != "" {
		digitA = digitIndex(a[0])
	}
	digitB := base
	if bounded {
		if b == "" {
			return "", errors.New("workbench: empty upper bound")
		}
		digitB = digitIndex(b[0])
	}
	if digitA < 0 || digitB < 0 {
		return "", errors.New("workbench: fraction is not base 62")
	}

	if digitB-digitA > 1 {
		// Integer round-half-up, which is what the reference implementation's
		// Math.round does for these two always-positive digits.
		return string(digits[(digitA+digitB+1)/2]), nil
	}
	// The digits are adjacent, so the answer is longer than either bound.
	if bounded && len(b) > 1 {
		return b[:1], nil
	}
	rest, err := midpoint(sliceFrom(a, 1), "", false)
	if err != nil {
		return "", err
	}
	return string(digits[digitA]) + rest, nil
}

// fallbackBetween is what to do with a key this file did not write.
//
// A plain base-62 midpoint, ignoring the integer part entirely: it cannot
// produce a well-formed order key, but it can always produce a string in the
// right place in the ordering, which is what the caller actually needs. Only
// reached for keys from another implementation, or an older one of this.
func fallbackBetween(before, after string) string {
	// Appending after a key this scheme did not write, which is mostly one
	// specific key: the zero-padded decimal index this package used to mint.
	// Those are six ASCII digits, every well-formed key begins with a letter,
	// and letters sort above digits -- so firstKey is above all of them and the
	// old numbering becomes a prefix of the new one instead of something that
	// has to be migrated with a move-node per card.
	if after == "" && before < firstKey {
		return firstKey
	}

	lower := strip(before)
	upper := ""
	bounded := after != ""
	if bounded {
		upper = strip(after)
	}
	// Also the guard that keeps an empty upper bound out of midpoint.
	if bounded && lower >= upper {
		return lower + string(digits[base/2])
	}
	if mid, err := midpoint(lower, upper, bounded); err == nil {
		return mid
	}
	return lower + string(digits[base/2])
}

// strip reduces a key to the base-62 digits in it, with trailing zeros removed
// because the midpoint treats those as a second spelling of the same value.
func strip(key string) string {
	var out strings.Builder
	for i := 0; i < len(key); i++ {
		if digitIndex(key[i]) >= 0 {
			out.WriteByte(key[i])
		}
	}
	return strings.TrimRight(out.String(), string(zero))
}

// digitIndex is the value of a base-62 digit, or -1 if it is not one.
func digitIndex(b byte) int { return strings.IndexByte(digits, b) }

// sliceFrom is s[n:], or "" when n is past the end.
func sliceFrom(s string, n int) string {
	if n >= len(s) {
		return ""
	}
	return s[n:]
}
