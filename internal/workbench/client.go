package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dev.jevido/work/internal/ops"
)

// The server's contract, from server/README.md. These are fixed: the server
// says so, and the web viewer is written against the same shapes.
const (
	routeWorkspaces = "/v1/workspaces"
	routeWorkspace  = "/v1/workspace"
	routeOps        = "/v1/ops"

	// maxPushOps and maxPullOps are the server's documented limits, not
	// guesses. Pushing more is a 400; asking for more is silently capped, and
	// a client that does not know the cap pages wrong.
	maxPushOps = 500
	maxPullOps = 1000
)

// Workspace is a workspace's metadata as the server reports it.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Head is the highest sequence in the log. Sequences are gapless, so a
	// client holding up to N knows Head-N is exactly how far behind it is.
	Head      int64  `json:"head"`
	CreatedAt string `json:"createdAt"`
}

// Created is the one and only time a workspace's keys are readable.
type Created struct {
	Workspace Workspace `json:"workspace"`
	WriteKey  string    `json:"writeKey"`
	ReadKey   string    `json:"readKey"`
}

// Meta is what a key can learn about the workspace it belongs to.
type Meta struct {
	Workspace Workspace `json:"workspace"`
	// Access is "read" or "write".
	Access string `json:"access"`
}

// Ack pairs an op ID with the sequence the server gave it.
type Ack struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}

// PushResult is what POST /v1/ops returns.
//
// Duplicates are not a failure. They are how a client that lost the response
// to an earlier attempt finds out that the attempt landed after all, which is
// the whole reason op IDs are the idempotency key.
type PushResult struct {
	Head       int64 `json:"head"`
	Accepted   []Ack `json:"accepted"`
	Duplicates []Ack `json:"duplicates"`
}

// Logged is one op as it sits in the server's log.
type Logged struct {
	Seq        int64  `json:"seq"`
	ReceivedAt string `json:"receivedAt"`
	Op         ops.Op `json:"op"`
}

// PullResult is what GET /v1/ops returns.
type PullResult struct {
	Head int64    `json:"head"`
	Ops  []Logged `json:"ops"`
	// More is true when there are ops after the last one returned. Page until
	// it is false rather than until a short page arrives -- the server is
	// allowed to return fewer than the limit.
	More bool `json:"more"`
}

// OpsClient is the transport the sync loop needs.
//
// It exists as an interface so the sync loop -- which owns everything
// interesting: what becomes an op, what survives a crash, what happens while
// offline -- is testable against a fake with no HTTP in sight. HTTPClient is
// the only implementation that ships.
type OpsClient interface {
	// Create makes a workspace. The bearer token is the server's signup
	// token, not a workspace key: there is no workspace to authenticate
	// against yet.
	Create(ctx context.Context, serverURL, signupToken, name string) (Created, error)
	// Meta reads the workspace a key belongs to. It is also how a key is
	// checked: a bad key fails here, at the point it was typed.
	Meta(ctx context.Context, serverURL, key string) (Meta, error)
	// Push appends ops. At most maxPushOps per call.
	Push(ctx context.Context, serverURL, writeKey string, batch []ops.Op) (PushResult, error)
	// Pull returns ops with a sequence strictly greater than since.
	Pull(ctx context.Context, serverURL, key string, since int64, limit int) (PullResult, error)
}

// APIError is a non-2xx response from the server.
//
// Code is what to branch on; Message is for a human and may change between
// releases. That is the server's rule and it is worth keeping on this side of
// the wire too, so nothing here parses Message.
type APIError struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is the server's Retry-After on a 429, or zero.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("server: %s (%d %s)", e.Message, e.Status, e.Code)
	}
	return fmt.Sprintf("server: %d %s", e.Status, e.Code)
}

// Fatal reports whether retrying the same request could ever succeed.
//
// A rejected key or a malformed op will be rejected again forever, and a sync
// loop that retries one every few seconds is a machine talking to itself. The
// queue keeps the ops either way -- this only decides whether to keep asking.
func (e *APIError) Fatal() bool {
	switch e.Status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge:
		return true
	}
	return false
}

// HTTPClient talks to a Work sync server.
//
// The zero value is usable and gets a client with sane timeouts. Reuse one:
// it holds the connection pool, and a new client per request would pay a TLS
// handshake every poll.
type HTTPClient struct {
	// HTTP is the underlying client. Nil means the package default.
	HTTP *http.Client
}

// defaultHTTP is shared by every zero-value HTTPClient so that polling reuses
// connections. http.DefaultClient is deliberately not used: it has no timeout
// at all, so one hung server would wedge the sync loop until the process died.
var defaultHTTP = &http.Client{
	Timeout: syncTimeout,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		// The poll is every few seconds and the body is small, so the cost
		// that matters is the handshake, not the transfer.
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
}

func (c *HTTPClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

// Create makes a workspace.
func (c *HTTPClient) Create(ctx context.Context, serverURL, signupToken, name string) (Created, error) {
	body := struct {
		Name string `json:"name"`
	}{Name: name}

	var out Created
	err := c.do(ctx, http.MethodPost, serverURL+routeWorkspaces, signupToken, body, &out)
	return out, err
}

// Meta reads the workspace a key belongs to.
func (c *HTTPClient) Meta(ctx context.Context, serverURL, key string) (Meta, error) {
	var out Meta
	err := c.do(ctx, http.MethodGet, serverURL+routeWorkspace, key, nil, &out)
	return out, err
}

// Push appends ops to the log.
func (c *HTTPClient) Push(ctx context.Context, serverURL, writeKey string, batch []ops.Op) (PushResult, error) {
	if len(batch) > maxPushOps {
		return PushResult{}, fmt.Errorf("workbench: %d ops exceeds the server's limit of %d", len(batch), maxPushOps)
	}
	body := struct {
		Ops []ops.Op `json:"ops"`
	}{Ops: batch}

	var out PushResult
	err := c.do(ctx, http.MethodPost, serverURL+routeOps, writeKey, body, &out)
	return out, err
}

// Pull reads the log from since.
func (c *HTTPClient) Pull(ctx context.Context, serverURL, key string, since int64, limit int) (PullResult, error) {
	if limit <= 0 || limit > maxPullOps {
		limit = maxPullOps
	}
	q := url.Values{}
	q.Set("since", strconv.FormatInt(since, 10))
	q.Set("limit", strconv.Itoa(limit))

	var out PullResult
	err := c.do(ctx, http.MethodGet, serverURL+routeOps+"?"+q.Encode(), key, nil, &out)
	return out, err
}

// maxResponseBytes bounds what a server can make this process allocate. The
// largest legitimate response is a full page of ops; 8 MiB is generous for
// that and still refuses to let a confused endpoint eat the heap.
const maxResponseBytes = 8 << 20

// do performs one request and decodes the response.
func (c *HTTPClient) do(ctx context.Context, method, endpoint, bearer string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("workbench: encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("workbench: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("workbench: %s %s: %w", method, redact(endpoint), err)
	}
	defer func() {
		// Drain before closing so the connection goes back to the pool
		// instead of being torn down and re-handshaken on the next poll.
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
	}()

	reader := io.LimitReader(resp.Body, maxResponseBytes)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiError(resp, reader)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(reader).Decode(out); err != nil {
		return fmt.Errorf("workbench: decode %s response: %w", redact(endpoint), err)
	}
	return nil
}

// apiError builds an APIError from a failed response.
//
// A body that is not the documented error envelope still produces an APIError
// with the status, because the status is the part a caller can act on and a
// proxy returning HTML is a real thing that happens.
func apiError(resp *http.Response, body io.Reader) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(body).Decode(&envelope)

	e := &APIError{
		Status:  resp.StatusCode,
		Code:    envelope.Error.Code,
		Message: envelope.Error.Message,
	}
	if e.Code == "" {
		e.Code = strings.ToLower(strings.ReplaceAll(http.StatusText(resp.StatusCode), " ", "_"))
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		e.RetryAfter = time.Duration(secs) * time.Second
	}
	return e
}

// redact strips the query from a URL before it goes into an error.
//
// Keys travel in a header rather than a query today, so this is belt and
// braces -- but an error message ends up in a log, a screenshot and a bug
// report, and a credential that can reach any of those is a credential that
// has leaked.
func redact(endpoint string) string {
	if i := strings.IndexByte(endpoint, '?'); i >= 0 {
		return endpoint[:i]
	}
	return endpoint
}

// retryAfter reports how long a failure asks a caller to wait, and whether
// waiting could help at all.
func retryAfter(err error) (wait time.Duration, retry bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return 0, true // A transport failure is exactly what a retry is for.
	}
	if api.Fatal() {
		return 0, false
	}
	return api.RetryAfter, true
}
