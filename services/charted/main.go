// Charted: the documentation site.
//
// One binary. It migrates its schema, serves the reader's static files, and
// answers the API those files call. Pages arrive over HTTP from documentation
// mode rather than from a directory on disk, which is what makes it publishable
// from a machine that is not this one.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dev.jevido/work/services/charted/api"
	"dev.jevido/work/services/charted/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("charted stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set, and there is nothing to serve without it")
	}

	// Startup is where a missing database is worth waiting for: a container
	// scheduled beside its Postgres routinely starts first, and dying
	// immediately turns a five-second race into a restart loop.
	db, err := open(ctx, dsn, 30*time.Second, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := store.Migrate(ctx, db, logger); err != nil {
		return err
	}

	site, err := siteFS(os.Getenv("CHARTED_SITE_DIR"), logger)
	if err != nil {
		return err
	}

	token := os.Getenv("CHARTED_TOKEN")
	if token == "" {
		// Said out loud, once. A read-only Charted is a legitimate deployment
		// -- a mirror, a staging copy -- but it is never what somebody meant
		// when they set one up to publish to.
		logger.Warn("no CHARTED_TOKEN, so nothing can be published to this instance")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.New(db, token, site, logger),
		ReadHeaderTimeout: 10 * time.Second,
		// Generous, because a write carries a whole page of markdown over a
		// connection that may be somebody's home upload.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("charted is serving", "port", port, "writable", token != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// open retries until the database answers or the budget runs out.
func open(ctx context.Context, dsn string, within time.Duration, logger *slog.Logger) (*store.Store, error) {
	deadline := time.Now().Add(within)
	for attempt := 1; ; attempt++ {
		db, err := store.Open(ctx, dsn)
		if err == nil {
			return db, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("the database never became reachable: %w", err)
		}
		logger.Info("waiting for the database", "attempt", attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// siteFS opens the directory holding the built reader, or nil when this
// deployment is the API alone.
//
// Checked here rather than on the first request: a CHARTED_SITE_DIR that points
// at nothing is a deployment mistake, and it should stop the process rather
// than become a 500 the first time somebody opens the site. The serving itself
// is packages/site, shared with the sync server.
func siteFS(dir string, logger *slog.Logger) (fs.FS, error) {
	if dir == "" {
		logger.Info("no CHARTED_SITE_DIR, so the API is served without a reader")
		return nil, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("CHARTED_SITE_DIR=%s: %w", dir, err)
	}
	dirFS := os.DirFS(root)
	if _, err := fs.Stat(dirFS, "index.html"); err != nil {
		return nil, fmt.Errorf("CHARTED_SITE_DIR=%s has no readable index.html: %w", root, err)
	}
	return dirFS, nil
}
