package claude

import (
	"strings"
	"testing"
)

// TestScanStreamParsesRealCLIShape uses lines in the exact shape the Claude CLI
// emits, including the noise cases: a version-manager banner on the first line,
// a hook payload, a partial-message delta, and a trailing result.
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
		{Kind: KindToolUse, ToolName: "Bash"},
		{Kind: KindResult, Result: "PONG", CostUSD: 0.12, DurationMS: 1770},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
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
