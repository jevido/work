package update

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

// Event names emitted to the frontend. Keep this in sync with
// frontend/src/lib/bridge/events.ts, as the workbench events are.
const (
	// EventUpdateAvailable says a newer release exists. It is emitted at most
	// once per version per run, so the frontend can treat it as "show the
	// dialog" rather than having to remember whether it already has.
	EventUpdateAvailable = "update:available"
)

// AvailableEvent is the payload for update:available.
type AvailableEvent struct {
	// Version is the newer release's tag, as tagged: "v0.2.0".
	Version string `json:"version"`
	// ReleaseURL is the release page on GitHub, so the frontend can offer
	// "see what changed" next to "install it".
	ReleaseURL string `json:"releaseUrl"`
}

// Emitter is how this package reaches the frontend. It is the same signature
// the workbench takes, and for the same reason: the application does not exist
// yet when the checker is constructed, so main hands over a function that
// looks it up when there is something to send.
type Emitter func(name string, data any)

const (
	// pollInterval is how often GitHub is asked. A desktop app that has just
	// been started is the interesting case and the first check covers it; past
	// that, a release lands every few days at best, so polling faster only
	// spends someone's rate limit to learn nothing.
	pollInterval = 6 * time.Hour
	// startupDelay keeps the first check out of the way of starting up. The
	// window, the frontend bundle and the agent folders all want the machine
	// in the first second, and an update that is discovered a few seconds
	// later is discovered just as usefully.
	startupDelay = 5 * time.Second
	// requestTimeout bounds one API call. This is a background poll, so a
	// hanging connection should be abandoned rather than waited on.
	requestTimeout = 20 * time.Second
)

// Checker polls for newer releases and, when asked, installs one.
//
// One type does both because both need the same two things -- the repository
// and the release that was last seen -- and because the frontend's Install
// click should not have to re-ask GitHub what the poller already knows.
type Checker struct {
	repo    string
	version string
	emit    Emitter
	// http is the poller's client, with a short timeout because a background
	// check that hangs should be abandoned. dl is the download client, which
	// has no timeout at all: a release binary over a slow connection takes as
	// long as it takes, and the request context bounds it instead.
	http *http.Client
	dl   *http.Client

	// interval and delay are fields rather than constants so the tests can
	// drive the loop without sleeping for hours.
	interval time.Duration
	delay    time.Duration

	// The rest are seams for the tests, and are the real thing everywhere
	// else. Updating replaces the binary the process is running and then
	// replaces the process, so the tests need somewhere else to point both:
	// baseURL at a stub instead of api.github.com, exePath at a copy in a
	// temporary directory, and restartProcess at a function that records the
	// call rather than execing the test binary's replacement.
	baseURL        string
	exePath        func() (string, error)
	restartProcess func(exe string) error
	restartAfter   time.Duration

	// mu guards everything below. The poll goroutine writes it and
	// ApplyUpdate, on the frontend's goroutine, reads it.
	mu      sync.Mutex
	etag    string
	latest  *Release
	emitted string
}

// New builds a checker for the running version. An empty repo means
// DefaultRepo, and an empty version means a development build.
func New(repo, version string, emit Emitter) *Checker {
	if repo == "" {
		repo = DefaultRepo
	}
	if version == "" {
		version = DevVersion
	}
	return &Checker{
		repo:    repo,
		version: version,
		emit:    emit,
		// Clients are kept rather than made per request, so a check reuses
		// the connection and the TLS session instead of paying for a
		// handshake every six hours.
		http:           &http.Client{Timeout: requestTimeout},
		dl:             &http.Client{},
		interval:       pollInterval,
		delay:          startupDelay,
		baseURL:        DefaultAPIBaseURL,
		exePath:        runningBinary,
		restartProcess: restart,
		restartAfter:   restartDelay,
	}
}

// Version is the release this binary was built as.
func (c *Checker) Version() string { return c.version }

// Start begins polling in the background and returns immediately. It stops
// when ctx is cancelled, which is the service's shutdown.
//
// A development build is not polled at all: a locally built binary is usually
// ahead of the newest release, and offering to "update" it to something older
// is worse than saying nothing.
func (c *Checker) Start(ctx context.Context) {
	if IsDevBuild(c.version) {
		log.Printf("update: development build, not checking for updates")
		return
	}

	go func() {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}

			if err := c.Check(ctx); err != nil && !errors.Is(err, context.Canceled) {
				// A failed check is a log line and nothing else. The network
				// being unavailable is the normal state of a laptop, and it is
				// not something to interrupt the user about.
				log.Printf("update: %v", err)
			}
			timer.Reset(c.interval)
		}
	}()
}

// Check asks GitHub once and emits update:available if the answer is newer
// than the running version. It is exported for the tests and for a future
// "check now" control; the poll loop is the only caller today.
func (c *Checker) Check(ctx context.Context) error {
	rel, err := c.latestRelease(ctx)
	if err != nil {
		return err
	}
	// Draft releases are invisible to an unauthenticated client and
	// prereleases are not what /releases/latest returns, so both of these are
	// belt and braces against a hand-made release.
	if rel.Draft || rel.Prerelease {
		return nil
	}
	if !IsNewer(rel.TagName, c.version) {
		return nil
	}

	c.mu.Lock()
	alreadySaid := c.emitted == rel.TagName
	c.emitted = rel.TagName
	c.mu.Unlock()
	if alreadySaid || c.emit == nil {
		return nil
	}

	c.emit(EventUpdateAvailable, AvailableEvent{
		Version:    rel.TagName,
		ReleaseURL: rel.HTMLURL,
	})
	return nil
}

// latestRelease returns the newest release, from cache when GitHub says the
// cache is still good.
func (c *Checker) latestRelease(ctx context.Context) (*Release, error) {
	c.mu.Lock()
	etag, cached := c.etag, c.latest
	c.mu.Unlock()

	rel, newETag, err := c.fetchLatest(ctx, etag)
	switch {
	case errors.Is(err, errNotModified) && cached != nil:
		return cached, nil
	case errors.Is(err, errNotModified):
		// An ETag with nothing behind it should not happen, but answering
		// "unchanged from nothing" would strand the caller. Drop the ETag so
		// the next call is unconditional.
		c.mu.Lock()
		c.etag = ""
		c.mu.Unlock()
		return nil, errors.New("update: cached release is missing")
	case err != nil:
		return nil, err
	}

	c.mu.Lock()
	c.etag, c.latest = newETag, rel
	c.mu.Unlock()
	return rel, nil
}
