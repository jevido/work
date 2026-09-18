package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// A site shaped like what Vite emits: a page, a hashed bundle under assets/,
// and one file at the root named by hand.
func testSite() fstest.MapFS {
	return fstest.MapFS{
		"index.html":               {Data: []byte(`<!doctype html><div id="app"></div>`)},
		"assets/app-abc123.js":     {Data: []byte(`console.log("viewer")`)},
		"assets/app-abc123.css":    {Data: []byte(`:root{color:#000}`)},
		"favicon.svg":              {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"nested/deeper/thing.json": {Data: []byte(`{}`)},
	}
}

// serveSite stands the API up with a site mounted at the root.
func serveSite(t *testing.T, site fstest.MapFS) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var mounted http.Handler
	if site == nil {
		mounted = New(newFakeStore(), testSignupToken, logger, nil).Handler()
	} else {
		mounted = New(newFakeStore(), testSignupToken, logger, site).Handler()
	}
	srv := httptest.NewServer(mounted)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading GET %s: %v", path, err)
	}
	return res, string(body)
}

func TestStaticServesTheSite(t *testing.T) {
	srv := serveSite(t, testSite())

	cases := []struct {
		name     string
		path     string
		status   int
		contains string
		cache    string
		mime     string
	}{
		{
			name:     "the root is the page",
			path:     "/",
			status:   http.StatusOK,
			contains: `id="app"`,
			cache:    "no-cache",
			mime:     "text/html",
		},
		{
			name:     "the page asked for by name",
			path:     "/index.html",
			status:   http.StatusOK,
			contains: `id="app"`,
			cache:    "no-cache",
			mime:     "text/html",
		},
		{
			name:     "a hashed bundle may be held forever",
			path:     "/assets/app-abc123.js",
			status:   http.StatusOK,
			contains: "viewer",
			cache:    "immutable",
			mime:     "text/javascript",
		},
		{
			name:     "a hashed stylesheet too",
			path:     "/assets/app-abc123.css",
			status:   http.StatusOK,
			contains: "color",
			cache:    "immutable",
			mime:     "text/css",
		},
		{
			name:     "a hand-named root file is held briefly",
			path:     "/favicon.svg",
			status:   http.StatusOK,
			contains: "svg",
			cache:    "max-age=300",
			mime:     "image/svg+xml",
		},
		{
			name:     "a nested real file is still a file",
			path:     "/nested/deeper/thing.json",
			status:   http.StatusOK,
			contains: "{}",
			cache:    "max-age=300",
			mime:     "application/json",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, body := get(t, srv, c.path)
			if res.StatusCode != c.status {
				t.Errorf("status = %d, want %d", res.StatusCode, c.status)
			}
			if !strings.Contains(body, c.contains) {
				t.Errorf("body = %q, want it to contain %q", body, c.contains)
			}
			if cache := res.Header.Get("Cache-Control"); !strings.Contains(cache, c.cache) {
				t.Errorf("Cache-Control = %q, want it to contain %q", cache, c.cache)
			}
			if mime := res.Header.Get("Content-Type"); !strings.Contains(mime, c.mime) {
				t.Errorf("Content-Type = %q, want it to contain %q", mime, c.mime)
			}
		})
	}
}

// Every path the client router owns comes back as the page, at 200. A reload of
// a route the viewer was already on is the whole reason this exists.
func TestStaticFallsBackToThePage(t *testing.T) {
	srv := serveSite(t, testSite())

	for _, path := range []string{
		"/anything/else",
		"/workspace",
		"/nested",         // a real directory, which is not a file
		"/assets",         // and the assets directory itself
		"/assets/gone.js", // a hashed name that no longer exists
		"/a/b/c/d/e/f/g",
	} {
		t.Run(path, func(t *testing.T) {
			res, body := get(t, srv, path)
			if res.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", res.StatusCode)
			}
			if !strings.Contains(body, `id="app"`) {
				t.Errorf("body = %q, want the page", body)
			}
			if cache := res.Header.Get("Cache-Control"); cache != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", cache)
			}
		})
	}
}

// The fallback must never reach outside the site, whichever way the path is
// spelled. Nothing above the root is readable, so the worst case is the page.
func TestStaticDoesNotEscapeTheRoot(t *testing.T) {
	site := testSite()
	srv := serveSite(t, site)

	for _, path := range []string{
		"/../secrets",
		"/%2e%2e/secrets",
		"/assets/../../secrets",
		"//etc/passwd",
		// Encoded so that the mux does not clean it away before the handler
		// sees it -- this is the spelling that reaches us with a ".." intact.
		"/..%2fsecrets",
	} {
		t.Run(path, func(t *testing.T) {
			res, body := get(t, srv, path)
			// Either the page or a refusal -- never a file, and never a 200
			// carrying something that is not the page.
			if res.StatusCode == http.StatusOK && !strings.Contains(body, `id="app"`) {
				t.Errorf("status 200 with body %q, which is neither the page nor a refusal", body)
			}
			if strings.Contains(body, "secret") || strings.Contains(body, "root:") {
				t.Errorf("body = %q, which came from outside the site", body)
			}
		})
	}
}

// A write to the root is the API's kind of wrong answer, not the page.
func TestStaticRefusesOtherMethods(t *testing.T) {
	srv := serveSite(t, testSite())

	res, err := srv.Client().Post(srv.URL+"/", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", res.StatusCode)
	}
	if mime := res.Header.Get("Content-Type"); !strings.Contains(mime, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", mime)
	}
}

// HEAD is how a monitor asks, and it is not a write.
func TestStaticAnswersHead(t *testing.T) {
	srv := serveSite(t, testSite())

	res, err := srv.Client().Head(srv.URL + "/")
	if err != nil {
		t.Fatalf("HEAD /: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
}

// The reason /v1/ is registered separately. A client written against the JSON
// contract must never be handed HTML and a 200 because it misspelled a path.
func TestUnknownAPIPathsStayJSONWithASiteMounted(t *testing.T) {
	srv := serveSite(t, testSite())

	for _, path := range []string{"/v1/nope", "/v1/", "/v1/workspaces/extra"} {
		t.Run(path, func(t *testing.T) {
			res, body := get(t, srv, path)
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404", res.StatusCode)
			}
			if !strings.Contains(body, `"not_found"`) {
				t.Errorf("body = %q, want the JSON not_found shape", body)
			}
			if mime := res.Header.Get("Content-Type"); !strings.Contains(mime, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", mime)
			}
		})
	}
}

// The endpoints themselves are untouched by what is mounted at the root.
func TestHealthStillAnswersWithASiteMounted(t *testing.T) {
	srv := serveSite(t, testSite())

	res, body := get(t, srv, "/v1/health")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if !strings.Contains(body, `"ok"`) {
		t.Errorf("body = %q, want the health body", body)
	}
}

// No site: the root is what it was before this file existed.
func TestRootIsAnAPIErrorWithNoSite(t *testing.T) {
	srv := serveSite(t, nil)

	res, body := get(t, srv, "/")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if !strings.Contains(body, `"not_found"`) {
		t.Errorf("body = %q, want the JSON not_found shape", body)
	}
	if mime := res.Header.Get("Content-Type"); !strings.Contains(mime, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", mime)
	}
}

// A site with no index is the server's own fault, and has to say so as one --
// not as a 404 that reads like the client asked for the wrong thing.
func TestSiteWithoutAnIndexIsAnInternalError(t *testing.T) {
	srv := serveSite(t, fstest.MapFS{
		"assets/app-abc123.js": {Data: []byte(`console.log("viewer")`)},
	})

	res, body := get(t, srv, "/")
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", res.StatusCode)
	}
	if !strings.Contains(body, `"internal"`) {
		t.Errorf("body = %q, want the JSON internal shape", body)
	}
}
