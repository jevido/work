package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"dev.jevido/work/internal/ops"
	"dev.jevido/work/server/store"
)

// MaxCachedDocuments caps how many workspaces keep a merged state in memory.
//
// A cached document is the whole workspace: every node, every field, and every
// op ID ever applied, because the merge's idempotence depends on remembering
// them. That is bounded by the log, not by the cache, so the number of them is
// what has to be bounded here. Past the cap the least recently asked-for
// workspace is dropped and rebuilt from the log the next time somebody wants
// it, which costs one replay rather than being wrong.
const MaxCachedDocuments = 64

// documentBody is the merged workspace as GET /v1/document spells it.
//
// Tree and Detached are the merge's own types rather than shapes redeclared
// here. Everything else in this package restates the store's types on the way
// out so that a database rename cannot change the wire; this is the opposite
// case. The whole point of the endpoint is that the viewer sees exactly what
// the merge produced, so a second declaration of these shapes would be a second
// place for the document to drift from the one implementation that defines it.
type documentBody struct {
	Head     int64          `json:"head"`
	Tree     []ops.TreeNode `json:"tree"`
	Detached []ops.Node     `json:"detached"`
}

// document is one workspace's merged state, kept between requests.
//
// Keeping it is the whole reason this is not a per-request replay. A viewer
// polling a four-thousand-op workspace every few seconds would otherwise make
// the server read and decode four thousand rows every few seconds to produce a
// document byte-identical to the last one. Held, a poll that finds nothing new
// is a single indexed query returning no rows: the state is already merged
// through head, and the rendered bytes are already encoded.
//
// The cache is safe because of gaplessness. A workspace's ops are numbered
// 1..head with no holes and are never changed once written, so the sequence
// number a state has been merged through names its contents exactly. There is
// nothing to invalidate — only ops to catch up on.
type document struct {
	// mu guards everything below it, and is held across the catch-up so that
	// two pollers of one workspace do not merge into the same State at once.
	// It is not held while the response is written: a slow reader must not
	// stall every other viewer of the same board.
	mu    sync.Mutex
	state ops.State
	// seq is the sequence number state has been merged through. It advances
	// only after an op has actually been applied, so a merge that fails leaves
	// this pointing at the last op that worked.
	seq int64
	// rendered is the response body for exactly seq, or nil if it has not been
	// encoded yet. Once set it is never mutated, only replaced, so a caller may
	// keep reading it after releasing mu.
	rendered []byte
	etag     string

	// used is the recency tick for eviction. It belongs to the cache, not to
	// the document, and is guarded by the cache's own mutex rather than by mu.
	used int64
}

// documents is the per-workspace document cache.
type documents struct {
	mu    sync.Mutex
	limit int
	tick  int64
	held  map[string]*document
}

func newDocuments(limit int) *documents {
	return &documents{limit: limit, held: make(map[string]*document)}
}

// checkout returns the cached document for a workspace, creating it empty if
// there is none and evicting the least recently used one if that puts the cache
// over its limit.
//
// Evicting a document another goroutine is holding is harmless: that goroutine
// keeps its pointer and finishes against a state nobody will see again. The
// worst outcome is work done twice, never an answer that is wrong.
func (d *documents) checkout(workspaceID string) *document {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.tick++
	if found, ok := d.held[workspaceID]; ok {
		found.used = d.tick
		return found
	}
	if len(d.held) >= d.limit {
		d.evictOldest()
	}
	fresh := &document{used: d.tick}
	d.held[workspaceID] = fresh
	return fresh
}

// evictOldest drops the least recently used document. A linear scan, because
// the cache holds tens of entries and a heap would be more machinery than the
// thing it indexes. Callers hold d.mu.
func (d *documents) evictOldest() {
	var oldestID string
	var oldest int64
	for id, held := range d.held {
		if oldestID == "" || held.used < oldest {
			oldestID, oldest = id, held.used
		}
	}
	delete(d.held, oldestID)
}

func (a *API) document(w http.ResponseWriter, r *http.Request, auth store.Auth) error {
	doc := a.documents.checkout(auth.WorkspaceID)

	rendered, etag, err := doc.current(r.Context(), a.store, auth.WorkspaceID)
	if err != nil {
		return err
	}

	// Vary and a private cache directive because the response depends entirely
	// on the key: two workspaces poll the same URL, and a shared cache keyed on
	// the URL alone would hand one of them the other's board.
	head := w.Header()
	head.Set("ETag", etag)
	head.Set("Cache-Control", "private, no-cache")
	head.Set("Vary", "Authorization")

	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	head.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(rendered)
	return err
}

// current brings the document up to the head of the log and returns the body to
// send for it.
//
// The bytes come back rather than being written here so that the lock is
// released before the response goes out.
func (d *document) current(ctx context.Context, backing Store, workspaceID string) ([]byte, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.catchUp(ctx, backing, workspaceID); err != nil {
		return nil, "", err
	}

	// The workspace ID is in the tag as well as the sequence number. A tag is
	// only ever compared against one the same client got from the same URL, so
	// the number alone would be correct; including the workspace means a tag
	// that somehow crossed workspaces fails the comparison rather than passing
	// it, which is the direction to be wrong in.
	etag := `"` + workspaceID + ":" + strconv.FormatInt(d.seq, 10) + `"`
	if d.rendered != nil && d.etag == etag {
		return d.rendered, d.etag, nil
	}

	tree := d.state.Tree()
	detached := d.state.Detached()
	if detached == nil {
		// Never null. The contract says these are arrays, and a client made to
		// handle both null and [] for one field has been given two shapes to
		// write against instead of one.
		detached = []ops.Node{}
	}
	body, err := json.Marshal(documentBody{Head: d.seq, Tree: tree, Detached: detached})
	if err != nil {
		return nil, "", fmt.Errorf("encoding the document for %s: %w", workspaceID, err)
	}
	d.rendered, d.etag = body, etag
	return d.rendered, d.etag, nil
}

// catchUp applies every op the log has gained since this state was last merged.
//
// It pages, because the log read is capped, and it keeps paging until the last
// sequence number it applied equals the head the store reported. Ops landing
// while it pages are simply included: the loop reads the head again with every
// page, so it stops when it is genuinely current rather than when it reaches a
// head that was current when it started.
func (d *document) catchUp(ctx context.Context, backing Store, workspaceID string) error {
	for {
		log, head, err := backing.Log(ctx, workspaceID, d.seq, MaxLogLimit)
		if err != nil {
			return err
		}
		if len(log) == 0 {
			if d.seq < head {
				// Gaplessness says this cannot happen: the store writes the
				// ops and moves the head in one transaction, and reads the
				// rows before the head, so a head above the last row means a
				// row that is missing rather than one not yet committed.
				// Reporting the document as current here would tell a viewer
				// it has everything when the log has a hole in it.
				return fmt.Errorf("workspace %s: no ops after %d but head is %d", workspaceID, d.seq, head)
			}
			return nil
		}
		for _, entry := range log {
			if _, err := d.state.Apply(entry.Op); err != nil {
				// Every op was validated against this same code before it was
				// written, so one that fails now means the log holds something
				// no replica can apply. The state stops at the last op that
				// worked and every later request fails here again, which is
				// the right kind of loud: there is no correct document to show
				// and pretending otherwise would show a silently truncated one.
				return fmt.Errorf("workspace %s: op %s at seq %d will not apply: %w",
					workspaceID, entry.Op.ID, entry.Seq, err)
			}
			d.seq = entry.Seq
		}
		if d.seq >= head {
			return nil
		}
	}
}

// matchesETag reports whether an If-None-Match header covers this tag.
//
// The header is a comma-separated list, may be `*`, and may carry the weak
// marker. Weak and strong compare the same here because this server only ever
// issues strong tags: a `W/` prefix on a tag it handed out came from an
// intermediary, and the bytes behind it are still the bytes it sent.
func matchesETag(header, tag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == tag {
			return true
		}
	}
	return false
}
