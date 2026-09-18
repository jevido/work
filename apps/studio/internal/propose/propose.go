// Package propose is the one tool Claude is given when it is asked to
// restructure an outline.
//
// It is an MCP server, spoken over stdio, hosted by the desktop binary itself
// -- `work mcp`. The `claude` CLI is pointed at it with --mcp-config, and the
// tool it declares shows up on the model's side as
// mcp__work__propose_restructure.
//
// The tool does nothing. That is not an oversight: the app is already reading
// the stream this call arrives on, and a tool_use block carries its own input,
// so the proposal reaches the review panel by being *said* rather than by
// being returned. See internal/claude/claude.go, which forwards the tool name
// and its input as an event, and frontend/src/lib/workspace/review.svelte.ts,
// which has been waiting for it. What this server is for is making the call
// legal and giving the model a schema to fill in.
//
// Nothing here touches the network, and nothing here may. Claude runs on the
// desktop against the user's own signed-in CLI, which is the whole reason the
// sync server needs no credentials and why a restructuring works with the
// network down.
package propose

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// ServerName is this server's name in the MCP config, and therefore the
	// middle of the tool name the model calls. Changing it changes
	// FullToolName, which the frontend matches on exactly.
	ServerName = "work"
	// ToolName is the tool as this server declares it.
	ToolName = "propose_restructure"
)

// FullToolName is how the tool arrives on the Claude stream: an MCP tool is
// namespaced by the server that offers it. This is the string the frontend
// compares against, and a mismatch is silent -- the proposal is simply dropped
// -- so it is derived here rather than written out twice.
const FullToolName = "mcp__" + ServerName + "__" + ToolName

// Limits, mirrored from frontend/src/lib/workspace/proposal.ts. They are in the
// schema as well as in the parser because a limit the model is told is a limit
// it can respect, and one it is not told is a refusal after the work is done.
const (
	// MaxOps is the most ops one proposal may carry.
	//
	// Five hundred rather than the two hundred this started at, because of
	// "replace": laying out a board of thirty notes with its links, its
	// regions, its glyphs and its captions is a couple of hundred operations,
	// and a limit that refuses a whole board is a limit that deletes the
	// feature. The cap is still here for the reason it always was -- a review
	// list nobody can read through is not a review -- and the review panel
	// pages rather than scrolling forever.
	MaxOps = 500
	// MaxTextLen is the longest line a proposal may write. A review limit,
	// not a protocol one: a row nobody can read is a row nobody can approve.
	MaxTextLen = 2000
	// MaxDetailLen is the longest body a proposal may write onto a card.
	//
	// Larger than MaxTextLen because it is a different thing: a title is a
	// line and a detail is the paragraph that did not fit on it. Still bounded,
	// and for the same reason -- a review row nobody reads to the end is a row
	// nobody has actually approved.
	MaxDetailLen = 8000
	// MaxIDLen matches the op envelope's cap on identifiers.
	MaxIDLen = 128
)

// defaultProtocol is answered when the client asks for a version this server
// has never heard of. Negotiation is otherwise an echo: a client that asks for
// a version in knownProtocols gets that version back, which is what keeps this
// working across CLI upgrades without a change here.
const defaultProtocol = "2025-06-18"

var knownProtocols = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// maxLine bounds one JSON-RPC message. A tools/call carries the whole proposal
// -- two hundred ops of two thousand characters is most of half a megabyte --
// so this is sized for the largest legal call and then some, rather than for a
// typical one.
const maxLine = 8 << 20

// Serve reads JSON-RPC messages from in and writes responses to out until in
// is exhausted or ctx is cancelled.
//
// Three methods and nothing else. Written by hand rather than against an SDK:
// this is under two hundred lines of protocol, and a dependency here is a
// dependency in the desktop binary every user installs.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	lines := bufio.NewScanner(in)
	lines.Buffer(make([]byte, 0, 64*1024), maxLine)

	encoder := json.NewEncoder(out)

	for lines.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := lines.Bytes()
		if len(line) == 0 {
			continue
		}

		response, send := handle(line)
		if !send {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("propose: writing a response: %w", err)
		}
	}

	if err := lines.Err(); err != nil {
		// A message over the cap is not something to recover from: the rest of
		// the stream is the tail of a message nobody can frame.
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("propose: a message was over the %d byte limit", maxLine)
		}
		return fmt.Errorf("propose: reading: %w", err)
	}
	return nil
}

// request is one incoming message. ID is kept raw because JSON-RPC allows a
// string or a number and the answer has to be the same one that was asked
// with, not this server's idea of it.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC's own codes. Only the three that can happen here.
const (
	codeParse          = -32700
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
)

// handle turns one message into at most one response.
//
// The bool is not decoration. A JSON-RPC notification -- a message with no id,
// which is how every MCP client sends notifications/initialized -- must not be
// answered at all. Replying to one is a protocol violation, and a client that
// is strict about it disconnects; which is why this deviates from "anything
// else gets a method-not-found" and stays silent for the id-less case.
func handle(line []byte) (response, bool) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		// No id to answer with, so this is the one error that goes out
		// unaddressed. A client that sent garbage gets told rather than
		// waiting on a reply that is never coming.
		return fail(nil, codeParse, "message is not valid JSON"), true
	}
	if len(req.ID) == 0 {
		return response{}, false
	}

	switch req.Method {
	case "initialize":
		return ok(req.ID, initializeResult(req.Params)), true
	case "tools/list":
		return ok(req.ID, toolsListResult()), true
	case "tools/call":
		return callTool(req)
	default:
		return fail(req.ID, codeMethodNotFound, "no such method: "+req.Method), true
	}
}

func ok(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func fail(id json.RawMessage, code int, message string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func initializeResult(params json.RawMessage) map[string]any {
	asked := defaultProtocol
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &p); err == nil && knownProtocols[p.ProtocolVersion] {
			asked = p.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": asked,
		// Tools and nothing else. No resources, no prompts, no sampling: a
		// server that advertises a capability it does not have is a client
		// waiting on a call that errors.
		"capabilities": map[string]any{"tools": map[string]any{}},
		"serverInfo":   map[string]any{"name": ServerName, "version": "1"},
	}
}

func toolsListResult() map[string]any {
	return map[string]any{
		"tools": []any{map[string]any{
			"name":        ToolName,
			"description": toolDescription,
			"inputSchema": json.RawMessage(inputSchema),
		}},
	}
}

func callTool(req request) (response, bool) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return fail(req.ID, codeInvalidParams, "params are not an object"), true
	}
	if p.Name != ToolName {
		return fail(req.ID, codeInvalidParams, "no such tool: "+p.Name), true
	}

	// The arguments are not read here, and not validated here either. They are
	// already on their way to the app as part of the tool_use block, and
	// frontend/src/lib/workspace/proposal.ts is where a proposal is checked --
	// one parser, in the place that has the document to check it against.
	// Refusing here as well would mean two rules that have to agree.
	return ok(req.ID, map[string]any{
		"content": []any{map[string]any{
			"type": "text",
			"text": "Proposal delivered for review. The person decides what applies; nothing has changed yet.",
		}},
	}), true
}

// toolDescription is read by the model at call time, and is the only place the
// rules exist when it matters. Everything in it is a rule that is enforced
// elsewhere and cheaper to follow than to have refused.
const toolDescription = `Propose a restructuring of the current outline or plan for a person to review.

Answer with operations over the nodes you were shown, never with a rewritten outline: a proposal is reviewed and approved one row at a time, and a whole-outline rewrite can be neither.

The idea view draws this document as a board: a subject in the middle, a handful of clusters around it, notes hanging off each of them, and links between lines the tree cannot connect. Depth is what makes something a cluster head: a root line is a head, its children are the notes on it. Keep boards shallow and wide — four to seven heads reads as thinking, fifteen reads as a list.

Rules:
- Name only ids that appear in the state you were given. An id you did not see does not exist, and a proposal naming one is refused whole.
- Say nothing about position. Where a line sits is decided by the app when the change is applied. Use "after" to name the sibling something goes below, or null for first. Never send a position, index, order, children or tree — a proposal carrying any of those is refused whole.
- One call carries the entire proposal. Do not call this tool more than once for one request.
- "summary" is one sentence a person reads before the rows: what you changed and why.

Operations:
- set-text: rewrite the text of an existing node.
- insert: add a new line under "parent" (omit or "" for a root), below "after". Give it a "ref" if later operations in the same proposal need to name it — refs are local to the proposal and never reach the document.
- move: re-parent a node and place it below "after".
- delete: remove a node. Its descendants go with it.
- promote: add an existing idea to the plan as a task, keeping the link back to the idea.
- set-status: mark a task todo, doing or done.
- link: draw a link between two lines that are not parent and child. This is how the map says two ideas relate when the tree cannot, because neither is under the other.
- unlink: remove a link between two lines.
- group: gather a line into a region, which is a named set. Give "region" the id of a region that exists, or "name" to make a new one. A line is in one region at a time.
- set-icon: put a glyph on a cluster head, to make it findable at a glance. Only on root lines, and only where it says something: a glyph on every line is the same as none. Send "" to take one off.
- caption: write why two lines are linked, onto the link itself. A few words, not a sentence — it is drawn along the line.
- set-detail: write the body of a card -- what it is, at length. The board draws only the title; this is what a person sees when they open the card. Newlines are allowed here and nowhere else. Send "" to clear it.
- guide: put a card under one of the workspace's guidelines, by the id listed above the outline. Only ids that are listed: you cannot invent a guideline, and a card that needs a word nobody has set up is worth saying so in your answer instead.
- unguide: take a card out from under a guideline.
- interest: record that one of the people or groups listed above is waiting on a card.
- uninterest: record that they are not.
- replace: start the board again. Everything on it now is moved under one collapsed line named after today and left there, so nothing is lost and the person can take it back. Use it when somebody dumps a set of raw ideas and asks for a board out of them; do not use it to tidy a branch, reorganise part of a board, or add to one — those are the operations above. Send it first, before the inserts that build the new board.`

// inputSchema mirrors ProposedOp in frontend/src/lib/workspace/proposal.ts. The
// two are checked against each other by a test in that file's own suite and by
// TestSchemaMatchesTheParser here; if a kind is added on one side it has to be
// added on the other, and the failing test is how that is found out.
const inputSchema = `{
  "type": "object",
  "required": ["summary", "ops"],
  "additionalProperties": false,
  "properties": {
    "summary": {
      "type": "string",
      "maxLength": 2000,
      "description": "One sentence: what this proposal changes and why."
    },
    "ops": {
      "type": "array",
      "minItems": 1,
      "maxItems": 500,
      "description": "The operations, in the order they should be applied.",
      "items": {
        "oneOf": [
          {
            "type": "object",
            "required": ["kind", "node", "text"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "set-text"},
              "node": {"type": "string", "maxLength": 128},
              "text": {"type": "string", "maxLength": 2000}
            }
          },
          {
            "type": "object",
            "required": ["kind", "text"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "insert"},
              "ref": {"type": "string", "maxLength": 128, "description": "A name for this new line, so later ops can put things under it. Local to the proposal."},
              "parent": {"type": "string", "maxLength": 128, "description": "Empty or omitted makes it a root."},
              "after": {"type": ["string", "null"], "maxLength": 128, "description": "The sibling it goes below, or null for first."},
              "text": {"type": "string", "maxLength": 2000}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "move"},
              "node": {"type": "string", "maxLength": 128},
              "parent": {"type": "string", "maxLength": 128},
              "after": {"type": ["string", "null"], "maxLength": 128}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "delete"},
              "node": {"type": "string", "maxLength": 128}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "promote"},
              "node": {"type": "string", "maxLength": 128}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "status"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "set-status"},
              "node": {"type": "string", "maxLength": 128},
              "status": {"enum": ["todo", "doing", "done"]}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "other"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "link"},
              "node": {"type": "string", "maxLength": 128},
              "other": {"type": "string", "maxLength": 128, "description": "The line at the other end. Not a parent or a child of node -- the tree already says that."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "other"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "unlink"},
              "node": {"type": "string", "maxLength": 128},
              "other": {"type": "string", "maxLength": 128}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "group"},
              "node": {"type": "string", "maxLength": 128},
              "region": {"type": "string", "maxLength": 128, "description": "A region that already exists, to add this line to."},
              "name": {"type": "string", "maxLength": 2000, "description": "A name for a new region, when there is no region to name."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "icon"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "set-icon"},
              "node": {"type": "string", "maxLength": 128},
              "icon": {"enum": ["", "people", "heart", "trophy", "signpost", "warning", "check", "bulb", "flag", "question", "timing"], "description": "The glyph, by name. \"\" takes one off."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "other", "text"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "caption"},
              "node": {"type": "string", "maxLength": 128},
              "other": {"type": "string", "maxLength": 128, "description": "The line at the other end of the link being captioned."},
              "text": {"type": "string", "maxLength": 2000, "description": "A few words. It is drawn along the line."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "text"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "set-detail"},
              "node": {"type": "string", "maxLength": 128},
              "text": {"type": "string", "maxLength": 8000, "description": "The body of the card. Newlines allowed. \"\" clears it."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "guideline"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "guide"},
              "node": {"type": "string", "maxLength": 128},
              "guideline": {"type": "string", "maxLength": 128, "description": "A guideline id from the list above the outline. Not a name, and not one you made up."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "guideline"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "unguide"},
              "node": {"type": "string", "maxLength": 128},
              "guideline": {"type": "string", "maxLength": 128}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "party"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "interest"},
              "node": {"type": "string", "maxLength": 128},
              "party": {"type": "string", "maxLength": 128, "description": "A party id from the list above the outline."}
            }
          },
          {
            "type": "object",
            "required": ["kind", "node", "party"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "uninterest"},
              "node": {"type": "string", "maxLength": 128},
              "party": {"type": "string", "maxLength": 128}
            }
          },
          {
            "type": "object",
            "required": ["kind", "reason"],
            "additionalProperties": false,
            "properties": {
              "kind": {"const": "replace"},
              "reason": {"type": "string", "maxLength": 2000, "description": "One line: why the board is being started again. The person reads this before approving it."}
            }
          }
        ]
      }
    }
  }
}`

// ConfigFile is the name WriteConfig gives the file it writes.
const ConfigFile = "mcp.json"

// WriteConfig writes an MCP config into dir pointing the CLI at this binary,
// and returns the path to pass as --mcp-config.
//
// os.Executable rather than os.Args[0]: the latter is whatever the shell said,
// which is a relative path as often as not, and the CLI resolves the command
// from its own working directory rather than from ours.
//
// The server key is [ServerName], which is what makes the tool arrive as
// [FullToolName]. The three have to agree, and they agree here because two of
// them are derived from the third.
func WriteConfig(dir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("propose: finding this binary: %w", err)
	}

	body, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			ServerName: map[string]any{
				"command": self,
				"args":    []string{"mcp"},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("propose: encoding the config: %w", err)
	}

	path := filepath.Join(dir, ConfigFile)
	// 0600 out of habit rather than necessity: there is no secret in here, but
	// a file naming an executable the CLI will run is not one to leave
	// writable by anything else on the machine.
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("propose: writing %s: %w", path, err)
	}
	return path, nil
}
