package claude

import (
	"reflect"
	"strings"
	"testing"
)

// TestScanStreamParsesRealCLIShape uses lines in the exact shape the Claude CLI
// emits, including the noise cases: a version-manager banner on the first line,
// a hook payload, a partial-message delta, and a trailing result.
//
// The partial stream announces a tool call before its arguments exist, so no
// event comes from that; tool calls are read from the complete assistant turn
// instead, which TestScanStreamReadsToolCalls covers.
func TestScanStreamParsesRealCLIShape(t *testing.T) {
	const stream = `mise ~/.config/mise/config.toml tools: claude@2.1.250
{"type":"system","subtype":"hook_started","hook_name":"SessionStart:startup"}
{"type":"system","subtype":"init","session_id":"abc-123","model":"claude-sonnet-5","tools":["Bash"]}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"PO"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"NG"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}}
{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Bash"}}}
not json at all
{"type":"result","subtype":"success","is_error":false,"result":"PONG","total_cost_usd":0.12,"duration_ms":1770}
`

	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}

	want := []Event{
		{Kind: KindSession, SessionID: "abc-123", Model: "claude-sonnet-5"},
		{Kind: KindText, Text: "PO"},
		{Kind: KindText, Text: "NG"},
		{Kind: KindThinking, Text: "hmm"},
		{Kind: KindResult, Result: "PONG", CostUSD: 0.12, DurationMS: 1770},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("event %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// TestScanStreamReadsStructuredOutput covers the schema-constrained turn used
// for delegation planning: the object arrives alongside the text result.
func TestScanStreamReadsStructuredOutput(t *testing.T) {
	const stream = `{"type":"result","subtype":"success","is_error":false,"result":"{\"mode\":\"team\"}","structured_output":{"mode":"team","steps":[]}}
`
	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 1 || got[0].Kind != KindResult {
		t.Fatalf("got %+v, want a single result event", got)
	}
	if want := `{"mode":"team","steps":[]}`; string(got[0].Structured) != want {
		t.Errorf("structured = %s, want %s", got[0].Structured, want)
	}
}

// TestScanStreamReportsErrorResult covers the CLI reporting failure in-band,
// which is not the same as the process exiting non-zero.
func TestScanStreamReportsErrorResult(t *testing.T) {
	const stream = `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"rate limited"}
`
	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 1 || got[0].Kind != KindError || got[0].Message != "rate limited" {
		t.Fatalf("got %+v, want a single error event", got)
	}
}

// TestScanStreamHandlesLongLines guards the buffer limit: tool results and
// system payloads regularly exceed bufio's 64KiB default.
func TestScanStreamHandlesLongLines(t *testing.T) {
	long := strings.Repeat("x", 200<<10)
	stream := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + long + `"}}}` + "\n"

	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 1 || len(got[0].Text) != len(long) {
		t.Fatalf("got %d events, first text len %d, want 1 event of len %d",
			len(got), len(got[0].Text), len(long))
	}
}

// TestScanStreamReadsToolCalls covers the pair that makes tool use legible: the
// assistant's complete tool_use block with its arguments, then the tool_result
// that answers it.
func TestScanStreamReadsToolCalls(t *testing.T) {
	const stream = `{"type":"assistant","message":{"content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/tmp/x.go"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":[{"type":"text","text":"package main"}]}]}}
`
	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}

	use := got[0]
	if use.Kind != KindToolUse || use.ToolName != "Read" || use.ToolID != "tu_1" {
		t.Errorf("tool use = %+v, want a Read call with id tu_1", use)
	}
	if want := `{"file_path":"/tmp/x.go"}`; string(use.ToolInput) != want {
		t.Errorf("tool input = %s, want %s", use.ToolInput, want)
	}

	res := got[1]
	if res.Kind != KindToolResult || res.ToolID != "tu_1" || res.ToolResult != "package main" {
		t.Errorf("tool result = %+v, want tu_1 returning the file text", res)
	}
	if res.ToolFailed {
		t.Error("tool result marked failed, want success")
	}
}

// TestScanStreamReadsStringToolResults covers the other shape a result takes.
func TestScanStreamReadsStringToolResults(t *testing.T) {
	const stream = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_2","is_error":true,"content":"permission denied"}]}}
`
	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].ToolResult != "permission denied" || !got[0].ToolFailed {
		t.Errorf("event = %+v, want a failed result carrying the message", got[0])
	}
}

// TestScanStreamTruncatesHugeToolResults guards the event bridge: reading a big
// file must not push the whole thing to the frontend.
func TestScanStreamTruncatesHugeToolResults(t *testing.T) {
	huge := strings.Repeat("y", maxToolResult*2)
	stream := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"` + huge + `"}]}}` + "\n"

	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if len(got[0].ToolResult) > maxToolResult+32 {
		t.Errorf("result length %d, want it truncated near %d", len(got[0].ToolResult), maxToolResult)
	}
	if !strings.HasSuffix(got[0].ToolResult, "truncated") {
		t.Error("truncated result does not say so")
	}
}

// TestScanStreamIgnoresUnknownMessageShapes keeps a CLI change from breaking
// parsing outright.
func TestScanStreamIgnoresUnknownMessageShapes(t *testing.T) {
	const stream = `{"type":"assistant","message":"just a string"}
{"type":"user","message":{"content":"not a list"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"","input":{}}]}}
`
	var got []Event
	if err := scanStream(strings.NewReader(stream), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing emitted", got)
	}
}
