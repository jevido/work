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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

	// pgxpool.New does not connect; it parses. The first real connection is the
	// migration below, and on a machine where this container and Postgres come
	// up together that connection loses the race often enough to matter.
	if err := waitForDatabase(ctx, pool, logger, databaseWait); err != nil {
		return err
	}

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

// How long the server will wait for a database that is coming up, and how it
// spaces its attempts.
//
// The bound is a minute. It is long enough to cover Postgres starting from a
// clean shutdown and replaying a small WAL, and short enough that a server
// pointed at a database that will never answer gives up inside a deploy's
// patience rather than sitting in a loop looking healthy. Past it the process
// exits and whatever is supervising it restarts it, which is a retry with a
// clean slate rather than a longer version of this one.
const (
	databaseWait     = time.Minute
	databaseBackoff  = 250 * time.Millisecond
	databaseMaxDelay = 5 * time.Second
	databaseAttempt  = 5 * time.Second
)

// waitForDatabase blocks until the database answers, the budget runs out, or it
// hits something waiting will not fix.
//
// The distinction is the point. A database that is starting up, or a port not
// yet accepting connections, is a race this server loses on a cold boot and
// wins a second later. A wrong password, a missing database, a host that does
// not resolve to anything — those are configuration, and retrying them for a
// minute turns a message that says what is wrong into a timeout that does not.
// The budget is a parameter rather than the constant read directly so that a
// test can prove the giving-up path without waiting a minute for it.
func waitForDatabase(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, budget time.Duration) error {
	started := time.Now()
	deadline := started.Add(budget)
	delay := databaseBackoff

	for attempt := 1; ; attempt++ {
		// Bounded per attempt as well as overall: a TCP connect to a host that
		// is up but not listening can hang, and one hung attempt must not eat
		// the whole budget.
		trying, cancel := context.WithTimeout(ctx, databaseAttempt)
		err := pool.Ping(trying)
		cancel()

		if err == nil {
			if attempt > 1 {
				logger.Info("database is up", "attempts", attempt,
					"waited", time.Since(started).Round(time.Millisecond))
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		reason, worthWaiting := notReadyYet(err)
		if !worthWaiting {
			// Not "could not reach the database" — this one answered, or
			// refused, in a way no amount of waiting changes.
			return fmt.Errorf("the database rejected this server's first connection: %w", err)
		}

		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("the database was still %s after %s and %d attempts: %w",
				reason, budget, attempt, err)
		}

		logger.Warn("waiting for the database", "reason", reason, "attempt", attempt,
			"retrying_in", delay, "giving_up_in", left.Round(time.Second), "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(delay, left)):
		}
		delay = min(delay*2, databaseMaxDelay)
	}
}

// notReadyYet says whether a failed connection is one that waiting fixes, and
// what to call it in the log.
//
// The reason string is the thing an operator reads at three in the morning.
// "starting up" means Postgres is there and is not finished; "not accepting
// connections yet" means nothing is listening, which on a cold boot is the same
// story a moment earlier and on a misconfigured host is the wrong address. The
// underlying error is logged beside it either way, so the two are told apart by
// reading rather than by guessing.
func notReadyYet(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57P03":
			// cannot_connect_now: the database system is starting up. This is
			// the exact SQLSTATE seen when the server beats Postgres up.
			return "starting up", true
		case "53300":
			// too_many_connections: someone else's pool has not drained yet,
			// which on a redeploy is the instance being replaced.
			return "out of connection slots", true
		}
		// Any other SQLSTATE is the database answering a question. It has an
		// opinion about this connection and will have the same one in a minute.
		return "", false
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// A compose network brings names up with the containers behind them, so
		// a name that does not resolve during a cold start usually will. A name
		// that is simply wrong burns the budget and then says so.
		return "not resolvable yet", true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "not accepting connections yet", true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "not answering", true
	}
	return "", false
}
