package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"dev.jevido/work/internal/ops"
)

// fetchDocument gets /v1/document, optionally with an If-None-Match, and hands
// back the response with its body already read. The shared call helper sends no
// extra headers, and conditional requests are half of what this endpoint is.
func fetchDocument(t *testing.T, srv *httptest.Server, key, ifNoneMatch string) (*http.Response, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/document", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/document: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	return resp, raw
}

// replay merges a log from nothing and encodes it the way the endpoint does.
//
// This is the oracle the served document is checked against. The endpoint keeps
// a merged state between requests and advances it; this throws that state away
// and starts over. If the two ever differ, the cache has drifted from the merge
// it is supposed to be a cache of, which is the only way this endpoint can lie.
func replay(t *testing.T, log []ops.Op) []byte {
	t.Helper()

	var state ops.State
	if _, err := state.ApplyAll(log); err != nil {
		t.Fatalf("merging the reference log: %v", err)
	}
	detached := state.Detached()
	if detached == nil {
		detached = []ops.Node{}
	}
	raw, err := json.Marshal(documentBody{Head: int64(len(log)), Tree: state.Tree(), Detached: detached})
	if err != nil {
		t.Fatalf("encoding the reference document: %v", err)
	}
	return raw
}

// field is the JSON for a single title, which is enough content to tell nodes
// apart in a rendered document.
func field(title string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"title": json.RawMessage(fmt.Sprintf("%q", title))}
}

func create(id, node, parent, position string, clock uint64) ops.Op {
	return ops.Op{
		ID: id, Kind: ops.KindCreateNode, Actor: "desktop-test", Clock: clock,
		Node: node, Parent: parent, Position: position, Fields: field(node),
	}
}

// readDocument reads the endpoint and insists on a 200 with a JSON body.
func readDocument(t *testing.T, srv *httptest.Server, key string) (map[string]any, []byte, string) {
	t.Helper()

	resp, raw := fetchDocument(t, srv, key, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding the document: %v", err)
	}
	return decoded, raw, resp.Header.Get("ETag")
}

func TestDocumentIsAlwaysAColdReplay(t *testing.T) {
	// The property: whatever the endpoint has cached, what it serves is byte
	// for byte what merging the whole log from nothing produces. Every batch
	// below arrives while a state from the batch before it is already held, so
	// this exercises the incremental path rather than a fresh merge each time.
	batches := [][]ops.Op{
		{create("o1", "root", "", "m", 1)},
		{
			create("o2", "kid", "root", "a", 2),
			create("o3", "sibling", "root", "b", 3),
		},
		{
			// A child of a node no op has mentioned: detached, and it stays
			// detached until that op arrives.
			create("o4", "early", "ghost", "a", 4),
		},
		{
			// The missing parent lands, and `early` joins the tree.
			create("o5", "ghost", "", "z", 5),
		},
		{
			// A delete takes `root` out of the tree and pushes its children
			// into the detached list rather than deleting them.
			{ID: "o6", Kind: ops.KindDeleteNode, Actor: "desktop-test", Clock: 6, Node: "root"},
		},
		{
			// Edits after a delete still merge, and still show up nowhere.
			{ID: "o7", Kind: ops.KindSetFields, Actor: "desktop-test", Clock: 7, Node: "root", Fields: field("edited after the delete")},
			// A move out from under the tombstone puts a child back in the tree.
			{ID: "o8", Kind: ops.KindMoveNode, Actor: "desktop-test", Clock: 8, Node: "kid", Parent: "ghost", Position: "b"},
		},
		{
			{
				ID: "o9", Kind: ops.KindExtractToTask, Actor: "desktop-test", Clock: 9,
				Node: "kid", Task: "task-1", Parent: "", Position: "y", Fields: field("extracted"),
			},
		},
	}

	srv, backing := serve(t, "")
	writeKey, readKey := backing.withWorkspace("ws_replay")

	var log []ops.Op
	for i, batch := range batches {
		if _, err := backing.Append(t.Context(), "ws_replay", batch); err != nil {
			t.Fatalf("batch %d: appending: %v", i, err)
		}
		log = append(log, batch...)

		_, raw, _ := readDocument(t, srv, readKey)
		if want := replay(t, log); string(raw) != string(want) {
			t.Fatalf("after batch %d the document has drifted from a cold replay\n got %s\nwant %s", i, raw, want)
		}
	}

	// The write key reaches it too: read is a floor, not an exact match.
	if _, raw, _ := readDocument(t, srv, writeKey); string(raw) != string(replay(t, log)) {
		t.Error("the write key gets a different document from the read key")
	}
}

func TestDocumentSeparatesReachableFromDetached(t *testing.T) {
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_shape")

	log := []ops.Op{
		create("o1", "kept", "", "a", 1),
		create("o2", "doomed", "", "b", 2),
		create("o3", "stranded", "doomed", "a", 3),
		{ID: "o4", Kind: ops.KindDeleteNode, Actor: "desktop-test", Clock: 4, Node: "doomed"},
	}
	if _, err := backing.Append(t.Context(), "ws_shape", log); err != nil {
		t.Fatalf("appending: %v", err)
	}

	decoded, _, _ := readDocument(t, srv, readKey)

	if decoded["head"] != float64(4) {
		t.Errorf("head = %v, want 4", decoded["head"])
	}

	tree, ok := decoded["tree"].([]any)
	if !ok {
		t.Fatalf("tree = %v, want an array", decoded["tree"])
	}
	if len(tree) != 1 || tree[0].(map[string]any)["id"] != "kept" {
		t.Fatalf("tree = %v, want only the reachable root", tree)
	}

	detached, ok := decoded["detached"].([]any)
	if !ok {
		t.Fatalf("detached = %v, want an array", decoded["detached"])
	}
	if len(detached) != 1 {
		t.Fatalf("detached = %v, want the one node under the tombstone", detached)
	}
	stranded := detached[0].(map[string]any)
	if stranded["id"] != "stranded" {
		t.Errorf("detached[0].id = %v, want stranded", stranded["id"])
	}
	// A viewer has to be able to say where it belonged, which is the difference
	// between "not filed" and "lost".
	if stranded["parent"] != "doomed" {
		t.Errorf("detached[0].parent = %v, want doomed", stranded["parent"])
	}
	if _, present := stranded["children"]; present {
		t.Error("detached entries are flat; this one carries children")
	}

	t.Run("the tombstone is in neither list", func(t *testing.T) {
		for _, list := range []string{"tree", "detached"} {
			for _, entry := range decoded[list].([]any) {
				if entry.(map[string]any)["id"] == "doomed" {
					t.Errorf("the deleted node is in %s", list)
				}
			}
			for _, entry := range decoded[list].([]any) {
				if _, present := entry.(map[string]any)["deleted"]; present {
					t.Errorf("%s carries a deleted flag; the contract says tombstones are absent, not marked", list)
				}
			}
		}
	})

	t.Run("an empty workspace is two empty arrays, not two nulls", func(t *testing.T) {
		backing.withWorkspace("ws_empty")
		_, empty := backing.withWorkspace("ws_empty")

		decoded, raw, _ := readDocument(t, srv, empty)
		if decoded["head"] != float64(0) {
			t.Errorf("head = %v, want 0", decoded["head"])
		}
		if string(raw) != `{"head":0,"tree":[],"detached":[]}` {
			t.Errorf("body = %s, want empty arrays", raw)
		}
	})
}

func TestDocumentPollIsIncremental(t *testing.T) {
	// This is the caching decision under test. A poll that finds nothing new
	// must cost one query that returns no rows — not a re-read of the log — or
	// the endpoint is a per-request replay wearing a cache's clothes.
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_poll")

	log := []ops.Op{create("o1", "a", "", "a", 1), create("o2", "b", "", "b", 2)}
	if _, err := backing.Append(t.Context(), "ws_poll", log); err != nil {
		t.Fatalf("appending: %v", err)
	}

	_, first, firstTag := readDocument(t, srv, readKey)
	backing.logs()

	_, second, secondTag := readDocument(t, srv, readKey)
	if string(first) != string(second) {
		t.Errorf("two polls of an unchanged workspace disagree:\n%s\n%s", first, second)
	}
	if firstTag != secondTag {
		t.Errorf("ETag changed without the log changing: %s then %s", firstTag, secondTag)
	}

	calls := backing.logs()
	if len(calls) != 1 {
		t.Fatalf("an unchanged poll made %d log reads, want 1", len(calls))
	}
	if calls[0] != 2 {
		t.Errorf("the poll read the log from %d, want from the head at 2", calls[0])
	}

	t.Run("a new op moves the tag and is picked up from where the state stopped", func(t *testing.T) {
		if _, err := backing.Append(t.Context(), "ws_poll", []ops.Op{create("o3", "c", "", "c", 3)}); err != nil {
			t.Fatalf("appending: %v", err)
		}
		backing.logs()

		decoded, raw, tag := readDocument(t, srv, readKey)
		if tag == firstTag {
			t.Error("the ETag did not move when the log did")
		}
		if decoded["head"] != float64(3) {
			t.Errorf("head = %v, want 3", decoded["head"])
		}
		if want := replay(t, append(log, create("o3", "c", "", "c", 3))); string(raw) != string(want) {
			t.Errorf("body = %s, want %s", raw, want)
		}
		for _, since := range backing.logs() {
			if since < 2 {
				t.Errorf("the catch-up re-read the log from %d; it already had everything to 2", since)
			}
		}
	})
}

func TestDocumentConditionalRequest(t *testing.T) {
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_cond")

	if _, err := backing.Append(t.Context(), "ws_cond", []ops.Op{create("o1", "a", "", "a", 1)}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	_, full, tag := readDocument(t, srv, readKey)
	if tag == "" {
		t.Fatal("no ETag on the document")
	}

	t.Run("an unchanged document is a 304 with no body", func(t *testing.T) {
		resp, raw := fetchDocument(t, srv, readKey, tag)
		if resp.StatusCode != http.StatusNotModified {
			t.Fatalf("status = %d, want 304; body %s", resp.StatusCode, raw)
		}
		if len(raw) != 0 {
			t.Errorf("304 carried a body: %s", raw)
		}
		if resp.Header.Get("ETag") != tag {
			t.Errorf("304 ETag = %q, want %q", resp.Header.Get("ETag"), tag)
		}
		if resp.Header.Get("Vary") != "Authorization" {
			t.Errorf("Vary = %q, want Authorization; the body depends entirely on the key",
				resp.Header.Get("Vary"))
		}
	})

	t.Run("a stale tag gets the whole document", func(t *testing.T) {
		resp, raw := fetchDocument(t, srv, readKey, `"ws_cond:0"`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if string(raw) != string(full) {
			t.Errorf("body = %s, want %s", raw, full)
		}
	})

	t.Run("a new op makes the held tag stale", func(t *testing.T) {
		if _, err := backing.Append(t.Context(), "ws_cond", []ops.Op{create("o2", "b", "", "b", 2)}); err != nil {
			t.Fatalf("appending: %v", err)
		}
		resp, raw := fetchDocument(t, srv, readKey, tag)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if string(raw) == string(full) {
			t.Error("the body did not change after an op landed")
		}
	})

	t.Run("a tag list and the weak marker both match", func(t *testing.T) {
		_, _, current := readDocument(t, srv, readKey)
		for _, header := range []string{
			`"ws_cond:0", ` + current,
			"W/" + current,
			"*",
		} {
			resp, _ := fetchDocument(t, srv, readKey, header)
			if resp.StatusCode != http.StatusNotModified {
				t.Errorf("If-None-Match: %s gave %d, want 304", header, resp.StatusCode)
			}
		}
	})

	t.Run("another workspace's tag never matches", func(t *testing.T) {
		_, otherKey := backing.withWorkspace("ws_other")
		resp, _ := fetchDocument(t, srv, otherKey, tag)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200; a tag from another workspace matched", resp.StatusCode)
		}
	})
}

func TestDocumentMergesALogLongerThanOnePage(t *testing.T) {
	// The log read is capped at MaxLogLimit, so catching up has to page. A
	// document that stopped at the first page would be silently short.
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_long")

	const count = MaxLogLimit*2 + 7
	log := make([]ops.Op, 0, count)
	for i := range count {
		log = append(log, create(fmt.Sprintf("o%04d", i), fmt.Sprintf("n%04d", i), "", fmt.Sprintf("p%04d", i), uint64(i+1)))
	}
	if _, err := backing.Append(t.Context(), "ws_long", log); err != nil {
		t.Fatalf("appending: %v", err)
	}

	decoded, raw, _ := readDocument(t, srv, readKey)
	if decoded["head"] != float64(count) {
		t.Errorf("head = %v, want %d", decoded["head"], count)
	}
	if got := len(decoded["tree"].([]any)); got != count {
		t.Errorf("tree has %d roots, want %d", got, count)
	}
	if want := replay(t, log); string(raw) != string(want) {
		t.Error("a multi-page document differs from a cold replay")
	}
}

func TestDocumentRefusesAGapInTheLog(t *testing.T) {
	// Gaplessness is what makes the cache key sound. A head with no rows behind
	// it means the invariant has broken, and serving the document anyway would
	// tell a viewer it has everything while an op is missing.
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_gap")

	if _, err := backing.Append(t.Context(), "ws_gap", []ops.Op{create("o1", "a", "", "a", 1)}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	backing.phantomHead = 9

	resp, _ := fetchDocument(t, srv, readKey, "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDocumentCacheEvicts(t *testing.T) {
	// More workspaces than the cache holds. Eviction may cost a replay; it may
	// not cost correctness.
	srv, backing := serve(t, "")

	keys := make([]string, MaxCachedDocuments+4)
	wants := make([][]byte, len(keys))
	for i := range keys {
		id := fmt.Sprintf("ws_evict_%02d", i)
		_, keys[i] = backing.withWorkspace(id)
		log := []ops.Op{create(fmt.Sprintf("o%02d", i), fmt.Sprintf("n%02d", i), "", "a", 1)}
		if _, err := backing.Append(t.Context(), id, log); err != nil {
			t.Fatalf("appending to %s: %v", id, err)
		}
		wants[i] = replay(t, log)
		if _, raw, _ := readDocument(t, srv, keys[i]); string(raw) != string(wants[i]) {
			t.Fatalf("%s: first read = %s, want %s", id, raw, wants[i])
		}
	}

	// The earliest workspaces have certainly been evicted by now. They must
	// still answer, and answer the same.
	for i, key := range keys {
		if _, raw, _ := readDocument(t, srv, key); string(raw) != string(wants[i]) {
			t.Errorf("workspace %d after eviction = %s, want %s", i, raw, wants[i])
		}
	}
}

func TestDocumentUnderConcurrentPollers(t *testing.T) {
	// One State is not safe for concurrent use, and every viewer of a workspace
	// shares one. Run with -race.
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_race")

	for i := range 40 {
		op := create(fmt.Sprintf("o%02d", i), fmt.Sprintf("n%02d", i), "", fmt.Sprintf("p%02d", i), uint64(i+1))
		if _, err := backing.Append(t.Context(), "ws_race", []ops.Op{op}); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	// Kept under the per-key burst on purpose: this test is about two pollers
	// sharing one merged state, and a 429 would be the limiter answering rather
	// than the document path.
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				resp, raw := fetchDocument(t, srv, readKey, "")
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status = %d, want 200; body %s", resp.StatusCode, raw)
					return
				}
				var decoded documentBody
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Errorf("decoding: %v", err)
					return
				}
				// Every poller sees the same finished log, so a short tree is a
				// state two goroutines trod on rather than a slow one.
				if decoded.Head != 40 || len(decoded.Tree) != 40 {
					t.Errorf("head %d with %d roots, want 40 and 40", decoded.Head, len(decoded.Tree))
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDocumentNeedsAKey(t *testing.T) {
	srv, backing := serve(t, "")
	backing.withWorkspace("ws_auth")

	for name, key := range map[string]string{
		"no key":      "",
		"unknown key": "rk_" + fixedHex("nobody"),
	} {
		t.Run(name, func(t *testing.T) {
			resp, _ := fetchDocument(t, srv, key, "")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// The document path needs nothing of the Store beyond what the rest of the
// package already declared, which is why this endpoint tests with no database
// like everything else here.
var _ Store = (*fakeStore)(nil)
