package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dev.jevido/work/internal/ops"
	"dev.jevido/work/server/api"
	"dev.jevido/work/server/store"
)

// This is the only place the real handlers and the real Postgres meet, so it is
// the only place that can check the thing that actually ships: that ops posted
// over HTTP come back out of the log and merge to what the contract in
// README.md says they should.
//
// The package's own tests each cover one layer — the API against an in-memory
// store, the store against Postgres — and both would still pass if the wiring
// between them were wrong. It skips without a database, like the store's tests:
//
//	TEST_DATABASE_URL=postgres://localhost/work_test?sslmode=disable go test ./...
const signupToken = "an-end-to-end-signup-token-long-enough"

type client struct {
	t   *testing.T
	url string
	key string
}

func stand(t *testing.T) *client {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	backing := store.New(pool)
	if err := store.Migrate(t.Context(), backing, quiet); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	// Wrapped exactly as run() wraps it, so what this exercises is the handler
	// that ships rather than one assembled for the test.
	srv := httptest.NewServer(withRequestLog(quiet, api.New(backing, signupToken, quiet).Handler()))
	t.Cleanup(srv.Close)
	return &client{t: t, url: srv.URL}
}

// do makes a request and decodes the response, failing on any status other than
// the one wanted.
func (c *client) do(method, path string, want int, body any) map[string]any {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encoding: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.url+path, reader)
	if err != nil {
		c.t.Fatalf("building request: %v", err)
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		c.t.Fatalf("%s %s: decoding: %v", method, path, err)
	}
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d; body %v", method, path, resp.StatusCode, want, decoded)
	}
	return decoded
}

// TestEndToEnd walks the path a desktop replica takes, then replays what came
// back through the merge and checks it against the three rules in README.md.
func TestEndToEnd(t *testing.T) {
	c := stand(t)

	c.key = signupToken
	created := c.do(http.MethodPost, "/v1/workspaces", http.StatusCreated,
		map[string]any{"name": "end-to-end"})
	writeKey := created["writeKey"].(string)
	readKey := created["readKey"].(string)

	// An outline with three nodes, then two replicas editing the same node at
	// the same clock, a delete racing an edit from far in the future, and an
	// extraction. Every rule the contract states is in here somewhere.
	first := []ops.Op{
		{ID: "e1", Kind: ops.KindCreateNode, Actor: "a", Clock: 1, Node: "n1", Position: "m",
			Fields: fieldsOf(t, map[string]any{"title": "outline"})},
		{ID: "e2", Kind: ops.KindCreateNode, Actor: "a", Clock: 2, Node: "n2", Parent: "n1", Position: "a",
			Fields: fieldsOf(t, map[string]any{"title": "first"})},
		{ID: "e3", Kind: ops.KindCreateNode, Actor: "a", Clock: 3, Node: "n3", Parent: "n1", Position: "b",
			Fields: fieldsOf(t, map[string]any{"title": "doomed"})},
	}
	second := []ops.Op{
		// Same clock, different actors, same field: bob wins the tiebreak.
		{ID: "e4", Kind: ops.KindSetFields, Actor: "alice", Clock: 5, Node: "n2",
			Fields: fieldsOf(t, map[string]any{"title": "from-alice"})},
		{ID: "e5", Kind: ops.KindSetFields, Actor: "bob", Clock: 5, Node: "n2",
			Fields: fieldsOf(t, map[string]any{"title": "from-bob", "status": "doing"})},
		{ID: "e6", Kind: ops.KindDeleteNode, Actor: "a", Clock: 6, Node: "n3"},
		// An edit from far past the delete. The node stays deleted; the field
		// still merges, which is the refinement rule two spells out.
		{ID: "e7", Kind: ops.KindSetFields, Actor: "z", Clock: 900, Node: "n3",
			Fields: fieldsOf(t, map[string]any{"title": "edited after the delete"})},
		{ID: "e8", Kind: ops.KindExtractToTask, Actor: "a", Clock: 10, Node: "n2", Task: "t1",
			Parent: "n1", Position: "z", Fields: fieldsOf(t, map[string]any{"status": "todo"})},
	}

	c.key = writeKey
	if head := c.do(http.MethodPost, "/v1/ops", http.StatusOK,
		map[string]any{"ops": first})["head"]; head != float64(3) {
		t.Fatalf("head = %v after the first batch, want 3", head)
	}
	if head := c.do(http.MethodPost, "/v1/ops", http.StatusOK,
		map[string]any{"ops": second})["head"]; head != float64(8) {
		t.Fatalf("head = %v after the second batch, want 8", head)
	}

	t.Run("a replayed batch writes nothing", func(t *testing.T) {
		replay := c.do(http.MethodPost, "/v1/ops", http.StatusOK, map[string]any{"ops": second})
		if head := replay["head"]; head != float64(8) {
			t.Errorf("head = %v on a replay, want 8", head)
		}
		if accepted := replay["accepted"].([]any); len(accepted) != 0 {
			t.Errorf("accepted = %v on a replay, want nothing written", accepted)
		}
		if duplicates := replay["duplicates"].([]any); len(duplicates) != len(second) {
			t.Errorf("duplicates = %d, want all %d", len(duplicates), len(second))
		}
	})

	t.Run("a read key cannot write", func(t *testing.T) {
		reader := &client{t: t, url: c.url, key: readKey}
		reader.do(http.MethodPost, "/v1/ops", http.StatusForbidden,
			map[string]any{"ops": first})
	})

	// Page the whole log back with a read key, the way the viewer does.
	reader := &client{t: t, url: c.url, key: readKey}
	var log []ops.Op
	var since float64
	for page := 0; ; page++ {
		if page > len(first)+len(second) {
			t.Fatal("paging did not terminate")
		}
		body := reader.do(http.MethodGet, fmt.Sprintf("/v1/ops?since=%d&limit=3", int(since)), http.StatusOK, nil)
		for _, raw := range body["ops"].([]any) {
			entry := raw.(map[string]any)
			var op ops.Op
			remarshal(t, entry["op"], &op)
			log = append(log, op)
			since = entry["seq"].(float64)
		}
		if body["more"] == false {
			// The contract says this is exactly when the caller is caught up.
			if since != body["head"] {
				t.Fatalf("more is false at seq %v with head %v", since, body["head"])
			}
			break
		}
	}
	if len(log) != len(first)+len(second) {
		t.Fatalf("read back %d ops, want %d", len(log), len(first)+len(second))
	}

	// Replay through the shared merge, which is what every consumer does.
	var state ops.State
	if _, err := state.ApplyAll(log); err != nil {
		t.Fatalf("merging the log the server returned: %v", err)
	}

	t.Run("rule one: fields are last-write-wins, one field at a time", func(t *testing.T) {
		if got := fieldOf(t, &state, "n2", "title"); got != `"from-bob"` {
			t.Errorf("title = %s, want \"from-bob\": the higher actor breaks an equal clock", got)
		}
		if got := fieldOf(t, &state, "n2", "status"); got != `"doing"` {
			t.Errorf("status = %s, want \"doing\": a field nobody contested survives", got)
		}
	})

	t.Run("rule two: delete beats the edit, and the edit still merges", func(t *testing.T) {
		node, ok := state.Node("n3")
		if !ok {
			t.Fatal("the tombstone is gone entirely")
		}
		if !node.Deleted {
			t.Error("an edit at clock 900 beat a delete at clock 6")
		}
		if got := fieldOf(t, &state, "n3", "title"); got != `"edited after the delete"` {
			t.Errorf("title = %s, want the later edit merged so the tombstone reads the same everywhere", got)
		}
	})

	t.Run("extract-to-task links both ways", func(t *testing.T) {
		if got := fieldOf(t, &state, "n2", ops.FieldTaskID); got != `"t1"` {
			t.Errorf("%s = %s, want \"t1\"", ops.FieldTaskID, got)
		}
		if got := fieldOf(t, &state, "t1", ops.FieldExtractedFrom); got != `"n2"` {
			t.Errorf("%s = %s, want \"n2\"", ops.FieldExtractedFrom, got)
		}
	})

	t.Run("the tree is what the viewer draws", func(t *testing.T) {
		tree := state.Tree()
		if len(tree) != 1 || tree[0].ID != "n1" {
			t.Fatalf("roots = %v, want just n1", tree)
		}
		got := make([]string, len(tree[0].Children))
		for i, child := range tree[0].Children {
			got[i] = child.ID
		}
		// n2 at "a", t1 at "z", and n3 nowhere because it is a tombstone.
		want := []string{"n2", "t1"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("children = %v, want %v", got, want)
		}
		if len(state.Detached()) != 0 {
			t.Errorf("detached = %v, want nothing adrift", state.Detached())
		}
	})
}

func TestHealthNeedsTheDatabase(t *testing.T) {
	c := stand(t)
	if status := c.do(http.MethodGet, "/v1/health", http.StatusOK, nil)["status"]; status != "ok" {
		t.Errorf("status = %v, want ok", status)
	}
}

func fieldsOf(t *testing.T, kv map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(kv))
	for name, value := range kv {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encoding field %q: %v", name, err)
		}
		out[name] = encoded
	}
	return out
}

func fieldOf(t *testing.T, state *ops.State, nodeID, name string) string {
	t.Helper()
	node, ok := state.Node(nodeID)
	if !ok {
		t.Fatalf("node %s is not in the merged state", nodeID)
	}
	value, ok := node.Fields[name]
	if !ok {
		t.Fatalf("node %s has no field %q", nodeID, name)
	}
	return string(value)
}

// remarshal moves a decoded JSON value into a typed one, which is how a client
// gets from the response body to an op it can merge.
func remarshal(t *testing.T, from any, into any) {
	t.Helper()
	encoded, err := json.Marshal(from)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	if err := json.Unmarshal(encoded, into); err != nil {
		t.Fatalf("decoding: %v", err)
	}
}
