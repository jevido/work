package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"dev.jevido/work/internal/ops"
	"dev.jevido/work/server/store"
)

// fakeStore is enough of a store to exercise the HTTP layer without a database.
//
// It is deliberately simple. What the real store does that this does not — the
// row lock, the transaction, the unique index — is the part that only means
// anything against Postgres, and is tested there. What is reproduced here is
// only what a handler can observe: sequence numbers that count up without gaps,
// and an op ID that lands once.
type fakeStore struct {
	mu sync.Mutex

	workspaces map[string]*store.Workspace
	keys       map[string]store.Auth
	log        map[string][]store.Entry

	// pingErr makes the database look unreachable.
	pingErr error
	// appendErr is returned by the next Append, for testing error mapping.
	appendErr error
	created   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		workspaces: make(map[string]*store.Workspace),
		keys:       make(map[string]store.Auth),
		log:        make(map[string][]store.Entry),
	}
}

// withWorkspace adds a workspace and returns its write and read keys, in the
// shape a real key has so that the length check in the real store would pass
// too.
func (f *fakeStore) withWorkspace(id string) (writeKey, readKey string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.workspaces[id] = &store.Workspace{ID: id, Name: id, CreatedAt: time.Unix(0, 0).UTC()}
	writeKey = "wk_" + fixedHex(id+"-write")
	readKey = "rk_" + fixedHex(id+"-read")
	f.keys[writeKey] = store.Auth{WorkspaceID: id, Access: store.AccessWrite}
	f.keys[readKey] = store.Auth{WorkspaceID: id, Access: store.AccessRead}
	return writeKey, readKey
}

// fixedHex pads a label out to the 32 hex characters a key carries, so test
// keys are the shape the contract describes.
func fixedHex(seed string) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 32)
	for i := range out {
		if i < len(seed) {
			out[i] = hex[int(seed[i])%16]
		} else {
			out[i] = 'a'
		}
	}
	return string(out)
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

func (f *fakeStore) Lookup(_ context.Context, key string) (store.Auth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	auth, ok := f.keys[key]
	if !ok {
		return store.Auth{}, store.ErrNoKey
	}
	return auth, nil
}

func (f *fakeStore) Workspace(_ context.Context, id string) (store.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found, ok := f.workspaces[id]
	if !ok {
		return store.Workspace{}, store.ErrNoWorkspace
	}
	return *found, nil
}

func (f *fakeStore) CreateWorkspace(_ context.Context, name string) (store.Created, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	id := "ws_" + fixedHex(name)
	f.workspaces[id] = &store.Workspace{ID: id, Name: name, CreatedAt: time.Unix(0, 0).UTC()}
	return store.Created{
		Workspace: *f.workspaces[id],
		WriteKey:  "wk_" + fixedHex(name+"-write"),
		ReadKey:   "rk_" + fixedHex(name+"-read"),
	}, nil
}

func (f *fakeStore) Append(_ context.Context, workspaceID string, list []ops.Op) (store.AppendResult, error) {
	if f.appendErr != nil {
		return store.AppendResult{}, f.appendErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	workspace, ok := f.workspaces[workspaceID]
	if !ok {
		return store.AppendResult{}, store.ErrNoWorkspace
	}
	known := make(map[string]store.Entry, len(f.log[workspaceID]))
	for _, entry := range f.log[workspaceID] {
		known[entry.Op.ID] = entry
	}

	result := store.AppendResult{}
	for _, op := range list {
		if prior, ok := known[op.ID]; ok {
			if !sameOp(prior.Op, op) {
				return store.AppendResult{}, store.ErrOpConflict
			}
			result.Duplicates = append(result.Duplicates, store.Appended{OpID: op.ID, Seq: prior.Seq})
			continue
		}
		workspace.Head++
		entry := store.Entry{Seq: workspace.Head, ReceivedAt: time.Unix(0, 0).UTC(), Op: op}
		f.log[workspaceID] = append(f.log[workspaceID], entry)
		known[op.ID] = entry
		result.Accepted = append(result.Accepted, store.Appended{OpID: op.ID, Seq: entry.Seq})
	}
	result.Head = workspace.Head
	return result, nil
}

func sameOp(a, b ops.Op) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}

func (f *fakeStore) Log(_ context.Context, workspaceID string, since int64, limit int) ([]store.Entry, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	workspace, ok := f.workspaces[workspaceID]
	if !ok {
		return nil, 0, store.ErrNoWorkspace
	}
	var out []store.Entry
	for _, entry := range f.log[workspaceID] {
		if entry.Seq > since {
			out = append(out, entry)
		}
		if len(out) == limit {
			break
		}
	}
	return out, workspace.Head, nil
}

// errBroken stands in for anything the store fails at that a handler is not
// expected to recognise.
var errBroken = errors.New("the database fell over")
