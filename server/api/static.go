// The viewer, served from the same origin as the API it reads.
//
// The read key travels in a URL fragment, and a fragment is only ever resolved
// by the browser -- so the page and the API it calls have to be the same
// origin, or the key can never reach a request. That is what this file is for:
// not a convenience, a requirement of how the viewer is let in.

package api

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

// static serves the viewer's built files, or refuses in the API's own voice
// when this server was started without one.
//
// Written by hand rather than with [http.FileServer], which is close and wrong
// in three ways for a single-page viewer: it redirects /index.html to /, it
// lists a directory that has no index in it, and it answers 404 for a path the
// client router owns. All three would have to be undone.
func (a *API) static() http.Handler {
	// No site: the root behaves exactly as it did before anything was mounted
	// there, so an API-only deployment is not a deployment with a broken page.
	if a.site == nil {
		return a.plain(func(http.ResponseWriter, *http.Request) error {
			return apiError{http.StatusNotFound, "not_found", "no such endpoint"}
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HEAD as well as GET: ServeFileFS answers one by writing the headers
		// and no body, and a monitor asking HEAD / should not see a 405.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			a.fail(w, r, apiError{http.StatusMethodNotAllowed, "method_not_allowed",
				"wrong method for this endpoint"})
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		// Anything that is not a file in the site is the index, including a
		// path that does not even parse. A viewer link is mistyped far more
		// often than it is attacked, and the page it lands on can say so --
		// which is more than a 400 with no markup in it can.
		//
		// The index by name goes the same way as the index by fallback, so
		// that /index.html is answered rather than bounced to / first.
		if name == "" || name == "." || name == indexFile || !fs.ValidPath(name) {
			a.serveIndex(w, r)
			return
		}
		info, err := fs.Stat(a.site, name)
		if err != nil || info.IsDir() {
			a.serveIndex(w, r)
			return
		}

		setCache(w, name)
		http.ServeFileFS(w, r, a.site, name)
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
// index while the request says otherwise is either the wrong answer or, if
// the request is rewritten to agree, a redirect loop. ServeContent looks at
// the name it is given and nothing else, and still answers a conditional
// request with a 304.
func (a *API) serveIndex(w http.ResponseWriter, r *http.Request) {
	// Opened and read before a single header is set, because a missing or
	// unreadable index is the server's own fault and has to be able to become
	// a JSON 500 -- which it cannot once a Cache-Control for a page that will
	// never be sent is already on the response.
	page, err := fs.ReadFile(a.site, indexFile)
	if err != nil {
		a.fail(w, r, fmt.Errorf("reading %s from the site: %w", indexFile, err))
		return
	}
	// The mod time is what makes the conditional request work. A site with no
	// usable one still serves, it just always serves in full.
	var modified time.Time
	if info, err := fs.Stat(a.site, indexFile); err == nil {
		modified = info.ModTime()
	}

	setCache(w, indexFile)
	http.ServeContent(w, r, indexFile, modified, bytes.NewReader(page))
}

// setCache decides how long a file may be held.
//
// The split is possible only because Vite hashes what it emits into assets/:
// a hashed name can never mean two different things, so it is safe forever,
// and everything that names it is in the index -- which therefore may not be
// held at all, or a deploy would be invisible to anyone who had already
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
