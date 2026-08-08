package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// TurnResult is the outcome of one harness turn.
type TurnResult struct {
	SessionID    string
	ResultText   string
	IsError      bool
	DurationMs   int64
	TotalCostUSD float64
	NumTurns     int
}

// Harness abstracts a coding-agent CLI harness (claude, future codex).
type Harness interface {
	// RunTurn sends prompt to the harness. If sessionID is "", starts a
	// fresh session; otherwise resumes it. The returned TurnResult always
	// carries the harness's session id (new or resumed) when err is nil.
	//
	// onEvent, when non-nil, is called with one display line per event as
	// the turn runs — the caller uses it to show live progress. It is called
	// from RunTurn's goroutine, never concurrently.
	RunTurn(ctx context.Context, prompt, sessionID string, onEvent func(string)) (TurnResult, error)
}

// claudeHarness drives the Claude Code CLI in headless mode.
type claudeHarness struct {
	bin string
}

// codexHarness drives the Codex CLI's JSONL exec interface. Unlike the
// app-server this is deliberately one process per turn, matching the existing
// Claude adapter and keeping v0's lifecycle simple.
type codexHarness struct{ bin string }

func newCodexHarness() *codexHarness { return &codexHarness{bin: "codex"} }

func newClaudeHarness() *claudeHarness {
	return &claudeHarness{bin: "claude"}
}

type claudeJSONResult struct {
	Result       string  `json:"result"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMs   int64   `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
}

// streamEnvelope is the outer shape shared by every stream-json line.
type streamEnvelope struct {
	Type    string `json:"type"`
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
}

func (h *claudeHarness) RunTurn(ctx context.Context, prompt, sessionID string, onEvent func(string)) (TurnResult, error) {
	// stream-json (with --verbose, which it requires) emits one JSON object
	// per line as the turn runs, rather than a single blob at the end — that
	// is what makes a live progress view possible. --brief additionally gives
	// the agent a tool for pushing deliberate updates mid-turn.
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--brief",
		"--dangerously-skip-permissions",
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.CommandContext(ctx, h.bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return TurnResult{}, fmt.Errorf("claude: stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return TurnResult{}, fmt.Errorf("claude: start: %w", err)
	}

	var raw claudeJSONResult
	var sawResult bool
	var tail bytes.Buffer // kept only for the error message when parsing fails

	// bufio.Reader rather than Scanner: a single tool_result line can exceed
	// any fixed token size, and a dropped line would silently lose the result.
	r := bufio.NewReader(stdout)
	for {
		line, readErr := r.ReadString('\n')
		if len(line) > 0 {
			if tail.Len() < 2000 {
				tail.WriteString(line)
			}
			if res, ok := parseStreamLine(line, onEvent); ok {
				raw, sawResult = res, true
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				stderr.WriteString("\nstdout read error: " + readErr.Error())
			}
			break
		}
	}
	runErr := cmd.Wait()

	if !sawResult {
		return TurnResult{}, fmt.Errorf("claude: no result event in output (run err=%v, stderr=%q, stdout=%q)",
			runErr, truncate(stderr.String(), 500), truncate(tail.String(), 500))
	}

	result := TurnResult{
		SessionID:    raw.SessionID,
		ResultText:   raw.Result,
		IsError:      raw.IsError,
		DurationMs:   raw.DurationMs,
		TotalCostUSD: raw.TotalCostUSD,
		NumTurns:     raw.NumTurns,
	}
	if runErr != nil {
		return result, fmt.Errorf("claude exited with error: %w (stderr=%q)", runErr, truncate(stderr.String(), 500))
	}
	return result, nil
}

func (h *codexHarness) RunTurn(ctx context.Context, prompt, sessionID string, onEvent func(string)) (TurnResult, error) {
	args := []string{"exec"}
	if sessionID != "" {
		args = append(args, "resume", sessionID)
	}
	args = append(args, "--json", "--dangerously-bypass-approvals-and-sandbox", prompt)
	cmd := exec.CommandContext(ctx, h.bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return TurnResult{}, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return TurnResult{}, fmt.Errorf("codex: start: %w", err)
	}

	var result TurnResult
	var tail bytes.Buffer
	r := bufio.NewReader(stdout)
	for {
		line, readErr := r.ReadString('\n')
		if len(line) > 0 {
			if tail.Len() < 2000 {
				tail.WriteString(line)
			}
			parseCodexLine(line, &result, onEvent)
		}
		if readErr != nil {
			if readErr != io.EOF {
				stderr.WriteString("\nstdout read error: " + readErr.Error())
			}
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		return result, fmt.Errorf("codex exited with error: %w (stderr=%q, stdout=%q)", err, truncate(stderr.String(), 500), truncate(tail.String(), 500))
	}
	if result.SessionID == "" {
		return TurnResult{}, fmt.Errorf("codex: no thread.started event in output (stderr=%q, stdout=%q)", truncate(stderr.String(), 500), truncate(tail.String(), 500))
	}
	return result, nil
}

// Codex exec --json emits JSONL events. Keep the decoder intentionally loose:
// unknown event types are safely ignored, so cosmetic CLI event additions do
// not break dispatch. thread.started supplies the durable resume ID; completed
// agent messages and tool calls are the useful parts of the live trace.
func parseCodexLine(line string, result *TurnResult, onEvent func(string)) {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Command string          `json:"command"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
		} `json:"item"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil {
		return
	}
	if event.Type == "thread.started" && event.ThreadID != "" {
		result.SessionID = event.ThreadID
		return
	}
	if event.Type != "item.completed" || onEvent == nil {
		return
	}
	switch event.Item.Type {
	case "agent_message":
		if text := strings.TrimSpace(event.Item.Text); text != "" {
			result.ResultText = text
			onEvent(text)
		}
	case "command_execution":
		if event.Item.Command != "" {
			onEvent("⚒ shell  " + oneLine(event.Item.Command, 120))
		}
	case "mcp_tool_call", "web_search", "file_change":
		name := event.Item.Name
		if name == "" {
			name = event.Item.Type
		}
		if summary := summarizeToolInput(event.Item.Input); summary != "" {
			onEvent("⚒ " + name + "  " + summary)
		} else {
			onEvent("⚒ " + name)
		}
	}
}

// parseStreamLine decodes one stream-json line, reporting any display-worthy
// content through onEvent. It returns the final result payload once the
// terminating "result" event arrives.
func parseStreamLine(line string, onEvent func(string)) (claudeJSONResult, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return claudeJSONResult{}, false
	}
	var env streamEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		// Non-JSON noise on stdout is not fatal — the result event is what
		// matters, and dropping a garbled progress line costs nothing.
		return claudeJSONResult{}, false
	}

	if env.Type == "result" {
		var raw claudeJSONResult
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return claudeJSONResult{}, false
		}
		return raw, true
	}

	if onEvent != nil && env.Type == "assistant" {
		for _, block := range env.Message.Content {
			if s := formatBlock(block); s != "" {
				onEvent(s)
			}
		}
	}
	return claudeJSONResult{}, false
}

// formatBlock renders one content block as a progress line, or "" to skip it.
// Thinking blocks are deliberately skipped: headless mode streams them with
// their text omitted, so they carry no information to show.
func formatBlock(b contentBlock) string {
	switch b.Type {
	case "text":
		return strings.TrimSpace(b.Text)
	case "tool_use":
		if summary := summarizeToolInput(b.Input); summary != "" {
			return "⚒ " + b.Name + "  " + summary
		}
		return "⚒ " + b.Name
	}
	return ""
}

// preferredInputKeys are the tool-input fields worth showing, in priority
// order — the one thing a reader wants to know is *what* the tool was pointed
// at, not the whole argument object.
var preferredInputKeys = []string{
	"description", "command", "file_path", "path", "pattern", "query", "url", "prompt", "message",
}

func summarizeToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, key := range preferredInputKeys {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return oneLine(s, 120)
		}
	}
	// No recognised field: show the keys so the line still says something,
	// sorted so the same call never renders two different ways.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return oneLine(strings.Join(keys, ", "), 120)
}

func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	return truncate(s, n)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
