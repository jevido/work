package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"dev.jevido/work/services/charted/render"
	"dev.jevido/work/services/charted/store"
)

const token = "s3cret"

func ask(t *testing.T, h http.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q is not the JSON this expects: %v", rec.Body.String(), err)
	}
	return out
}

/* -------------------------------------------------------------------------- */
/* Reading                                                                    */
/* -------------------------------------------------------------------------- */

func TestReadsNeedNoToken(t *testing.T) {
	db := newFake()
	db.spaces = []store.NavSpace{{Slug: "studio", Title: "Studio"}}
	db.pages["studio/install"] = store.Page{Space: "studio", Slug: "install", Title: "Install"}
	h := New(db, token, nil, quiet())

	for _, path := range []string{"/v1/health", "/v1/nav", "/v1/pages/studio/install", "/v1/search?q=x"} {
		if rec := ask(t, h, http.MethodGet, path, "", ""); rec.Code != http.StatusOK {
			t.Errorf("GET %s with no token = %d, want 200", path, rec.Code)
		}
	}
}

// The slug is a wildcard, so a nested page has to arrive as one string with its
// slashes intact rather than as the first segment alone.
func TestPageReadsANestedSlug(t *testing.T) {
	db := newFake()
	db.pages["studio/guides/first-run"] = store.Page{
		Space: "studio", Slug: "guides/first-run", Title: "First run",
	}
	h := New(db, token, nil, quiet())

	rec := ask(t, h, http.MethodGet, "/v1/pages/studio/guides/first-run", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := decode[store.Page](t, rec); got.Slug != "guides/first-run" {
		t.Errorf("slug = %q, want the whole path", got.Slug)
	}
}

func TestMissingPageIs404(t *testing.T) {
	h := New(newFake(), token, nil, quiet())

	rec := ask(t, h, http.MethodGet, "/v1/pages/studio/nowhere", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	if got := decode[map[string]string](t, rec)["error"]; got != "not_found" {
		t.Errorf("error = %q, want not_found", got)
	}
}

// An empty query is not an error and not a scan of everything: it is no
// results, because a search box that has not been typed into yet asks for
// nothing.
func TestEmptySearchIsEmpty(t *testing.T) {
	db := newFake()
	db.hits = []store.Hit{{Space: "studio", Slug: "install"}}
	h := New(db, token, nil, quiet())

	rec := ask(t, h, http.MethodGet, "/v1/search?q=%20", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if hits := decode[map[string][]store.Hit](t, rec)["hits"]; len(hits) != 0 {
		t.Errorf("hits = %v, want none", hits)
	}
}

// A database that is down is a 503 on the health check rather than a 200 with
// an unhappy body: the healthcheck in the image reads the status.
func TestHealthFailsWhenTheDatabaseIsDown(t *testing.T) {
	db := newFake()
	db.pingErr = errDown
	h := New(db, token, nil, quiet())

	if rec := ask(t, h, http.MethodGet, "/v1/health", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rec.Code)
	}
}

/* -------------------------------------------------------------------------- */
/* Writing                                                                    */
/* -------------------------------------------------------------------------- */

func TestWritesNeedTheToken(t *testing.T) {
	db := newFake()
	h := New(db, token, nil, quiet())
	body := `{"title":"Install","markdown":"# Install"}`

	cases := map[string]int{
		"":       http.StatusUnauthorized,
		"not-it": http.StatusUnauthorized,
		token:    http.StatusOK,
	}
	for bearer, want := range cases {
		rec := ask(t, h, http.MethodPut, "/v1/pages/studio/install", body, bearer)
		if rec.Code != want {
			t.Errorf("PUT with bearer %q = %d, want %d", bearer, rec.Code, want)
		}
	}
}

// A deployment with no token configured refuses every write outright. The
// failure it guards against is the opposite default: a site anybody who finds
// it can publish to.
func TestNoTokenMeansReadOnly(t *testing.T) {
	h := New(newFake(), "", nil, quiet())

	rec := ask(t, h, http.MethodPut, "/v1/pages/studio/install",
		`{"title":"Install","markdown":"x"}`, "anything")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if got := decode[map[string]string](t, rec)["error"]; got != "read_only" {
		t.Errorf("error = %q, want read_only", got)
	}
}

// The space and the slug come from the path, not from the body: a body that
// names a different page would be a way to write anywhere with one URL's
// permission.
func TestPutTakesItsAddressFromThePath(t *testing.T) {
	db := newFake()
	h := New(db, token, nil, quiet())

	rec := ask(t, h, http.MethodPut, "/v1/pages/studio/guides/first-run",
		`{"space":"elsewhere","slug":"somewhere-else","title":"First run","markdown":"hi"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	if db.lastWrite.Space != "studio" || db.lastWrite.Slug != "guides/first-run" {
		t.Errorf("wrote %s/%s, want studio/guides/first-run", db.lastWrite.Space, db.lastWrite.Slug)
	}
}

func TestPutRefusesAPageWithNoTitle(t *testing.T) {
	h := New(newFake(), token, nil, quiet())

	rec := ask(t, h, http.MethodPut, "/v1/pages/studio/install", `{"markdown":"x"}`, token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

// Broken links come back with a 200. Refusing the write would mean
// documentation has to be written from the leaves inwards, which is not how
// anybody writes it.
func TestBrokenLinksAreReportedNotRefused(t *testing.T) {
	db := newFake()
	db.writeSaid = []render.Link{{Space: "studio", Slug: "roles", Label: "roles"}}
	h := New(db, token, nil, quiet())

	rec := ask(t, h, http.MethodPut, "/v1/pages/studio/invites",
		`{"title":"Invites","markdown":"see [roles](roles)"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	got := decode[struct {
		Broken []render.Link `json:"broken"`
	}](t, rec)
	if len(got.Broken) != 1 || got.Broken[0].Slug != "roles" {
		t.Errorf("broken = %+v, want the one link", got.Broken)
	}
}

// An array rather than null, so a caller can loop over it without asking
// whether it is there.
func TestNoBrokenLinksIsAnEmptyArray(t *testing.T) {
	h := New(newFake(), token, nil, quiet())

	rec := ask(t, h, http.MethodPut, "/v1/pages/studio/install",
		`{"title":"Install","markdown":"nothing to link"}`, token)
	if !strings.Contains(rec.Body.String(), `"broken":[]`) {
		t.Errorf("body = %s, want an empty array", rec.Body.String())
	}
}

func TestDeleteRemovesAPageAndAnswers404ForAMissingOne(t *testing.T) {
	db := newFake()
	db.pages["studio/install"] = store.Page{Space: "studio", Slug: "install"}
	h := New(db, token, nil, quiet())

	if rec := ask(t, h, http.MethodDelete, "/v1/pages/studio/install", "", token); rec.Code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", rec.Code)
	}
	if len(db.deleted) != 1 || db.deleted[0] != "studio/install" {
		t.Errorf("deleted %v, want studio/install", db.deleted)
	}
	if rec := ask(t, h, http.MethodDelete, "/v1/pages/studio/install", "", token); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
}

// A space with no title is named after itself rather than refused: the title is
// one call away and a nameless section is worse than an obvious one.
func TestPutSpaceFallsBackToTheSlug(t *testing.T) {
	db := newFake()
	h := New(db, token, nil, quiet())

	if rec := ask(t, h, http.MethodPut, "/v1/spaces/studio", `{}`, token); rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if db.lastSpace.Slug != "studio" || db.lastSpace.Title != "studio" {
		t.Errorf("space = %+v, want the slug as both", db.lastSpace)
	}
}

// The database's own words never reach the caller: an error from Postgres names
// tables.
func TestInternalErrorsAreNotEchoed(t *testing.T) {
	db := newFake()
	db.writeErr = errDown
	h := New(db, token, nil, quiet())

	rec := ask(t, h, http.MethodPut, "/v1/pages/studio/install",
		`{"title":"Install","markdown":"x"}`, token)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), errDown.Error()) {
		t.Errorf("body repeats the database's error: %s", rec.Body)
	}
}

/* -------------------------------------------------------------------------- */
/* The reader in front of it                                                  */
/* -------------------------------------------------------------------------- */

// Every path that is not the API is the page, and the API is still the API.
func TestTheReaderIsServedWithoutSwallowingTheAPI(t *testing.T) {
	db := newFake()
	h := New(db, token, fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
	}, quiet())

	if rec := ask(t, h, http.MethodGet, "/studio/install", "", ""); rec.Code != http.StatusOK {
		t.Errorf("a page URL = %d, want 200", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "doctype") {
		t.Errorf("a page URL served %q, want the index", rec.Body.String())
	}

	// The one that matters: a static handler mounted in front of a JSON API
	// breaks the API by answering 200 and HTML to something that parses JSON.
	rec := ask(t, h, http.MethodGet, "/v1/pages/studio/nowhere", "", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("a missing page = %d, want the API's 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "doctype") {
		t.Error("the API answered with the page")
	}
}

func TestWithNoReaderTheRootIsNotServed(t *testing.T) {
	h := New(newFake(), token, nil, quiet())

	if rec := ask(t, h, http.MethodGet, "/", "", ""); rec.Code == http.StatusOK {
		t.Errorf("root = 200 with no site configured, want a refusal")
	}
}
