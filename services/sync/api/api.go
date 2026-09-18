// Package api is the sync server's HTTP surface: the routes, the shapes on the
// wire, and the rules about who may call what.
//
// The shapes here are the contract in services/sync/README.md, which the desktop app
// and the web viewer are both written against. They are declared as explicit
// response types rather than assembled from store types on the way out, so that
// renaming a database field cannot quietly change what a client receives.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dev.jevido/work/packages/ops"
	"dev.jevido/work/services/sync/store"
)

// Limits on one request. They are part of the contract, so they are constants
// here rather than configuration: a client cannot discover them by trial.
const (
	// MaxOpsPerRequest caps how many ops one append may carry.
	MaxOpsPerRequest = 500
	// MaxBodyBytes caps a request body.
	MaxBodyBytes = 1 << 20
	// MaxLogLimit caps, and is the default for, how many ops one read returns.
	MaxLogLimit = 1000
	// MaxWorkspaceNameLen caps a workspace's label.
	MaxWorkspaceNameLen = 200
)

// Store is the persistence this package needs. It is declared here, where it is
// used, so that the HTTP layer can be tested against anything that satisfies it
// — which is how these handlers are tested without a database.
type Store interface {
	Ping(ctx context.Context) error
	Lookup(ctx context.Context, key string) (store.Auth, error)
	Workspace(ctx context.Context, id string) (store.Workspace, error)
	CreateWorkspace(ctx context.Context, name string) (store.Created, error)
	MintKey(ctx context.Context, workspaceID string, access store.Access) (string, error)
	Append(ctx context.Context, workspaceID string, list []ops.Op) (store.AppendResult, error)
	Log(ctx context.Context, workspaceID string, since int64, limit int) ([]store.Entry, int64, error)
}

// API serves the sync endpoints.
type API struct {
	store  Store
	logger *slog.Logger
	// signupToken gates workspace creation. Empty means open signup, which is
	// the default: a server nobody can create a workspace on is a server the
	// app cannot be set up against.
	signupToken string
	limiter     *limiter
	// documents is the merged state behind GET /v1/document, one per workspace
	// and bounded, so that a viewer polling does not replay the log each time.
	documents *documents
	// site is the viewer's built files, or nil when this server is API only.
	// See static.go.
	site fs.FS
}

// New builds the API. An empty signupToken leaves workspace creation open, and
// a nil site makes the root answer as an API endpoint that does not exist.
//
// The site is a parameter rather than something set afterwards: an API that is
// only half built between New and a later call is an API that serves a 404 for
// the page during startup, which is exactly the window a health check looks in.
func New(backing Store, signupToken string, logger *slog.Logger, site fs.FS) *API {
	return &API{
		store:       backing,
		logger:      logger,
		signupToken: signupToken,
		limiter:     newLimiter(),
		documents:   newDocuments(MaxCachedDocuments),
		site:        site,
	}
}

// Handler is the router.
//
// Every path is registered twice: once per method it supports, and once without
// a method at all. The method-less pattern is less specific, so it only catches
// what the others did not, which turns the standard mux's plain-text 405 into
// the JSON error every other failure here uses.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /v1/health", a.plain(a.health))
	mux.Handle("/v1/health", a.plain(methodNotAllowed))

	mux.Handle("POST /v1/workspaces", a.limited(a.createWorkspace))
	mux.Handle("/v1/workspaces", a.plain(methodNotAllowed))

	mux.Handle("GET /v1/workspace", a.authed(store.AccessRead, a.workspace))
	mux.Handle("/v1/workspace", a.plain(methodNotAllowed))

	// Write access, and not because minting is a write to the log -- it is
	// not. It is because a read key that could mint a write key would be a read
	// key with write access, one request later.
	mux.Handle("POST /v1/keys", a.authed(store.AccessWrite, a.mintKey))
	mux.Handle("/v1/keys", a.plain(methodNotAllowed))

	mux.Handle("POST /v1/ops", a.authed(store.AccessWrite, a.appendOps))
	mux.Handle("GET /v1/ops", a.authed(store.AccessRead, a.readLog))
	mux.Handle("/v1/ops", a.plain(methodNotAllowed))

	// Read access, because the read-only viewer is what this is for.
	mux.Handle("GET /v1/document", a.authed(store.AccessRead, a.document))
	mux.Handle("/v1/document", a.plain(methodNotAllowed))

	// Every unmatched API path, before the root catches it. This pattern is
	// more specific than "/", so it takes what the named routes above did not
	// -- and without it a mistyped endpoint would answer 200 with the viewer's
	// HTML to a client that parses JSON, which is a worse failure than a 404
	// by some distance.
	mux.Handle("/v1/", a.plain(func(http.ResponseWriter, *http.Request) error {
		return apiError{http.StatusNotFound, "not_found", "no such endpoint"}
	}))

	// The viewer, or the same 404 as before when there is none. See static.go.
	mux.Handle("/", a.static())
	return mux
}

// workspaceBody is the workspace as every response spells it.
type workspaceBody struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Head      int64     `json:"head"`
	CreatedAt time.Time `json:"createdAt"`
}

func describe(w store.Workspace) workspaceBody {
	return workspaceBody{ID: w.ID, Name: w.Name, Head: w.Head, CreatedAt: w.CreatedAt.UTC()}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) error {
	if err := a.store.Ping(r.Context()); err != nil {
		return fmt.Errorf("pinging the database: %w", err)
	}
	return writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{"ok"})
}

func (a *API) createWorkspace(w http.ResponseWriter, r *http.Request) error {
	// An unset signup token means open signup: anyone who can reach this
	// server may create a workspace. That is the deliberate default, because
	// the alternative -- a token every new user has to be handed out of band
	// before the app does anything at all -- is a wall in front of the first
	// thing anybody tries. Creation is still rate limited like every other
	// endpoint, and a workspace costs a row and two keys.
	//
	// Setting WORK_SIGNUP_TOKEN closes it again for a server that should only
	// ever hold the workspaces it already has.
	if a.signupToken != "" {
		presented, ok := bearer(r)
		if !ok || !sameSecret(presented, a.signupToken) {
			return errUnauthorized
		}
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > MaxWorkspaceNameLen {
		return apiError{http.StatusBadRequest, "bad_request",
			fmt.Sprintf("name must be 1 to %d characters", MaxWorkspaceNameLen)}
	}

	created, err := a.store.CreateWorkspace(r.Context(), name)
	if err != nil {
		return fmt.Errorf("creating workspace: %w", err)
	}
	a.logger.Info("workspace created", "workspace", created.Workspace.ID, "name", name)

	return writeJSON(w, http.StatusCreated, struct {
		Workspace workspaceBody `json:"workspace"`
		WriteKey  string        `json:"writeKey"`
		ReadKey   string        `json:"readKey"`
	}{describe(created.Workspace), created.WriteKey, created.ReadKey})
}

// mintKey issues another key for the workspace the caller's key opens.
//
// There is no endpoint that reads a key back, and there cannot be: keys are
// stored as hashes, so the plaintext of the one handed out at creation does not
// exist anywhere on this side. What somebody actually wants when they ask to
// "see the read key again" is a read key they can send to a colleague, and this
// gives them one.
//
// The keys already in use keep working. Minting is not rotation, and a button
// that quietly cut off every other machine in the workspace would be a very
// expensive way to get a link.
func (a *API) mintKey(w http.ResponseWriter, r *http.Request, auth store.Auth) error {
	var body struct {
		Access string `json:"access"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	access := store.Access(strings.TrimSpace(body.Access))
	if access != store.AccessRead && access != store.AccessWrite {
		return apiError{http.StatusBadRequest, "bad_request", `access must be "read" or "write"`}
	}

	key, err := a.store.MintKey(r.Context(), auth.WorkspaceID, access)
	if err != nil {
		return fmt.Errorf("minting a key: %w", err)
	}
	a.logger.Info("key minted", "workspace", auth.WorkspaceID, "access", access)

	return writeJSON(w, http.StatusCreated, struct {
		Key    string       `json:"key"`
		Access store.Access `json:"access"`
	}{key, access})
}

func (a *API) workspace(w http.ResponseWriter, r *http.Request, auth store.Auth) error {
	found, err := a.store.Workspace(r.Context(), auth.WorkspaceID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, struct {
		Workspace workspaceBody `json:"workspace"`
		Access    store.Access  `json:"access"`
	}{describe(found), auth.Access})
}

func (a *API) appendOps(w http.ResponseWriter, r *http.Request, auth store.Auth) error {
	var body struct {
		Ops []ops.Op `json:"ops"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	switch {
	case len(body.Ops) == 0:
		return apiError{http.StatusBadRequest, "bad_request", "ops is empty"}
	case len(body.Ops) > MaxOpsPerRequest:
		return apiError{http.StatusBadRequest, "bad_request",
			fmt.Sprintf("%d ops, over the limit of %d", len(body.Ops), MaxOpsPerRequest)}
	}
	// Ops are validated by the same code the desktop app merges them with, so
	// the log cannot hold an op that a replica would later refuse to apply.
	for _, op := range body.Ops {
		if err := op.Validate(); err != nil {
			return apiError{http.StatusBadRequest, "bad_request", err.Error()}
		}
	}

	result, err := a.store.Append(r.Context(), auth.WorkspaceID, body.Ops)
	if err != nil {
		if errors.Is(err, store.ErrOpConflict) {
			return apiError{http.StatusBadRequest, "bad_request", err.Error()}
		}
		// A 409 rather than a 400, because the request was well formed and the
		// workspace moved: the same body would have been accepted before the
		// delete landed and will never be accepted again. The message names
		// the op and the node for a human reading a log; a client works out
		// which of its ops to drop by catching up on the log and merging,
		// which tells it every tombstone at once rather than one per refused
		// request.
		var deleted store.DeletedError
		if errors.As(err, &deleted) {
			return apiError{http.StatusConflict, "node_deleted", err.Error()}
		}
		return err
	}

	return writeJSON(w, http.StatusOK, struct {
		Head       int64      `json:"head"`
		Accepted   []appended `json:"accepted"`
		Duplicates []appended `json:"duplicates"`
	}{result.Head, landed(result.Accepted), landed(result.Duplicates)})
}

type appended struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}

// landed converts where ops ended up, and never returns nil: the contract says
// these are arrays, and a client that has to handle both null and [] for the
// same field has been given two shapes to write against instead of one.
func landed(list []store.Appended) []appended {
	out := make([]appended, len(list))
	for i, one := range list {
		out[i] = appended{ID: one.OpID, Seq: one.Seq}
	}
	return out
}

func (a *API) readLog(w http.ResponseWriter, r *http.Request, auth store.Auth) error {
	since, err := intParam(r, "since", 0, 0)
	if err != nil {
		return err
	}
	limit, err := intParam(r, "limit", MaxLogLimit, 1)
	if err != nil {
		return err
	}
	limit = min(limit, MaxLogLimit)

	log, head, err := a.store.Log(r.Context(), auth.WorkspaceID, since, int(limit))
	if err != nil {
		return err
	}

	type entry struct {
		Seq        int64     `json:"seq"`
		ReceivedAt time.Time `json:"receivedAt"`
		Op         ops.Op    `json:"op"`
	}
	out := make([]entry, len(log))
	for i, one := range log {
		out[i] = entry{Seq: one.Seq, ReceivedAt: one.ReceivedAt.UTC(), Op: one.Op}
	}

	// Gaplessness is what makes this a comparison rather than a lookahead row:
	// the caller has everything up to the last seq returned, so there is more
	// exactly when that is short of the head.
	reached := since
	if len(log) > 0 {
		reached = log[len(log)-1].Seq
	}
	return writeJSON(w, http.StatusOK, struct {
		Head int64   `json:"head"`
		Ops  []entry `json:"ops"`
		More bool    `json:"more"`
	}{head, out, reached < head})
}

// intParam reads a non-negative integer query parameter, defaulting when it is
// absent and refusing what it cannot make sense of rather than guessing.
func intParam(r *http.Request, name string, fallback, low int64) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < low {
		return 0, apiError{http.StatusBadRequest, "bad_request",
			fmt.Sprintf("%s must be a whole number of at least %d", name, low)}
	}
	return value, nil
}

// decode reads a JSON body, capped so that a large one is refused rather than
// buffered.
//
// Unknown fields are ignored on purpose: a newer client talking to an older
// server is a thing that will happen every time this is deployed, and it should
// not be a 400.
func decode(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return apiError{http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("body is over the %d byte limit", MaxBodyBytes)}
		}
		return apiError{http.StatusBadRequest, "bad_request", "body is not valid JSON: " + err.Error()}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) error {
	// Encoded before anything is written, so a failure can still become an
	// error response instead of a half-sent one.
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding response: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(encoded)
	return err
}

func methodNotAllowed(http.ResponseWriter, *http.Request) error {
	return apiError{http.StatusMethodNotAllowed, "method_not_allowed", "wrong method for this endpoint"}
}
