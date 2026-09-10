// Package update keeps a running Work up to date with its own releases.
//
// The split here mirrors the rest of the app: this package owns the work --
// asking GitHub what the latest release is, deciding whether it is newer than
// the binary that is running, fetching it and swapping it in -- and the
// frontend only ever sees one semantic event and one service call. Nothing
// about HTTP, temporary files or process replacement crosses the Wails bridge.
//
// The version this compares against is stamped in at link time
// (-X main.version=...) and handed to New by main. An unstamped build is a
// development build, and Work deliberately neither nags nor replaces it: see
// IsDevBuild.
package update

import "strings"

// DevVersion is what main.version holds when nothing stamped it, which is
// every `go build` and every `task build` that is not a release build.
const DevVersion = "dev"

// IsDevBuild reports whether v is a development build rather than a release.
//
// It is checked in two places, for two different reasons: a development build
// is not polled for updates, because a local build is usually *ahead* of the
// newest release and being told to downgrade to it is noise; and it is never
// replaced, because the binary sitting in bin/ is somebody's working tree
// output, not something Work has any business overwriting.
func IsDevBuild(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == DevVersion || v == "0.0.0"
}

// Compare orders two release versions the way semver does, without pulling in
// a semver package for it: releases here are `vMAJOR.MINOR.PATCH` tags written
// by one workflow, so the interesting cases are a missing `v`, a missing field
// and a prerelease suffix.
//
// It returns -1 if a sorts before b, 0 if they are equal, and 1 if a sorts
// after b. Fields are compared numerically, so v0.10.0 is correctly newer than
// v0.9.0 -- the bug a plain string compare would have. A missing field reads as
// zero, so "1.2" and "1.2.0" are the same version. A prerelease suffix
// ("1.2.0-rc.1") sorts before the release it leads to, and two prereleases of
// the same core version fall back to comparing the suffix as text, which is
// right for -rc.1/-rc.2 and close enough for anything else this repo will tag.
//
// Anything non-numeric where a number belongs reads as zero rather than an
// error. The caller's choice when a tag is unparseable is to do nothing, and
// "not newer" already means exactly that.
func Compare(a, b string) int {
	acore, apre := splitVersion(a)
	bcore, bpre := splitVersion(b)

	for acore != "" || bcore != "" {
		var an, bn int
		an, acore = nextField(acore)
		bn, bcore = nextField(bcore)
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
	}

	switch {
	case apre == "" && bpre == "":
		return 0
	case apre == "":
		// b is a prerelease of the same core version, so a is the real thing.
		return 1
	case bpre == "":
		return -1
	}
	return strings.Compare(apre, bpre)
}

// IsNewer reports whether the release tagged latest is newer than the running
// version. It is the only question the poller asks.
func IsNewer(latest, current string) bool {
	return Compare(latest, current) > 0
}

// splitVersion strips the tag's leading v and separates the numeric core from
// any prerelease or build-metadata suffix.
func splitVersion(v string) (core, pre string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Build metadata does not affect ordering at all, so it goes first and is
	// then discarded along with whatever followed it.
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

// nextField reads the leading number of a dotted version core and returns it
// with the remainder after the dot. It never lengthens its input, so the loop
// in Compare always terminates.
func nextField(s string) (n int, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		n = n*10 + int(s[i]-'0')
		i++
	}
	// Skip to just past the next dot; a field of trailing junk is dropped
	// rather than being carried into the following comparison.
	if j := strings.IndexByte(s, '.'); j >= 0 {
		return n, s[j+1:]
	}
	return n, ""
}
