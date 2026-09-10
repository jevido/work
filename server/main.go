// Command server is the sync server behind work.jevido.app.
//
// It stores an append-only log of ops per workspace and hands that log back in
// order. It holds no Anthropic credentials and never runs claude: agents run on
// the desktop, against the user's own signed-in CLI, and what reaches here is
// the result. A compromised server leaks a task board, not a Claude account.
//
// The API it serves is written down in server/README.md, which is the contract
// the desktop app and the web viewer are both built against.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dev.jevido/work/server/api"
	"dev.jevido/work/server/store"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.logLevel}))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	backing := store.New(pool)
	// Migrating at startup, before the listener opens, is what makes deploying
	// this one step: there is no window where the new binary is serving against
	// the old schema.
	if err := store.Migrate(ctx, backing, logger); err != nil {
		return err
	}
	if cfg.signupToken == "" {
		logger.Info("workspace creation is closed; set WORK_SIGNUP_TOKEN to open it")
	}

	srv := &http.Server{
		Addr:    cfg.addr,
		Handler: withRequestLog(logger, api.New(backing, cfg.signupToken, logger).Handler()),
		// Without these a single slow client holds a connection open for as
		// long as it likes. Every one of them is short because every request
		// here is small: the largest body allowed is a megabyte and the
		// slowest query is a bounded range scan.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	listening := make(chan error, 1)
	go func() { listening <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", cfg.addr)

	select {
	case err := <-listening:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		// A fresh context: the one above is already cancelled, and the point of
		// this one is to let in-flight requests finish rather than cut them.
		draining, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		return srv.Shutdown(draining)
	}
}

// withRequestLog records how each request went. It is a function, not a
// framework: the whole middleware need of this server is this one wrapper.
func withRequestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		// A failure is logged loudly enough to find; a success is at debug,
		// because a healthy server serving a viewer's poll every few seconds
		// should not fill a disk.
		level := slog.LevelDebug
		if recorder.status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if recorder.status >= http.StatusBadRequest {
			level = slog.LevelInfo
		}
		logger.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"took", time.Since(started))
	})
}

// statusRecorder remembers the status so it can be logged after the fact.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}
