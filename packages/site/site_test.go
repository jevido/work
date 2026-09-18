package site

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func built() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>page</title>")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
		"favicon.png":          {Data: []byte("\x89PNG")},
		"nested/deep/file.txt": {Data: []byte("deep")},
	}
}

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestServesTheFilesThatExist(t *testing.T) {
	h := Handler(built(), Options{})

	for _, path := range []string{"/assets/index-abc.js", "/favicon.png", "/nested/deep/file.txt"} {
		rec := get(t, h, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

// Every path the client router owns is the page, at 200. A 404 here would mean
// a reload of a route somebody was already on fails.
func TestUnknownPathsAreThePage(t *testing.T) {
	h := Handler(built(), Options{})

	for _, path := range []string{"/", "/studio/install", "/studio/install/deep", "/index.html"} {
		rec := get(t, h, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		if body := rec.Body.String(); body != string(built()["index.html"].Data) {
			t.Errorf("GET %s served %q, want the index", path, body)
		}
	}
}

// A path that does not parse is a mistyped link far more often than an attack,
// and it lands on the page rather than on a plain-text 400.
func TestTraversalIsThePageNotAnEscape(t *testing.T) {
	h := Handler(built(), Options{})

	rec := get(t, h, http.MethodGet, "/../../etc/passwd")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if rec.Body.String() != string(built()["index.html"].Data) {
		t.Errorf("body = %q, want the index", rec.Body.String())
	}
}

// The cache split is the whole reason this is not http.FileServer: hashed
// assets are immutable, the page may never be held, and hand-named files sit
// between the two.
func TestCacheHeaders(t *testing.T) {
	h := Handler(built(), Options{})

	cases := map[string]string{
		"/assets/index-abc.js": "public, max-age=31536000, immutable",
		"/favicon.png":         "public, max-age=300",
		"/":                    "no-cache",
		"/some/route":          "no-cache",
	}
	for path, want := range cases {
		if got := get(t, h, http.MethodGet, path).Header().Get("Cache-Control"); got != want {
			t.Errorf("Cache-Control for %s = %q, want %q", path, got, want)
		}
	}
}

func TestHeadIsServedAndOtherMethodsAreRefused(t *testing.T) {
	asked := false
	h := Handler(built(), Options{
		OnMethod: func(w http.ResponseWriter, _ *http.Request) {
			asked = true
			w.WriteHeader(http.StatusMethodNotAllowed)
		},
	})

	// A monitor asking HEAD / must not see a 405.
	if rec := get(t, h, http.MethodHead, "/"); rec.Code != http.StatusOK {
		t.Errorf("HEAD / = %d, want 200", rec.Code)
	}
	if asked {
		t.Fatal("HEAD went to OnMethod")
	}

	if rec := get(t, h, http.MethodPost, "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", rec.Code)
	}
	if !asked {
		t.Error("POST did not reach OnMethod, so the caller could not word its own refusal")
	}
}

// A site directory with no index is the server's own fault, and the caller has
// to be able to answer it in its own shape -- which it cannot once a header for
// a page that will never be sent is already on the response.
func TestABrokenSiteReachesTheCaller(t *testing.T) {
	var got error
	h := Handler(fstest.MapFS{"assets/x.js": {Data: []byte("x")}}, Options{
		OnBroken: func(w http.ResponseWriter, _ *http.Request, err error) {
			got = err
			w.WriteHeader(http.StatusInternalServerError)
		},
	})

	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
	if got == nil || !errors.Is(got, fs.ErrNotExist) {
		t.Errorf("OnBroken got %v, want a missing-file error", got)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "" {
		t.Errorf("Cache-Control = %q on a response that is not the page", cache)
	}
}

// The defaults exist so that a caller who does not care still gets something
// sane rather than a nil dereference.
func TestDefaultsAnswerWithoutHooks(t *testing.T) {
	h := Handler(fstest.MapFS{}, Options{})

	if rec := get(t, h, http.MethodGet, "/"); rec.Code != http.StatusInternalServerError {
		t.Errorf("broken site with no hook = %d, want 500", rec.Code)
	}
	if rec := get(t, h, http.MethodDelete, "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE with no hook = %d, want 405", rec.Code)
	}
}
