// Package site serves a built single-page bundle: the files a bundler emitted,
// with every unknown path falling back to index.html.
//
// Shared because two servers here do exactly this. The sync server hands out
// apps/website and Charted hands out apps/charted, and the second one was a
// second implementation of the first -- the same fallback, the same cache
// split, the same three edge cases, with only one of them tested. What each
// caller keeps for itself is how a refusal is worded, which is the one part
// that genuinely differs: both answer JSON, in their own shape.
package site

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// indexFile is the page every unmatched path falls back to.
const indexFile = "index.html"

// Options are the two answers the caller owns.
//
// Both are optional and both have a plain-text default, so a caller that does
// not care gets something sane rather than a nil dereference.
type Options struct {
	// OnBroken is called when the site itself cannot be read -- a missing or
	// unreadable index.html. That is the server's own fault, not the
	// request's, so it is the caller's 500 to word.
	OnBroken func(w http.ResponseWriter, r *http.Request, err error)

	// OnMethod is called for anything that is not GET or HEAD.
	OnMethod func(w http.ResponseWriter, r *http.Request)
}

// Handler serves fsys, falling back to its index.html.
//
// Written by hand rather than with [http.FileServer], which is close and wrong
// in three ways for a single-page bundle: it redirects /index.html to /, it
// lists a directory that has no index in it, and it answers 404 for a path the
// client router owns. All three would have to be undone.
func Handler(fsys fs.FS, opts Options) http.Handler {
	if opts.OnBroken == nil {
		opts.OnBroken = func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "the site is not readable", http.StatusInternalServerError)
		}
	}
	if opts.OnMethod == nil {
		opts.OnMethod = func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "wrong method for a page", http.StatusMethodNotAllowed)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HEAD as well as GET: ServeFileFS answers one by writing the headers
		// and no body, and a monitor asking HEAD / should not see a 405.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			opts.OnMethod(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		// Anything that is not a file in the site is the index, including a
		// path that does not even parse. A link is mistyped far more often
		// than it is attacked, and the page it lands on can say so -- which is
		// more than a 400 with no markup in it can.
		//
		// The index by name goes the same way as the index by fallback, so
		// that /index.html is answered rather than bounced to / first.
		if name == "" || name == "." || name == indexFile || !fs.ValidPath(name) {
			serveIndex(w, r, fsys, opts)
			return
		}
		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			serveIndex(w, r, fsys, opts)
			return
		}

		setCache(w, name)
		http.ServeFileFS(w, r, fsys, name)
	})
}

// serveIndex answers with the page, at 200.
//
// Not 404-with-a-body: the path is one the client router resolves, and a
// browser reloading a route it was already on should get the same page it had,
// not a status that says the route does not exist.
//
// Read whole and handed to [http.ServeContent] rather than served by
// [http.ServeFileFS], which cannot be used here at all: it reasons about
// r.URL.Path, which for a fallback is some route this file knows nothing
// about. It refuses a path holding ".." with a plain-text 400, and it
// redirects anything ending in /index.html to ./ -- so pointing it at the
// index while the request says otherwise is either the wrong answer or, if the
// request is rewritten to agree, a redirect loop. ServeContent looks at the
// name it is given and nothing else, and still answers a conditional request
// with a 304.
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS, opts Options) {
	// Opened and read before a single header is set, because a missing or
	// unreadable index is the server's own fault and has to be able to become
	// the caller's 500 -- which it cannot once a Cache-Control for a page that
	// will never be sent is already on the response.
	page, err := fs.ReadFile(fsys, indexFile)
	if err != nil {
		opts.OnBroken(w, r, fmt.Errorf("site: reading %s: %w", indexFile, err))
		return
	}
	// The mod time is what makes the conditional request work. A site with no
	// usable one still serves, it just always serves in full.
	var modified time.Time
	if info, err := fs.Stat(fsys, indexFile); err == nil {
		modified = info.ModTime()
	}

	setCache(w, indexFile)
	http.ServeContent(w, r, indexFile, modified, bytes.NewReader(page))
}

// setCache decides how long a file may be held.
//
// The split is possible only because the bundler hashes what it emits into
// assets/: a hashed name can never mean two different things, so it is safe
// forever, and everything that names it is in the index -- which therefore may
// not be held at all, or a deploy would be invisible to anyone who had already
// visited.
func setCache(w http.ResponseWriter, name string) {
	switch {
	case name == indexFile:
		// no-cache, not no-store. The browser still asks, and still gets a 304
		// when nothing changed; it simply may not answer from its own copy
		// without asking first.
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(name, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		// A favicon, a manifest, anything else at the root: named by hand, so
		// it can change under its own name, but not often enough to ask every
		// time.
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
}
