package charted

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRefusesWhatIsNotASite(t *testing.T) {
	if _, err := New("", "token"); !errors.Is(err, ErrNoSite) {
		t.Errorf("New with no address = %v, want ErrNoSite", err)
	}
	// A hostname on its own is the mistake somebody actually makes, and it has
	// to be caught here rather than becoming a request to a relative path.
	for _, address := range []string{"docs.example.com", "ftp://docs.example.com", "://"} {
		if _, err := New(address, "token"); err == nil {
			t.Errorf("New(%q) was accepted", address)
		}
	}
}

func TestPublishNeedsAToken(t *testing.T) {
	client, err := New("https://docs.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if client.Writable() {
		t.Error("Writable with no token")
	}
	if _, err := client.Publish(t.Context(), Page{Space: "s", Slug: "p", Title: "P"}); !errors.Is(err, ErrNoToken) {
		t.Errorf("Publish = %v, want ErrNoToken", err)
	}
}

// The slug carries slashes and the space may carry anything a folder name can.
// Each segment is escaped on its own, so a nested page stays nested instead of
// arriving as one escaped string.
func TestPublishAddressesNestedPages(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.EscapedPath(), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"broken":[]}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Publish(t.Context(), Page{
		Space: "studio", Slug: "guides/first run", Title: "First run", Markdown: "hi",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if want := "/v1/pages/studio/guides/first%20run"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestPublishReturnsTheBrokenLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"broken":[{"space":"studio","slug":"roles","label":"roles"}]}`))
	}))
	defer server.Close()

	client, _ := New(server.URL, "s3cret")
	broken, err := client.Publish(t.Context(), Page{Space: "studio", Slug: "invites", Title: "Invites"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(broken) != 1 || broken[0].Slug != "roles" {
		t.Errorf("broken = %+v, want the one link", broken)
	}
}

// The site words its own refusals -- "not a writer", "this deployment has no
// write token" -- and repeating them beats inventing a worse sentence here.
func TestARefusalKeepsTheSitesWords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","message":"not a writer"}`))
	}))
	defer server.Close()

	client, _ := New(server.URL, "wrong")
	_, err := client.Publish(t.Context(), Page{Space: "s", Slug: "p", Title: "P"})

	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("err = %v, want a StatusError", err)
	}
	if status.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status.Status)
	}
	if status.Error() != "charted: not a writer" {
		t.Errorf("message = %q, want the site's own", status.Error())
	}
}

// A refusal with no JSON in it -- a proxy in the way, an HTML error page --
// still has to be an error somebody can read.
func TestARefusalWithoutJSONStillReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>gateway</html>"))
	}))
	defer server.Close()

	client, _ := New(server.URL, "s3cret")
	_, err := client.Publish(t.Context(), Page{Space: "s", Slug: "p", Title: "P"})
	if err == nil || !errors2Contains(err, "502") {
		t.Errorf("err = %v, want it to name the status", err)
	}
}

// Unpublishing something that is already gone is what the caller wanted.
func TestUnpublishToleratesAMissingPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found","message":"no such page"}`))
	}))
	defer server.Close()

	client, _ := New(server.URL, "s3cret")
	if err := client.Unpublish(t.Context(), "studio", "gone"); err != nil {
		t.Errorf("Unpublish = %v, want it to be fine", err)
	}
}

func TestPublishRefusesAnIncompletePage(t *testing.T) {
	client, _ := New("https://docs.example.com", "s3cret")
	for _, page := range []Page{
		{Slug: "p", Title: "P"},
		{Space: "s", Title: "P"},
		{Space: "s", Slug: "p"},
	} {
		if _, err := client.Publish(t.Context(), page); err == nil {
			t.Errorf("Publish(%+v) was accepted", page)
		}
	}
}

func errors2Contains(err error, want string) bool {
	return err != nil && len(want) > 0 && contains(err.Error(), want)
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
