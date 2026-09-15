package propose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// converse drives Serve with the given messages and returns what it wrote back,
// decoded. A response per line, in order, and never more than one per request.
func converse(t *testing.T, messages ...string) []map[string]any {
	t.Helper()

	var out strings.Builder
	in := strings.NewReader(strings.Join(messages, "\n") + "\n")
	if err := Serve(t.Context(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var decoded []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not JSON: %q: %v", line, err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Errorf("jsonrpc = %v, want 2.0", m["jsonrpc"])
		}
		decoded = append(decoded, m)
	}
	return decoded
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if e, bad := m["error"]; bad {
		t.Fatalf("wanted a result, got error %v", e)
	}
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want an object", m["result"])
	}
	return r
}

func failure(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("wanted an error, got result %v", m["result"])
	}
	return e
}

func TestInitialize(t *testing.T) {
	t.Run("the client's version is echoed when it is one we know", func(t *testing.T) {
		// Echoing rather than insisting is what keeps this working across CLI
		// upgrades without a change here.
		for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18"} {
			got := converse(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{}}}`)
			if len(got) != 1 {
				t.Fatalf("got %d responses, want 1", len(got))
			}
			r := result(t, got[0])
			if r["protocolVersion"] != version {
				t.Errorf("protocolVersion = %v, want %v", r["protocolVersion"], version)
			}
		}
	})

	t.Run("a version we do not know gets ours", func(t *testing.T) {
		got := converse(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
		r := result(t, got[0])
		if r["protocolVersion"] != defaultProtocol {
			t.Errorf("protocolVersion = %v, want %v", r["protocolVersion"], defaultProtocol)
		}
	})

	t.Run("tools, and nothing this server has not got", func(t *testing.T) {
		got := converse(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
		r := result(t, got[0])

		caps, ok := r["capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("capabilities is %T, want an object", r["capabilities"])
		}
		if _, has := caps["tools"]; !has {
			t.Error("capabilities does not include tools")
		}
		// Advertising a capability this server has not got is a client waiting
		// on a call that errors.
		for _, absent := range []string{"resources", "prompts", "sampling"} {
			if _, has := caps[absent]; has {
				t.Errorf("capabilities claims %q, which this server does not serve", absent)
			}
		}

		info, ok := r["serverInfo"].(map[string]any)
		if !ok {
			t.Fatalf("serverInfo is %T, want an object", r["serverInfo"])
		}
		// The name is the middle of FullToolName, which the frontend matches
		// on exactly.
		if info["name"] != ServerName {
			t.Errorf("serverInfo.name = %v, want %v", info["name"], ServerName)
		}
	})
}

func TestToolsList(t *testing.T) {
	got := converse(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
	r := result(t, got[0])

	tools, ok := r["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want exactly one", r["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("the tool is %T, want an object", tools[0])
	}
	if tool["name"] != ToolName {
		t.Errorf("name = %v, want %v", tool["name"], ToolName)
	}
	if _, ok := tool["inputSchema"].(map[string]any); !ok {
		t.Fatalf("inputSchema is %T, want an object", tool["inputSchema"])
	}

	// The description is read by the model at call time and is the only place
	// the rules exist when it matters. These are the ones that are enforced
	// elsewhere and cheaper to follow than to have refused.
	description, _ := tool["description"].(string)
	for _, must := range []string{"after", "position", "refused whole", "summary"} {
		if !strings.Contains(description, must) {
			t.Errorf("the description does not mention %q", must)
		}
	}
}

// The schema is the contract with proposal.ts. If a kind is added on one side
// and not the other, a proposal either cannot be expressed or cannot be read.
func TestSchemaMatchesTheParser(t *testing.T) {
	var schema struct {
		Type       string `json:"type"`
		Required   []string
		Properties struct {
			Ops struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					OneOf []struct {
						Required   []string
						Properties map[string]json.RawMessage
					} `json:"oneOf"`
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(inputSchema), &schema); err != nil {
		t.Fatalf("the schema is not valid JSON: %v", err)
	}

	if schema.Type != "object" {
		t.Errorf("type = %q, want object", schema.Type)
	}
	if schema.Properties.Ops.MaxItems != MaxOps {
		t.Errorf("ops maxItems = %d, want %d", schema.Properties.Ops.MaxItems, MaxOps)
	}

	// Every kind proposal.ts can read, and no kind it cannot.
	want := map[string]bool{
		"set-text": false, "insert": false, "move": false,
		"delete": false, "promote": false, "set-status": false,
	}
	for _, branch := range schema.Properties.Ops.Items.OneOf {
		raw, has := branch.Properties["kind"]
		if !has {
			t.Fatal("a branch of the schema has no kind")
		}
		var k struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(raw, &k); err != nil {
			t.Fatalf("a branch's kind is not a const: %v", err)
		}
		seen, known := want[k.Const]
		if !known {
			t.Errorf("the schema allows kind %q, which proposal.ts cannot read", k.Const)
			continue
		}
		if seen {
			t.Errorf("kind %q appears twice", k.Const)
		}
		want[k.Const] = true
	}
	for kind, seen := range want {
		if !seen {
			t.Errorf("the schema has no branch for kind %q", kind)
		}
	}

	// A proposal that places things itself is refused by proposal.ts whole, so
	// the schema must not invite one. additionalProperties is false on every
	// branch, which is what makes that true without naming each forbidden key.
	for _, forbidden := range []string{"position", "children", "tree", "index", "order", "clock", "actor"} {
		if strings.Contains(inputSchema, `"`+forbidden+`"`) {
			t.Errorf("the schema mentions %q, which a proposal may not carry", forbidden)
		}
	}
}

func TestToolsCall(t *testing.T) {
	t.Run("the tool answers, and says nothing has changed", func(t *testing.T) {
		got := converse(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"`+ToolName+`","arguments":{"summary":"tidy","ops":[{"kind":"delete","node":"n1"}]}}}`)
		r := result(t, got[0])
		content, ok := r["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("content = %v, want at least one block", r["content"])
		}
		first, _ := content[0].(map[string]any)
		text, _ := first["text"].(string)
		// The model has to know this is not an application: it is a proposal
		// waiting for a person, and saying otherwise invites a second call.
		if !strings.Contains(text, "review") {
			t.Errorf("the answer is %q, which does not say it is waiting for review", text)
		}
	})

	t.Run("an unknown tool is refused by name", func(t *testing.T) {
		got := converse(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`)
		e := failure(t, got[0])
		if e["code"] != float64(codeInvalidParams) {
			t.Errorf("code = %v, want %d", e["code"], codeInvalidParams)
		}
		if msg, _ := e["message"].(string); !strings.Contains(msg, "rm_rf") {
			t.Errorf("message = %q, want it to name the tool asked for", msg)
		}
	})

	t.Run("arguments are not validated here", func(t *testing.T) {
		// On purpose. proposal.ts is the one parser, in the place that has the
		// document to check ids against; a second set of rules here would be a
		// second set of rules to keep in agreement.
		got := converse(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"`+ToolName+`","arguments":{"nonsense":true}}}`)
		result(t, got[0])
	})
}

func TestUnknownMethod(t *testing.T) {
	got := converse(t, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	e := failure(t, got[0])
	if e["code"] != float64(codeMethodNotFound) {
		t.Errorf("code = %v, want %d", e["code"], codeMethodNotFound)
	}
}

// A notification has no id, and JSON-RPC forbids answering one. Every MCP
// client sends notifications/initialized; a server that replies to it is a
// server a strict client disconnects from.
func TestNotificationsAreNotAnswered(t *testing.T) {
	got := converse(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`,
	)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1 -- notifications must not be answered", len(got))
	}
	if got[0]["id"] != float64(7) {
		t.Errorf("the one response is for id %v, want 7", got[0]["id"])
	}
}

func TestMalformedLine(t *testing.T) {
	// An error response, not a crash and not silence: a client that sent
	// garbage should be told rather than left waiting on a reply.
	got := converse(t, `{not json at all`, `{"jsonrpc":"2.0","id":8,"method":"tools/list"}`)
	if len(got) != 2 {
		t.Fatalf("got %d responses, want 2", len(got))
	}
	e := failure(t, got[0])
	if e["code"] != float64(codeParse) {
		t.Errorf("code = %v, want %d", e["code"], codeParse)
	}
	result(t, got[1])
}

func TestBlankLinesAreSkipped(t *testing.T) {
	got := converse(t, ``, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`, ``)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
}

// The id is answered with exactly what was asked with. JSON-RPC allows a string
// or a number, and a client that asked with "a" and is answered with 0 cannot
// match the two up.
func TestIDIsEchoedInItsOwnType(t *testing.T) {
	got := converse(t, `{"jsonrpc":"2.0","id":"a-string","method":"tools/list"}`)
	if got[0]["id"] != "a-string" {
		t.Errorf("id = %#v, want the string it was asked with", got[0]["id"])
	}
}

func TestFullToolNameIsDerived(t *testing.T) {
	// The frontend compares against this exact string, and a mismatch is
	// silent -- the proposal is dropped without a word.
	if FullToolName != "mcp__work__propose_restructure" {
		t.Errorf("FullToolName = %q", FullToolName)
	}
}

func TestCancelledContextStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out strings.Builder
	err := Serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	if err == nil {
		t.Error("Serve returned nil for a cancelled context")
	}
}

func TestWriteConfig(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteConfig(dir)
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	var config struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatalf("the config is not valid JSON: %v", err)
	}

	server, named := config.MCPServers[ServerName]
	if !named {
		// The key is what makes the tool arrive as FullToolName. A different
		// one here is every proposal dropped, silently.
		t.Fatalf("no server named %q; got %v", ServerName, config.MCPServers)
	}
	if len(server.Args) != 1 || server.Args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", server.Args)
	}

	// An absolute path, because the CLI resolves the command from its own
	// working directory rather than from ours.
	if !filepath.IsAbs(server.Command) {
		t.Errorf("command = %q, which is not absolute", server.Command)
	}
	if _, err := os.Stat(server.Command); err != nil {
		t.Errorf("command %q is not there: %v", server.Command, err)
	}
}
