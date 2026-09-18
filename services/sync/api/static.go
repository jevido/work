// The viewer, served from the same origin as the API it reads.
//
// The read key travels in a URL fragment, and a fragment is only ever resolved
// by the browser -- so the page and the API it calls have to be the same
// origin, or the key can never reach a request. That is what this file is for:
// not a convenience, a requirement of how the viewer is let in.

package api

import (
	"net/http"

	"dev.jevido/work/packages/site"
)

// static serves the viewer's built files, or refuses in the API's own voice
// when this server was started without one.
//
// The serving itself is packages/site, which Charted uses for the same job.
// What stays here is the part that is this server's rather than any server's:
// an absent site is a 404 in the API's JSON shape and not a blank page, a wrong
// method is the same apiError every other endpoint answers with, and an
// unreadable index is the same 500.
func (a *API) static() http.Handler {
	// No site: the root behaves exactly as it did before anything was mounted
	// there, so an API-only deployment is not a deployment with a broken page.
	if a.site == nil {
		return a.plain(func(http.ResponseWriter, *http.Request) error {
			return apiError{http.StatusNotFound, "not_found", "no such endpoint"}
		})
	}

	return site.Handler(a.site, site.Options{
		OnBroken: func(w http.ResponseWriter, r *http.Request, err error) {
			a.fail(w, r, err)
		},
		OnMethod: func(w http.ResponseWriter, r *http.Request) {
			a.fail(w, r, apiError{http.StatusMethodNotAllowed, "method_not_allowed",
				"wrong method for this endpoint"})
		},
	})
}
