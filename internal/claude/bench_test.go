package claude

import (
	"bytes"
	"strings"
	"testing"
)

// A run's stream is overwhelmingly text deltas: one line per token, with a
// handful of tool calls and one result among them. Anything scanStream does
// per line is therefore paid per token, which is why this benchmark is
// weighted the way a real run is rather than evenly across event kinds.
func benchStream(tokens int) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"system","subtype":"init","session_id":"s-1","model":"claude-opus-5"}` + "\n")
	for i := 0; i < tokens; i++ {
		b.WriteString(`{"type":"stream_event","session_id":"s-1","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" token"}}}` + "\n")
		if i%200 == 0 {
			b.WriteString(`{"type":"stream_event","session_id":"s-1","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Read"}}}` + "\n")
			b.WriteString(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/tmp/x"}}]}}` + "\n")
			b.WriteString(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n")
		}
	}
	b.WriteString(`{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.01,"duration_ms":1234}` + "\n")
	return []byte(b.String())
}

func BenchmarkScanStream(b *testing.B) {
	data := benchStream(2000)
	sink := func(e Event) { _ = e.Text }

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := scanStream(bytes.NewReader(data), sink); err != nil {
			b.Fatal(err)
		}
	}
}
