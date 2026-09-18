package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubGitHub serves one /releases/latest reply and counts the requests it was
// asked to serve, so a test can prove the conditional request works.
type stubGitHub struct {
	mu       sync.Mutex
	release  Release
	etag     string
	requests int
	// conditional records the If-None-Match of the last request.
	conditional string
}

// served is the request count, read under the lock the handler writes it with.
func (s *stubGitHub) served() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *stubGitHub) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests++
		s.conditional = r.Header.Get("If-None-Match")
		rel, etag, want := s.release, s.etag, s.conditional
		s.mu.Unlock()

		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if etag != "" {
			w.Header().Set("ETag", etag)
			if want == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			t.Errorf("encode release: %v", err)
		}
	})
}

// newTestChecker points a checker at a stub instead of api.github.com. The
// repo is spliced into the URL, so a repo of the form "<host>/x/y" is enough
// to redirect the whole call without a transport in the way.
func newTestChecker(t *testing.T, server *httptest.Server, version string, emit Emitter) *Checker {
	t.Helper()
	c := New("owner/name", version, emit)
	c.http = server.Client()
	c.dl = server.Client()
	c.baseURL = server.URL
	return c
}

func TestCheckEmitsWhenNewer(t *testing.T) {
	stub := &stubGitHub{release: Release{
		TagName: "v0.2.0",
		HTMLURL: "https://github.com/owner/name/releases/tag/v0.2.0",
	}}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()

	var got []AvailableEvent
	c := newTestChecker(t, server, "v0.1.0", func(name string, data any) {
		if name != EventUpdateAvailable {
			t.Errorf("event name = %q, want %q", name, EventUpdateAvailable)
		}
		ev, ok := data.(AvailableEvent)
		if !ok {
			t.Fatalf("payload is %T, want AvailableEvent", data)
		}
		got = append(got, ev)
	})

	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1", len(got))
	}
	if got[0].Version != "v0.2.0" || got[0].ReleaseURL != stub.release.HTMLURL {
		t.Errorf("payload = %+v", got[0])
	}

	// Checking again must not re-announce the same release: the frontend
	// treats the event as "open the dialog", and a six-hourly poll should not
	// reopen it forever.
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d events after a repeat check, want 1", len(got))
	}
}

func TestCheckSilentWhenNotNewer(t *testing.T) {
	for _, running := range []string{"v0.2.0", "v0.3.0"} {
		stub := &stubGitHub{release: Release{TagName: "v0.2.0"}}
		server := httptest.NewServer(stub.handler(t))

		emitted := 0
		c := newTestChecker(t, server, running, func(string, any) { emitted++ })
		if err := c.Check(context.Background()); err != nil {
			t.Fatalf("Check at %s: %v", running, err)
		}
		if emitted != 0 {
			t.Errorf("running %s: emitted %d events, want 0", running, emitted)
		}
		server.Close()
	}
}

func TestCheckIgnoresPrerelease(t *testing.T) {
	stub := &stubGitHub{release: Release{TagName: "v0.9.0", Prerelease: true}}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()

	emitted := 0
	c := newTestChecker(t, server, "v0.1.0", func(string, any) { emitted++ })
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if emitted != 0 {
		t.Errorf("emitted %d events for a prerelease, want 0", emitted)
	}
}

func TestCheckUsesETag(t *testing.T) {
	stub := &stubGitHub{
		release: Release{TagName: "v0.2.0"},
		etag:    `W/"abc123"`,
	}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()

	c := newTestChecker(t, server, "v0.1.0", func(string, any) {})
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("first Check: %v", err)
	}
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("second Check: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.requests != 2 {
		t.Fatalf("served %d requests, want 2", stub.requests)
	}
	if stub.conditional != stub.etag {
		t.Errorf("If-None-Match = %q, want %q", stub.conditional, stub.etag)
	}
}

func TestStartSkipsDevBuild(t *testing.T) {
	stub := &stubGitHub{release: Release{TagName: "v9.0.0"}}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()

	c := newTestChecker(t, server, DevVersion, func(string, any) {
		t.Error("a development build must not be told to update")
	})
	c.delay = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	// Long enough that a poll would have happened if one had been scheduled.
	time.Sleep(50 * time.Millisecond)

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.requests != 0 {
		t.Errorf("served %d requests for a development build, want 0", stub.requests)
	}
}

func TestApplyUpdateRefusesDevBuild(t *testing.T) {
	c := New("owner/name", DevVersion, nil)
	err := c.ApplyUpdate(context.Background())
	if err == nil {
		t.Fatal("ApplyUpdate on a development build should fail")
	}
	if !strings.Contains(err.Error(), "development build") {
		t.Errorf("error = %v, want it to name the development build", err)
	}
}

// The button's check answers whatever it finds, which is the whole difference
// between it and the poll: silence is a fine answer to a background question
// and no answer at all to somebody who pressed something.
func TestCheckNowAnswersBothWays(t *testing.T) {
	stub := &stubGitHub{release: Release{
		TagName: "v0.2.0",
		HTMLURL: "https://github.com/owner/name/releases/tag/v0.2.0",
	}}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()

	t.Run("newer says so and puts the popup up", func(t *testing.T) {
		var events int
		c := newTestChecker(t, server, "v0.1.0", func(string, any) { events++ })
		found, err := c.CheckNow(context.Background())
		if err != nil {
			t.Fatalf("CheckNow: %v", err)
		}
		if !found.Newer || found.Version != "v0.2.0" || found.Current != "v0.1.0" {
			t.Errorf("found = %+v, want v0.2.0 over v0.1.0", found)
		}
		if found.ReleaseURL == "" {
			t.Error("no release url, so there is nothing to read before installing")
		}
		if events != 1 {
			t.Errorf("emitted %d times, want 1", events)
		}
		// The poll must not then say the same thing again behind it.
		if err := c.Check(context.Background()); err != nil {
			t.Fatalf("Check: %v", err)
		}
		if events != 1 {
			t.Errorf("emitted %d times after the poll, want 1", events)
		}
	})

	t.Run("up to date is an answer, not silence", func(t *testing.T) {
		var events int
		c := newTestChecker(t, server, "v0.2.0", func(string, any) { events++ })
		found, err := c.CheckNow(context.Background())
		if err != nil {
			t.Fatalf("CheckNow: %v", err)
		}
		if found.Newer {
			t.Errorf("found = %+v, want nothing newer", found)
		}
		if found.Version != "v0.2.0" {
			t.Errorf("version = %q, want the release it saw", found.Version)
		}
		if events != 0 {
			t.Error("a popup for a version already running")
		}
	})

	t.Run("a development build asks nobody", func(t *testing.T) {
		before := stub.served()
		c := newTestChecker(t, server, DevVersion, func(string, any) {
			t.Error("a popup on a build that is probably ahead of the release")
		})
		found, err := c.CheckNow(context.Background())
		if err != nil {
			t.Fatalf("CheckNow: %v", err)
		}
		if !found.Dev || found.Newer {
			t.Errorf("found = %+v, want a dev build with nothing on offer", found)
		}
		if stub.served() != before {
			t.Error("GitHub was asked about a development build")
		}
	})
}
