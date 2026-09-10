package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"dev.jevido/work/server/store"
)

// apiError is a failure with a response already decided. Anything else a
// handler returns is the server's own fault and becomes a 500.
type apiError struct {
	status  int
	code    string
	message string
}

func (e apiError) Error() string { return e.code + ": " + e.message }

// errUnauthorized is deliberately one error for a missing key, a malformed key,
// an unknown key and a revoked one. Distinguishing them would tell someone
// working through a list of guesses which of them exist.
var errUnauthorized = apiError{http.StatusUnauthorized, "unauthorized", "unknown or expired key"}

// handler is what every endpoint is written as: it returns an error rather than
// writing one, so that deciding the status and shape of a failure happens in
// exactly one place.
type handler func(http.ResponseWriter, *http.Request) error

// authedHandler is a handler that has already been given a resolved key.
type authedHandler func(http.ResponseWriter, *http.Request, store.Auth) error

// plain runs a handler with no key and no rate limit. Only health uses it: a
// health check that can be rate-limited stops being a health check at exactly
// the moment it matters.
func (a *API) plain(next handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.fail(w, r, next(w, r))
	})
}

// limited runs a handler behind the rate limiter but with no key, for the one
// endpoint that has a caller before it has a key.
func (a *API) limited(next handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.allow(w, r); err != nil {
			a.fail(w, r, err)
			return
		}
		a.fail(w, r, next(w, r))
	})
}

// authed resolves the bearer key, checks it covers what the endpoint needs, and
// runs the handler.
func (a *API) authed(needed store.Access, next authedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.allow(w, r); err != nil {
			a.fail(w, r, err)
			return
		}

		key, ok := bearer(r)
		if !ok {
			a.fail(w, r, errUnauthorized)
			return
		}
		auth, err := a.store.Lookup(r.Context(), key)
		if err != nil {
			if errors.Is(err, store.ErrNoKey) {
				a.fail(w, r, errUnauthorized)
				return
			}
			a.fail(w, r, fmt.Errorf("resolving key: %w", err))
			return
		}
		// A key that is real but not enough is a 403, not a 401: retrying with
		// the same key will never work, and saying so is what stops a viewer
		// holding a read key from looping on a refresh it does not need.
		if !auth.Access.Allows(needed) {
			a.fail(w, r, apiError{http.StatusForbidden, "forbidden",
				fmt.Sprintf("this key may %s, and this endpoint needs %s", auth.Access, needed)})
			return
		}
		a.fail(w, r, next(w, r, auth))
	})
}

// allow charges the request against its caller's rate budget.
func (a *API) allow(w http.ResponseWriter, r *http.Request) error {
	// Keyed by the credential when there is one, so one workspace hammering the
	// server cannot exhaust anyone else's budget, and by address when there is
	// not, which is all an unauthenticated caller can be told apart by.
	who, ok := bearer(r)
	if !ok {
		who = callerAddress(r)
	}
	if retryAfter, ok := a.limiter.allow(who); !ok {
		w.Header().Set("Retry-After", retryAfter)
		return apiError{http.StatusTooManyRequests, "rate_limited", "too many requests"}
	}
	return nil
}

// fail turns whatever a handler returned into a response. A nil error means the
// handler has already written one.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}

	var known apiError
	if !errors.As(err, &known) {
		// An unexpected error is logged in full and reported as nothing at all,
		// because the detail in it is about the server's insides.
		a.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		known = apiError{http.StatusInternalServerError, "internal", "something went wrong"}
	}

	body, marshalErr := json.Marshal(struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{known.code, known.message}})
	if marshalErr != nil {
		// Nothing in apiError can fail to encode, so reaching here means the
		// response is already lost; say so plainly rather than write half of it.
		a.logger.Error("encoding an error response", "err", marshalErr)
		http.Error(w, `{"error":{"code":"internal","message":"something went wrong"}}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(known.status)
	if _, err := w.Write(body); err != nil {
		a.logger.Debug("writing an error response", "err", err)
	}
}

// bearer reads the token out of an Authorization header.
func bearer(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// sameSecret compares two secrets without leaking how far they matched.
//
// The key lookup does not need this — it hashes and probes an index — but the
// signup token is compared to a configured string directly, so it does.
func sameSecret(presented, configured string) bool {
	return subtle.ConstantTimeCompare([]byte(presented), []byte(configured)) == 1
}

// callerAddress is who to charge a request to when it carries no key.
func callerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
