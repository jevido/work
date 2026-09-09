package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"dev.jevido/work/internal/agents"
)

// pngBytes is a 1x1 PNG. The handler never decodes an image -- it only serves
// bytes and a content type off the extension -- so this only has to be a file.
var pngBytes = []byte("\x89PNG\r\n\x1a\n" + "fake but plausible")

// avatarServer builds the middleware over a config root, with a passthrough
// standing in for the embedded frontend.
func avatarServer(t *testing.T, root string) (http.Handler, *bool) {
	t.Helper()
	passed := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		passed = true
		w.WriteHeader(http.StatusTeapot)
	})
	return avatarMiddleware(func() string { return root })(next), &passed
}

func avatarRootWith(t *testing.T, id, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(agents.AgentsDir(root), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		if err := os.WriteFile(filepath.Join(dir, name), pngBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func get(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

func TestAvatarServesTheFile(t *testing.T) {
	cases := []struct {
		name string
		typ  string
	}{
		{"avatar.png", "image/png"},
		{"avatar.webp", "image/webp"},
	}
	for _, c := range cases {
		root := avatarRootWith(t, "anton", c.name)
		h, _ := avatarServer(t, root)

		res := get(t, h, AvatarRoute+"anton")
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()

		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", c.name, res.StatusCode)
		}
		if got := res.Header.Get("Content-Type"); got != c.typ {
			t.Errorf("%s: Content-Type = %q, want %q", c.name, got, c.typ)
		}
		if string(body) != string(pngBytes) {
			t.Errorf("%s: body = %q, want the file's bytes", c.name, body)
		}
		// The file is the user's and is edited outside Work, so the webview
		// has to ask again rather than trust what it has.
		if got := res.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want no-cache", c.name, got)
		}
	}
}

// The common case, and not an error: the office falls back to its colour blob.
func TestAvatarNotFoundCases(t *testing.T) {
	withAvatar := avatarRootWith(t, "anton", "avatar.png")

	cases := []struct {
		name string
		root string
		path string
	}{
		{"agent has no avatar", avatarRootWith(t, "anton", ""), AvatarRoute + "anton"},
		{"no such agent", withAvatar, AvatarRoute + "nobody"},
		{"no agent named", withAvatar, AvatarRoute},
		{"trailing slash", withAvatar, AvatarRoute + "anton/"},
		{"a file in the folder that is not an avatar", withAvatar, AvatarRoute + "anton/PERSONALITY.md"},
		{"traversal out of the agents folder", withAvatar, AvatarRoute + "../../etc/passwd"},
		{"escaped traversal", withAvatar, AvatarRoute + "..%2f..%2fetc%2fpasswd"},
		{"hidden folder", withAvatar, AvatarRoute + ".git"},
		{"no config folder chosen", "", AvatarRoute + "anton"},
	}
	for _, c := range cases {
		h, passed := avatarServer(t, c.root)
		res := get(t, h, c.path)
		res.Body.Close()

		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", c.name, res.StatusCode)
		}
		if *passed {
			t.Errorf("%s: fell through to the frontend handler", c.name)
		}
	}
}

// Anything that is not an avatar request is somebody else's: the frontend is
// still served by the handler this wraps.
func TestAvatarPassesEverythingElseThrough(t *testing.T) {
	h, passed := avatarServer(t, avatarRootWith(t, "anton", "avatar.png"))

	for _, path := range []string{"/", "/index.html", "/avatars", "/avatarsx/anton"} {
		*passed = false
		res := get(t, h, path)
		res.Body.Close()
		if !*passed {
			t.Errorf("%s: was handled as an avatar request (status %d)", path, res.StatusCode)
		}
	}
}

func TestAvatarRejectsWrites(t *testing.T) {
	h, _ := avatarServer(t, avatarRootWith(t, "anton", "avatar.png"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, AvatarRoute+"anton", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}

// A picture replaced in place keeps its name and its URL, so the modification
// time is what tells the webview it has to fetch the new one.
func TestAvatarRevalidatesOnModTime(t *testing.T) {
	h, _ := avatarServer(t, avatarRootWith(t, "anton", "avatar.png"))

	res := get(t, h, AvatarRoute+"anton")
	res.Body.Close()
	modified := res.Header.Get("Last-Modified")
	if modified == "" {
		t.Fatal("no Last-Modified: an unchanged avatar cannot be revalidated")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, AvatarRoute+"anton", nil)
	req.Header.Set("If-Modified-Since", modified)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidated status = %d, want 304", rec.Code)
	}
}
