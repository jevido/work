// Package api is Charted's HTTP surface: a read side anybody can call and a
// write side that needs the token.
//
// The split is the whole security model. Reading is public because a
// documentation site is public; writing is one bearer token because the only
// writer is documentation mode, running on somebody's machine, with a token in
// its config. There are no users here and no sessions -- adding either would be
// building an editor nobody asked for.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"dev.jevido/work/packages/site"
	"dev.jevido/work/services/charted/render"
	"dev.jevido/work/services/charted/store"
)

// Store is the half of the database this package uses.
//
// Declared here rather than imported as a concrete type, so the HTTP layer can
// be tested with no Postgres anywhere near it -- the same shape services/sync
// uses, and the reason its handlers have tests while these did not.
type Store interface {
	Ping(ctx context.Context) error
	Nav(ctx context.Context) ([]store.NavSpace, error)
	Page(ctx context.Context, space, slug string) (store.Page, error)
	Search(ctx context.Context, query string, limit int) ([]store.Hit, error)
	UpsertPage(ctx context.Context, in store.PageInput) ([]render.Link, error)
	DeletePage(ctx context.Context, space, slug string) error
	UpsertSpace(ctx context.Context, in store.Space) error
	BrokenLinks(ctx context.Context) ([]store.Broken, error)
}

type API struct {
	store  Store
	token  string
	site   http.Handler
	logger *slog.Logger
}

// New builds the handler. token may be empty, and then the write side is
// refused outright rather than left open: a deployment that forgot to set it is
// read-only, not writable by anyone who finds it.
//
// siteFS is the built reader, or nil for an API with no page in front of it.
func New(s Store, token string, siteFS fs.FS, logger *slog.Logger) http.Handler {
	a := &API{store: s, token: token, logger: logger}
	if siteFS != nil {
		// The same serving as the sync server's, in this API's voice: a broken
		// site is this package's 500 and a wrong method is its 405, both as the
		// JSON everything else here answers with.
		a.site = site.Handler(siteFS, site.Options{
			OnBroken: func(w http.ResponseWriter, _ *http.Request, err error) {
				a.oops(w, err)
			},
			OnMethod: func(w http.ResponseWriter, _ *http.Request) {
				a.fail(w, http.StatusMethodNotAllowed, "method_not_allowed",
					"wrong method for a page")
			},
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", a.health)
	mux.HandleFunc("GET /v1/nav", a.nav)
	mux.HandleFunc("GET /v1/search", a.search)
	mux.HandleFunc("GET /v1/pages/{space}/{slug...}", a.page)
	mux.HandleFunc("PUT /v1/pages/{space}/{slug...}", a.write(a.putPage))
	mux.HandleFunc("DELETE /v1/pages/{space}/{slug...}", a.write(a.deletePage))
	mux.HandleFunc("PUT /v1/spaces/{space}", a.write(a.putSpace))
	mux.HandleFunc("GET /v1/links/broken", a.write(a.brokenLinks))

	// Everything else is the reader, which owns its own routing. A page URL is
	// /<space>/<slug>, and the server has no opinion about it beyond handing
	// over the same index.html.
	if a.site != nil {
		mux.Handle("/", a.site)
	}
	return mux
}

/* -------------------------------------------------------------------------- */
/* Read                                                                       */
/* -------------------------------------------------------------------------- */

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		a.fail(w, http.StatusServiceUnavailable, "database", "the database is not reachable")
		return
	}
	a.ok(w, map[string]string{"status": "ok"})
}

func (a *API) nav(w http.ResponseWriter, r *http.Request) {
	spaces, err := a.store.Nav(r.Context())
	if err != nil {
		a.oops(w, err)
		return
	}
	// Belt as well as braces: the store returns an empty slice, and a Store
	// that did not would put a null in front of the reader's first render.
	if spaces == nil {
		spaces = []store.NavSpace{}
	}
	a.ok(w, map[string]any{"spaces": spaces})
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		a.ok(w, map[string]any{"hits": []any{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	hits, err := a.store.Search(r.Context(), query, limit)
	if err != nil {
		a.oops(w, err)
		return
	}
	a.ok(w, map[string]any{"hits": hits})
}

func (a *API) page(w http.ResponseWriter, r *http.Request) {
	page, err := a.store.Page(r.Context(), r.PathValue("space"), cleanSlug(r.PathValue("slug")))
	if errors.Is(err, store.ErrNotFound) {
		a.fail(w, http.StatusNotFound, "not_found", "no such page")
		return
	}
	if err != nil {
		a.oops(w, err)
		return
	}
	a.ok(w, page)
}

/* -------------------------------------------------------------------------- */
/* Write                                                                      */
/* -------------------------------------------------------------------------- */

// write wraps a handler in the token check.
//
// Constant-time comparison, and a refusal that says nothing about why: a token
// check that answers "wrong token" faster than "no token" is a token check that
// can be measured.
func (a *API) write(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			a.fail(w, http.StatusForbidden, "read_only",
				"this deployment has no write token, so nothing can be published to it")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			a.fail(w, http.StatusUnauthorized, "unauthorized", "not a writer")
			return
		}
		next(w, r)
	}
}

func (a *API) putPage(w http.ResponseWriter, r *http.Request) {
	var in store.PageInput
	if !a.read(w, r, &in) {
		return
	}
	in.Space = r.PathValue("space")
	in.Slug = cleanSlug(r.PathValue("slug"))
	if in.Slug == "" || in.Title == "" {
		a.fail(w, http.StatusBadRequest, "incomplete", "a page needs a slug and a title")
		return
	}

	broken, err := a.store.UpsertPage(r.Context(), in)
	if err != nil {
		a.oops(w, err)
		return
	}
	// 200 with the broken links in the body, not a 4xx: the page was written.
	// Reporting a link to a page that is not published yet as a failure would
	// force documentation to be written backwards, deepest page first.
	if broken == nil {
		// An empty array rather than null, so a caller can loop over it
		// without asking whether it is there.
		broken = []render.Link{}
	}
	a.ok(w, map[string]any{"space": in.Space, "slug": in.Slug, "broken": broken})
}

func (a *API) deletePage(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeletePage(r.Context(), r.PathValue("space"), cleanSlug(r.PathValue("slug")))
	if errors.Is(err, store.ErrNotFound) {
		a.fail(w, http.StatusNotFound, "not_found", "no such page")
		return
	}
	if err != nil {
		a.oops(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) putSpace(w http.ResponseWriter, r *http.Request) {
	var in store.Space
	if !a.read(w, r, &in) {
		return
	}
	in.Slug = r.PathValue("space")
	if in.Title == "" {
		in.Title = in.Slug
	}
	if err := a.store.UpsertSpace(r.Context(), in); err != nil {
		a.oops(w, err)
		return
	}
	a.ok(w, in)
}

func (a *API) brokenLinks(w http.ResponseWriter, r *http.Request) {
	broken, err := a.store.BrokenLinks(r.Context())
	if err != nil {
		a.oops(w, err)
		return
	}
	a.ok(w, map[string]any{"broken": broken})
}

/* -------------------------------------------------------------------------- */
/* Plumbing                                                                   */
/* -------------------------------------------------------------------------- */

// cleanSlug normalises the {slug...} wildcard: no leading or trailing slash, no
// ".." to climb out of a space with.
func cleanSlug(raw string) string {
	slug := strings.Trim(raw, "/")
	if slug == "" || strings.Contains(slug, "..") {
		return ""
	}
	return slug
}

func (a *API) read(w http.ResponseWriter, r *http.Request, into any) bool {
	// A megabyte. A page is prose; anything larger is a mistake or a bad
	// actor, and both are better refused than buffered.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		a.fail(w, http.StatusBadRequest, "bad_json", "the body is not the JSON this expects")
		return false
	}
	return true
}

func (a *API) ok(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		a.logger.Error("writing a response", "err", err)
	}
}

// oops is the one place an unexpected error becomes a response. The detail goes
// to the log, never to the caller: an error message from Postgres names tables.
func (a *API) oops(w http.ResponseWriter, err error) {
	a.logger.Error("request failed", "err", err)
	a.fail(w, http.StatusInternalServerError, "internal", "something went wrong here")
}

func (a *API) fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message}); err != nil {
		a.logger.Error("writing an error", "err", err)
	}
}
