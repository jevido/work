package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dev.jevido/work/internal/ops"
)

// The signup token has to clear the length the config insists on, so tests use
// one a real deployment could.
const testSignupToken = "a-signup-token-long-enough-to-be-allowed"

// serve stands the API up over a fake store.
func serve(t *testing.T, signupToken string) (*httptest.Server, *fakeStore) {
	t.Helper()
	backing := newFakeStore()
	// Discard the logs: a test that fails should fail on an assertion, not by
	// burying it in request lines.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(backing, signupToken, logger).Handler())
	t.Cleanup(srv.Close)
	return srv, backing
}

// call makes a request. An empty key sends no Authorization header at all,
// which is a different case from sending a bad one.
func call(t *testing.T, srv *httptest.Server, method, path, key string, body any) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, ok := body.(string)
		if !ok {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("encoding the request body: %v", err)
			}
			encoded = string(raw)
		}
		reader = strings.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// body decodes a response into a map, so assertions are about the JSON a client
// receives rather than about a Go struct that might not marshal to it.
func body(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	return out
}

// expectStatus asserts the status and, for a failure, the error code with it.
func expectStatus(t *testing.T, resp *http.Response, status int, code string) map[string]any {
	t.Helper()
	decoded := body(t, resp)
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d; body %v", resp.StatusCode, status, decoded)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if code == "" {
		return decoded
	}
	failure, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object in %v", decoded)
	}
	if failure["code"] != code {
		t.Errorf("error code = %v, want %q", failure["code"], code)
	}
	if message, _ := failure["message"].(string); message == "" {
		t.Error("the error carries no message")
	}
	return decoded
}

func anOp(id string) ops.Op {
	return ops.Op{
		ID: id, Kind: ops.KindSetFields, Actor: "desktop-test", Clock: 1, Node: "n",
		Fields: map[string]json.RawMessage{"title": json.RawMessage(`"x"`)},
	}
}

func TestHealth(t *testing.T) {
	srv, backing := serve(t, "")

	decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/health", "", nil), http.StatusOK, "")
	if decoded["status"] != "ok" {
		t.Errorf("status = %v, want ok", decoded["status"])
	}

	t.Run("goes red when the database does", func(t *testing.T) {
		backing.pingErr = errBroken
		expectStatus(t, call(t, srv, http.MethodGet, "/v1/health", "", nil), http.StatusInternalServerError, "internal")
	})
}

func TestRoutingFailures(t *testing.T) {
	srv, _ := serve(t, "")

	t.Run("an unknown path is a JSON 404", func(t *testing.T) {
		expectStatus(t, call(t, srv, http.MethodGet, "/v1/nope", "", nil), http.StatusNotFound, "not_found")
	})
	t.Run("the wrong method is a JSON 405", func(t *testing.T) {
		for _, path := range []string{"/v1/health", "/v1/workspaces", "/v1/workspace", "/v1/ops", "/v1/document"} {
			resp := call(t, srv, http.MethodDelete, path, "", nil)
			expectStatus(t, resp, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	})
}

func TestCreateWorkspace(t *testing.T) {
	t.Run("closed when no signup token is configured", func(t *testing.T) {
		srv, backing := serve(t, "")
		resp := call(t, srv, http.MethodPost, "/v1/workspaces", "anything", map[string]any{"name": "x"})
		expectStatus(t, resp, http.StatusForbidden, "forbidden")
		if backing.created != 0 {
			t.Error("a workspace was created anyway")
		}
	})

	t.Run("the wrong token is unauthorized", func(t *testing.T) {
		srv, _ := serve(t, testSignupToken)
		for _, key := range []string{"", "wrong", testSignupToken + "x"} {
			resp := call(t, srv, http.MethodPost, "/v1/workspaces", key, map[string]any{"name": "x"})
			expectStatus(t, resp, http.StatusUnauthorized, "unauthorized")
		}
	})

	t.Run("the name is checked", func(t *testing.T) {
		srv, _ := serve(t, testSignupToken)
		for _, name := range []any{"", "   ", strings.Repeat("n", MaxWorkspaceNameLen+1)} {
			resp := call(t, srv, http.MethodPost, "/v1/workspaces", testSignupToken, map[string]any{"name": name})
			expectStatus(t, resp, http.StatusBadRequest, "bad_request")
		}
	})

	t.Run("the keys come back once", func(t *testing.T) {
		srv, _ := serve(t, testSignupToken)
		resp := call(t, srv, http.MethodPost, "/v1/workspaces", testSignupToken, map[string]any{"name": "jevido/work"})
		decoded := expectStatus(t, resp, http.StatusCreated, "")

		workspace, ok := decoded["workspace"].(map[string]any)
		if !ok {
			t.Fatalf("no workspace in %v", decoded)
		}
		for _, key := range []string{"id", "name", "head", "createdAt"} {
			if _, ok := workspace[key]; !ok {
				t.Errorf("the workspace has no %q: %v", key, workspace)
			}
		}
		if workspace["name"] != "jevido/work" {
			t.Errorf("name = %v, want jevido/work", workspace["name"])
		}
		if workspace["head"] != float64(0) {
			t.Errorf("head = %v, want 0 for a fresh workspace", workspace["head"])
		}
		writeKey, _ := decoded["writeKey"].(string)
		readKey, _ := decoded["readKey"].(string)
		if !strings.HasPrefix(writeKey, "wk_") || !strings.HasPrefix(readKey, "rk_") {
			t.Errorf("keys = %q and %q, want wk_ and rk_ prefixes", writeKey, readKey)
		}
	})
}

func TestKeysDecideWhatAnEndpointDoes(t *testing.T) {
	srv, backing := serve(t, "")
	writeKey, readKey := backing.withWorkspace("ws_one")

	t.Run("no key at all", func(t *testing.T) {
		expectStatus(t, call(t, srv, http.MethodGet, "/v1/workspace", "", nil), http.StatusUnauthorized, "unauthorized")
	})

	t.Run("a key nobody issued", func(t *testing.T) {
		for _, key := range []string{"nonsense", "wk_short", "rk_" + strings.Repeat("f", 32)} {
			resp := call(t, srv, http.MethodGet, "/v1/workspace", key, nil)
			expectStatus(t, resp, http.StatusUnauthorized, "unauthorized")
		}
	})

	t.Run("a read key reads", func(t *testing.T) {
		decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/workspace", readKey, nil), http.StatusOK, "")
		if decoded["access"] != "read" {
			t.Errorf("access = %v, want read", decoded["access"])
		}
	})

	t.Run("a write key says so", func(t *testing.T) {
		decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/workspace", writeKey, nil), http.StatusOK, "")
		if decoded["access"] != "write" {
			t.Errorf("access = %v, want write", decoded["access"])
		}
	})

	t.Run("a read key may not append", func(t *testing.T) {
		resp := call(t, srv, http.MethodPost, "/v1/ops", readKey, map[string]any{"ops": []ops.Op{anOp("o1")}})
		// A 403 rather than a 401: the key is real, and retrying with it will
		// never work, so a viewer should stop rather than re-authenticate.
		expectStatus(t, resp, http.StatusForbidden, "forbidden")
	})

	t.Run("a write key may read", func(t *testing.T) {
		expectStatus(t, call(t, srv, http.MethodGet, "/v1/ops", writeKey, nil), http.StatusOK, "")
	})
}

func TestAppendOps(t *testing.T) {
	srv, backing := serve(t, "")
	writeKey, _ := backing.withWorkspace("ws_one")

	t.Run("bodies that are not ops", func(t *testing.T) {
		cases := map[string]struct {
			body   any
			status int
			code   string
		}{
			"not JSON":      {"{oops", http.StatusBadRequest, "bad_request"},
			"no ops":        {map[string]any{"ops": []ops.Op{}}, http.StatusBadRequest, "bad_request"},
			"missing field": {map[string]any{}, http.StatusBadRequest, "bad_request"},
			"over the body limit": {
				`{"ops":[{"id":"x","fields":{"a":"` + strings.Repeat("p", MaxBodyBytes) + `"}}]}`,
				http.StatusRequestEntityTooLarge, "too_large",
			},
		}
		for name, tt := range cases {
			t.Run(name, func(t *testing.T) {
				expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey, tt.body), tt.status, tt.code)
			})
		}
	})

	t.Run("too many ops in one request", func(t *testing.T) {
		list := make([]ops.Op, MaxOpsPerRequest+1)
		for i := range list {
			list[i] = anOp(fmt.Sprintf("many-%d", i))
		}
		resp := call(t, srv, http.MethodPost, "/v1/ops", writeKey, map[string]any{"ops": list})
		expectStatus(t, resp, http.StatusBadRequest, "bad_request")
	})

	t.Run("an op the merge would refuse is refused here", func(t *testing.T) {
		// The endpoint validates with the same code the desktop merges with, so
		// the log cannot come to hold an op a replica would later reject.
		bad := anOp("bad")
		bad.Clock = 0
		resp := call(t, srv, http.MethodPost, "/v1/ops", writeKey, map[string]any{"ops": []ops.Op{bad}})
		decoded := expectStatus(t, resp, http.StatusBadRequest, "bad_request")
		failure := decoded["error"].(map[string]any)
		if message, _ := failure["message"].(string); !strings.Contains(message, "clock") {
			t.Errorf("message = %q, want it to name the problem", message)
		}
	})

	t.Run("ops land and are numbered", func(t *testing.T) {
		list := []ops.Op{anOp("a1"), anOp("a2")}
		decoded := expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey,
			map[string]any{"ops": list}), http.StatusOK, "")

		if decoded["head"] != float64(2) {
			t.Errorf("head = %v, want 2", decoded["head"])
		}
		accepted, ok := decoded["accepted"].([]any)
		if !ok || len(accepted) != 2 {
			t.Fatalf("accepted = %v, want two entries", decoded["accepted"])
		}
		for i, want := range []float64{1, 2} {
			one := accepted[i].(map[string]any)
			if one["seq"] != want {
				t.Errorf("accepted[%d].seq = %v, want %v", i, one["seq"], want)
			}
			if one["id"] != list[i].ID {
				t.Errorf("accepted[%d].id = %v, want %s", i, one["id"], list[i].ID)
			}
		}
		if duplicates, ok := decoded["duplicates"].([]any); !ok || len(duplicates) != 0 {
			t.Errorf("duplicates = %v, want an empty array and not null", decoded["duplicates"])
		}
	})

	t.Run("a replayed request writes nothing and answers the same", func(t *testing.T) {
		// This is the case a client actually hits: it sent the ops, the
		// response was lost, and it has no way to tell that from a failure.
		list := []ops.Op{anOp("r1"), anOp("r2")}
		first := expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey,
			map[string]any{"ops": list}), http.StatusOK, "")
		second := expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey,
			map[string]any{"ops": list}), http.StatusOK, "")

		if first["head"] != second["head"] {
			t.Errorf("head moved from %v to %v on a replay", first["head"], second["head"])
		}
		if accepted := second["accepted"].([]any); len(accepted) != 0 {
			t.Errorf("accepted = %v on a replay, want nothing written", accepted)
		}
		duplicates := second["duplicates"].([]any)
		if len(duplicates) != 2 {
			t.Fatalf("duplicates = %v, want both ops", duplicates)
		}
		// The sequence numbers reported must be the ones the ops already have,
		// or a client reconciling after a lost response reconciles to the wrong
		// place.
		for i, one := range duplicates {
			was := first["accepted"].([]any)[i].(map[string]any)
			if one.(map[string]any)["seq"] != was["seq"] {
				t.Errorf("duplicate %d reports seq %v, want %v", i, one.(map[string]any)["seq"], was["seq"])
			}
		}
	})

	t.Run("a reused id with different content is a client bug", func(t *testing.T) {
		original := anOp("c1")
		if _, err := json.Marshal(original); err != nil {
			t.Fatal(err)
		}
		expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey,
			map[string]any{"ops": []ops.Op{original}}), http.StatusOK, "")

		impostor := original
		impostor.Fields = map[string]json.RawMessage{"title": json.RawMessage(`"different"`)}
		resp := call(t, srv, http.MethodPost, "/v1/ops", writeKey, map[string]any{"ops": []ops.Op{impostor}})
		expectStatus(t, resp, http.StatusBadRequest, "bad_request")
	})

	t.Run("a store failure is a 500 and says nothing about itself", func(t *testing.T) {
		backing.appendErr = errBroken
		t.Cleanup(func() { backing.appendErr = nil })

		resp := call(t, srv, http.MethodPost, "/v1/ops", writeKey, map[string]any{"ops": []ops.Op{anOp("e1")}})
		decoded := expectStatus(t, resp, http.StatusInternalServerError, "internal")
		if message := decoded["error"].(map[string]any)["message"].(string); strings.Contains(message, "database") {
			t.Errorf("message = %q, want it to leak nothing about the insides", message)
		}
	})
}

func TestReadLog(t *testing.T) {
	srv, backing := serve(t, "")
	writeKey, readKey := backing.withWorkspace("ws_one")

	list := make([]ops.Op, 5)
	for i := range list {
		list[i] = anOp(fmt.Sprintf("l%d", i))
	}
	expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey, map[string]any{"ops": list}), http.StatusOK, "")

	t.Run("an empty log is an empty array", func(t *testing.T) {
		_, otherRead := backing.withWorkspace("ws_two")
		decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/ops", otherRead, nil), http.StatusOK, "")
		if entries, ok := decoded["ops"].([]any); !ok || len(entries) != 0 {
			t.Errorf("ops = %v, want an empty array and not null", decoded["ops"])
		}
		if decoded["more"] != false || decoded["head"] != float64(0) {
			t.Errorf("more = %v and head = %v, want false and 0", decoded["more"], decoded["head"])
		}
	})

	t.Run("the whole log with no parameters", func(t *testing.T) {
		decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/ops", readKey, nil), http.StatusOK, "")
		entries := decoded["ops"].([]any)
		if len(entries) != len(list) {
			t.Fatalf("got %d ops, want %d", len(entries), len(list))
		}
		if decoded["more"] != false {
			t.Error("more is true with the whole log returned")
		}
		first := entries[0].(map[string]any)
		for _, key := range []string{"seq", "receivedAt", "op"} {
			if _, ok := first[key]; !ok {
				t.Errorf("an entry has no %q: %v", key, first)
			}
		}
		// The op comes back as it went in, because a viewer replays it through
		// the same merge the desktop does.
		carried := first["op"].(map[string]any)
		if carried["id"] != "l0" || carried["kind"] != string(ops.KindSetFields) {
			t.Errorf("op = %v, want the op that was sent", carried)
		}
	})

	t.Run("paging by sequence number", func(t *testing.T) {
		var since float64
		seen := 0
		for range len(list) {
			decoded := expectStatus(t, call(t, srv, http.MethodGet,
				fmt.Sprintf("/v1/ops?since=%d&limit=2", int(since)), readKey, nil), http.StatusOK, "")
			entries := decoded["ops"].([]any)
			seen += len(entries)
			if len(entries) > 0 {
				since = entries[len(entries)-1].(map[string]any)["seq"].(float64)
			}
			if decoded["more"] == false {
				// Gaplessness is what lets a caller stop here: the last seq it
				// holds is the head, so there is nothing it could be missing.
				if since != decoded["head"] {
					t.Errorf("more is false at seq %v with head %v", since, decoded["head"])
				}
				break
			}
		}
		if seen != len(list) {
			t.Errorf("paged through %d ops, want %d", seen, len(list))
		}
	})

	t.Run("since past the end", func(t *testing.T) {
		decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/ops?since=9999", readKey, nil), http.StatusOK, "")
		if len(decoded["ops"].([]any)) != 0 || decoded["more"] != false {
			t.Errorf("got %v, want nothing and more false", decoded)
		}
	})

	t.Run("parameters that are not numbers", func(t *testing.T) {
		for _, query := range []string{"?since=abc", "?since=-1", "?limit=0", "?limit=-3", "?limit=x"} {
			resp := call(t, srv, http.MethodGet, "/v1/ops"+query, readKey, nil)
			expectStatus(t, resp, http.StatusBadRequest, "bad_request")
		}
	})

	t.Run("a limit over the cap is capped, not refused", func(t *testing.T) {
		decoded := expectStatus(t, call(t, srv, http.MethodGet, "/v1/ops?limit=1000000", readKey, nil), http.StatusOK, "")
		if len(decoded["ops"].([]any)) != len(list) {
			t.Errorf("got %d ops, want all %d", len(decoded["ops"].([]any)), len(list))
		}
	})
}

func TestRateLimit(t *testing.T) {
	srv, backing := serve(t, "")
	_, readKey := backing.withWorkspace("ws_one")

	// Spend the burst, then find the refusal. The budget is generous enough
	// that no honest client meets it, so this has to go looking for it.
	var limited *http.Response
	for range burst + perSecond + 10 {
		resp := call(t, srv, http.MethodGet, "/v1/workspace", readKey, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = resp
			break
		}
	}
	if limited == nil {
		t.Fatalf("never rate-limited after %d requests", burst+perSecond+10)
	}
	expectStatus(t, limited, http.StatusTooManyRequests, "rate_limited")
	if limited.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After on a 429, so a client has nothing to wait for")
	}

	t.Run("health is never limited", func(t *testing.T) {
		// The budget for this caller is spent. Health still has to answer, or
		// the check goes red exactly when the server is busiest.
		expectStatus(t, call(t, srv, http.MethodGet, "/v1/health", "", nil), http.StatusOK, "")
	})

	t.Run("one caller cannot spend another's budget", func(t *testing.T) {
		_, otherKey := backing.withWorkspace("ws_two")
		expectStatus(t, call(t, srv, http.MethodGet, "/v1/workspace", otherKey, nil), http.StatusOK, "")
	})
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	// A newer client talking to an older server happens on every deploy, and
	// must not be a 400.
	srv, backing := serve(t, "")
	writeKey, _ := backing.withWorkspace("ws_one")

	raw := `{"ops":[{"id":"u1","kind":"set-fields","actor":"a","clock":1,"node":"n",
		"fields":{"title":"x"},"somethingNewer":{"nested":true}}],"alsoNewer":42}`
	expectStatus(t, call(t, srv, http.MethodPost, "/v1/ops", writeKey, raw), http.StatusOK, "")
}
