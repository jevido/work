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
	// KindToolUse fires when the assistant starts using a tool.
	KindToolUse EventKind = "tool_use"
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
	// ToolName is set for KindToolUse.
	ToolName string `json:"toolName,omitempty"`
	// Result, CostUSD and DurationMS are set for KindResult.
	Result     string  `json:"result,omitempty"`
	CostUSD    float64 `json:"costUsd,omitempty"`
	DurationMS int64   `json:"durationMs,omitempty"`
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

	// stream_event
	Event json.RawMessage `json:"event"`

	// assistant
	Message json.RawMessage `json:"message"`

	// result
	Result         string  `json:"result"`
	IsError        bool    `json:"is_error"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	DurationMS     int64   `json:"duration_ms"`
	APIErrorStatus any     `json:"api_error_status"`
}

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
			var se streamEvent
			if len(env.Event) == 0 || json.Unmarshal(env.Event, &se) != nil {
				continue
			}
			switch se.Type {
			case "content_block_delta":
				switch se.Delta.Type {
				case "text_delta":
					if se.Delta.Text != "" {
						emit(Event{Kind: KindText, Text: se.Delta.Text})
					}
				case "thinking_delta":
					if se.Delta.Thinking != "" {
						emit(Event{Kind: KindThinking, Text: se.Delta.Thinking})
					}
				}
			case "content_block_start":
				if se.ContentBlock.Type == "tool_use" && se.ContentBlock.Name != "" {
					emit(Event{Kind: KindToolUse, ToolName: se.ContentBlock.Name})
				}
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
				CostUSD:    env.TotalCostUSD,
				DurationMS: env.DurationMS,
			})
		}
	}
	return sc.Err()
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
