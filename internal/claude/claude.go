// Package claude runs the user's local Claude Code CLI and turns its streaming
// JSON output into a small set of Go events.
//
// Authentication is deliberately not handled here. The local `claude` binary
// already owns the user's session, so Work shells out to it rather than talking
// to the API directly. No credentials pass through this process.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// EventKind identifies what happened during a run.
type EventKind string

const (
	// KindSession fires once, when the CLI reports its session ID and model.
	KindSession EventKind = "session"
	// KindText carries a chunk of assistant text as it streams in.
	KindText EventKind = "text"
	// KindThinking carries a chunk of extended thinking text.
	KindThinking EventKind = "thinking"
	// KindToolUse fires when the assistant has decided on a tool call, with the
	// full arguments it intends to use.
	KindToolUse EventKind = "tool_use"
	// KindToolResult fires when a tool call comes back.
	KindToolResult EventKind = "tool_result"
	// KindResult fires once at the end of a successful run.
	KindResult EventKind = "result"
	// KindError reports a failure: a non-zero exit, an unusable binary, or an
	// error result from the CLI itself.
	KindError EventKind = "error"
)

// Event is one thing worth telling the frontend about.
type Event struct {
	Kind EventKind `json:"kind"`

	// Text is set for KindText and KindThinking.
	Text string `json:"text,omitempty"`
	// SessionID and Model are set for KindSession.
	SessionID string `json:"sessionId,omitempty"`
	Model     string `json:"model,omitempty"`
	// ToolID correlates a KindToolUse with its KindToolResult.
	ToolID string `json:"toolId,omitempty"`
	// ToolName is set for KindToolUse.
	ToolName string `json:"toolName,omitempty"`
	// ToolInput is the tool's arguments, verbatim, for KindToolUse.
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
	// ToolResult is what the tool returned, for KindToolResult. Long output is
	// truncated: this is for reading, not for replaying.
	ToolResult string `json:"toolResult,omitempty"`
	// ToolFailed marks a tool call the harness rejected or that errored.
	ToolFailed bool `json:"toolFailed,omitempty"`
	// Result, Structured, CostUSD and DurationMS are set for KindResult.
	Result string `json:"result,omitempty"`
	// Structured is the schema-validated object, present only when the request
	// carried a JSONSchema.
	Structured json.RawMessage `json:"structured,omitempty"`
	CostUSD    float64         `json:"costUsd,omitempty"`
	DurationMS int64           `json:"durationMs,omitempty"`
	// Message is set for KindError.
	Message string `json:"message,omitempty"`
}

// Request describes a single prompt to run.
type Request struct {
	// Prompt is the user's task. Required.
	Prompt string
	// Model is a Claude model alias or full name. Empty means the CLI default.
	Model string
	// AppendSystemPrompt is added to Claude's own system prompt. Used to give
	// an agent its personality.
	AppendSystemPrompt string
	// Resume continues an existing Claude session by ID. Empty starts fresh.
	Resume string
	// WorkDir is the directory Claude runs in. Empty means the current one.
	WorkDir string
	// AllowedTools restricts tool access. Empty means the CLI default.
	AllowedTools []string
	// JSONSchema constrains the reply to a schema-validated object, returned on
	// the result event as Event.Structured. Empty means a normal prose reply.
	JSONSchema string
	// PermissionMode is passed straight to the CLI. Empty inherits whatever the
	// user's own Claude installation already allows, which is the safe default:
	// Work does not quietly widen what an agent may do.
	PermissionMode string
}

// Runner executes prompts against the local Claude CLI.
//
// A Runner is safe for concurrent use; each Run gets its own process.
type Runner struct {
	// Bin is the CLI to execute. Defaults to "claude".
	Bin string
}

// NewRunner returns a Runner for the given binary. Empty bin means "claude".
func NewRunner(bin string) *Runner {
	if bin == "" {
		bin = "claude"
	}
	return &Runner{Bin: bin}
}

// Available reports whether the local Claude CLI can be found on PATH.
func (r *Runner) Available() (string, error) {
	return exec.LookPath(r.Bin)
}

// maxLine bounds a single JSON line from the CLI. Tool results and system
// payloads can be large, so the default bufio limit of 64KiB is not enough.
const maxLine = 8 << 20 // 8 MiB

// Run executes req and calls sink for every event, in order, from the calling
// goroutine's perspective: sink is never called concurrently with itself.
//
// Cancelling ctx kills the Claude process. Run returns ctx.Err() in that case,
// after emitting no further events.
func (r *Runner) Run(ctx context.Context, req Request, sink func(Event)) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return errors.New("claude: empty prompt")
	}

	args := []string{
		"-p", req.Prompt,
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.AppendSystemPrompt)
	}
	if req.Resume != "" {
		args = append(args, "--resume", req.Resume)
	}
	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowed-tools", strings.Join(req.AllowedTools, ","))
	}
	if req.JSONSchema != "" {
		args = append(args, "--json-schema", req.JSONSchema)
	}
	if req.PermissionMode != "" {
		args = append(args, "--permission-mode", req.PermissionMode)
	}

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = req.WorkDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("claude: stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, max: 16 << 10}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude: start %s: %w", r.Bin, err)
	}

	var mu sync.Mutex
	emit := func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		sink(e)
	}

	scanErr := scanStream(stdout, emit)
	if scanErr != nil {
		// Giving up on the stream leaves the CLI writing into a pipe nobody
		// reads. Wait would then block until that buffer drained, which it
		// never does, so a single over-long line would strand the process and
		// this goroutine for as long as the app lived. Drain it instead.
		_, _ = io.Copy(io.Discard, stdout)
	}

	waitErr := cmd.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if scanErr != nil && !errors.Is(scanErr, io.EOF) {
		return fmt.Errorf("claude: read stream: %w", scanErr)
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("claude: %s", msg)
	}
	return nil
}

// envelope is the outer shape of every line the CLI writes. Only the fields
// Work reacts to are declared; everything else is ignored on purpose so that
// new CLI fields do not break parsing.
type envelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init
	SessionID string `json:"session_id"`
	Model     string `json:"model"`

	// stream_event. Decoded inline rather than kept raw: this is the only
	// line the CLI sends per token, so a second Unmarshal of the same bytes
	// here is the difference between one parse per token and two.
	Event streamEvent `json:"event"`

	// assistant
	Message json.RawMessage `json:"message"`

	// result
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	IsError          bool            `json:"is_error"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	DurationMS       int64           `json:"duration_ms"`
	APIErrorStatus   any             `json:"api_error_status"`
}

// message is the shape of an assistant or user turn. Tool calls arrive here
// complete, with their arguments, which the partial-message stream cannot give
// us: the streamed form delivers arguments a character at a time and would
// need reassembling for no benefit.
type message struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`

	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// maxToolResult caps how much of a tool's output reaches the frontend. Reading
// a large file would otherwise push a megabyte through the event bridge.
const maxToolResult = 8 << 10

type streamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
	ContentBlock struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"content_block"`
}

// scanStream reads newline-delimited JSON and emits events. Non-JSON lines are
// skipped: version managers and shell wrappers sometimes print a banner before
// the CLI's own output.
func scanStream(rd io.Reader, emit func(Event)) error {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}

		switch env.Type {
		case "system":
			if env.Subtype == "init" {
				emit(Event{Kind: KindSession, SessionID: env.SessionID, Model: env.Model})
			}

		case "stream_event":
			if env.Event.Type != "content_block_delta" {
				continue
			}
			switch env.Event.Delta.Type {
			case "text_delta":
				if env.Event.Delta.Text != "" {
					emit(Event{Kind: KindText, Text: env.Event.Delta.Text})
				}
			case "thinking_delta":
				if env.Event.Delta.Thinking != "" {
					emit(Event{Kind: KindThinking, Text: env.Event.Delta.Thinking})
				}
			}

		case "assistant":
			for _, block := range decodeBlocks(env.Message) {
				if block.Type != "tool_use" || block.Name == "" {
					continue
				}
				emit(Event{
					Kind:      KindToolUse,
					ToolID:    block.ID,
					ToolName:  block.Name,
					ToolInput: block.Input,
				})
			}

		case "user":
			for _, block := range decodeBlocks(env.Message) {
				if block.Type != "tool_result" {
					continue
				}
				emit(Event{
					Kind:       KindToolResult,
					ToolID:     block.ToolUseID,
					ToolResult: flattenContent(block.Content),
					ToolFailed: block.IsError,
				})
			}

		case "result":
			if env.IsError {
				msg := env.Result
				if msg == "" {
					msg = "claude reported an error"
				}
				emit(Event{Kind: KindError, Message: msg})
				continue
			}
			emit(Event{
				Kind:       KindResult,
				Result:     env.Result,
				Structured: env.StructuredOutput,
				CostUSD:    env.TotalCostUSD,
				DurationMS: env.DurationMS,
			})
		}
	}
	return sc.Err()
}

// decodeBlocks pulls the content blocks out of a turn, tolerating anything
// unexpected: a shape we do not recognise means no tool calls, not an error.
func decodeBlocks(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}
	var msg message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}
	return msg.Content
}

// flattenContent turns a tool result into readable text. The field is either a
// plain string or a list of content blocks, depending on the tool.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return truncate(text)
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// Cut before copying. A tool result can run to megabytes and only
		// maxToolResult of it survives, so allocating the whole of it as a
		// string first is a copy nobody reads.
		return truncateBytes(raw)
	}

	var b strings.Builder
	for _, block := range blocks {
		if block.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(block.Text)
		if b.Len() >= maxToolResult {
			break
		}
	}
	return truncate(b.String())
}

func truncate(s string) string {
	if len(s) <= maxToolResult {
		return s
	}
	return s[:maxToolResult] + "\n… truncated"
}

// truncateBytes is truncate for bytes that are about to become a string, so
// the cut happens before the copy rather than after it.
func truncateBytes(b []byte) string {
	if len(b) <= maxToolResult {
		return string(b)
	}
	return string(b[:maxToolResult]) + "\n… truncated"
}

// limitedWriter keeps at most max bytes, so a chatty stderr cannot grow without
// bound while a long task runs.
type limitedWriter struct {
	w   io.Writer
	max int
	n   int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n >= l.max {
		return len(p), nil
	}
	if room := l.max - l.n; len(p) > room {
		p = p[:room]
	}
	n, err := l.w.Write(p)
	l.n += n
	return len(p), err
}
