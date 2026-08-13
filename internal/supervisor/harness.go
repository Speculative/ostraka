package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// TurnResult is the outcome of one harness turn.
type TurnResult struct {
	SessionID string
	// Model is the provider's resolved primary model for this turn. It is
	// reported rather than inferred from a requested alias because a provider
	// may fall back or use helper models during a turn.
	Model        string
	ResultText   string
	IsError      bool
	DurationMs   int64
	TotalCostUSD float64
	NumTurns     int
	Context      ContextUsage
}

// ContextUsage is the latest provider-authoritative context measurement.
// Zero values mean that the provider did not report context usage.
type ContextUsage struct {
	UsedTokens   int64
	WindowTokens int64
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

// codexHarness drives Codex through its local App Server protocol. Protocol
// details stay inside the adapter; callers use the normal Harness contract.
type codexHarness struct{ bin string }

func newCodexHarness() *codexHarness { return &codexHarness{bin: "codex"} }

func newClaudeHarness() *claudeHarness {
	return &claudeHarness{bin: "claude"}
}

type claudeJSONResult struct {
	Result       string                      `json:"result"`
	SessionID    string                      `json:"session_id"`
	TotalCostUSD float64                     `json:"total_cost_usd"`
	DurationMs   int64                       `json:"duration_ms"`
	NumTurns     int                         `json:"num_turns"`
	IsError      bool                        `json:"is_error"`
	ModelUsage   map[string]claudeModelUsage `json:"modelUsage"`
}

// claudeModelUsage is the per-model summary in Claude Code's terminating
// stream-json result. A turn can include helper/subagent models, so callers
// must select the resolved primary model rather than summing these windows.
type claudeModelUsage struct {
	ContextWindow int64 `json:"contextWindow"`
}

// streamEnvelope is the outer shape shared by every stream-json line.
type streamEnvelope struct {
	Type    string `json:"type"`
	Model   string `json:"model"`
	Message struct {
		Model   string         `json:"model"`
		Content []contentBlock `json:"content"`
		Usage   claudeUsage    `json:"usage"`
	} `json:"message"`
}

// claudeUsage is the live context footprint returned with every assistant
// response. Cache reads and writes count as input context in Claude Code.
type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func (u claudeUsage) contextTokens() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
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
	var telemetry TurnResult
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
			if res, ok := parseClaudeStreamLine(line, &telemetry, onEvent); ok {
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
		Model:        telemetry.Model,
		ResultText:   raw.Result,
		IsError:      raw.IsError,
		DurationMs:   raw.DurationMs,
		TotalCostUSD: raw.TotalCostUSD,
		NumTurns:     raw.NumTurns,
		Context:      telemetry.Context,
	}
	applyClaudeResultTelemetry(&result, raw)
	if runErr != nil {
		return result, fmt.Errorf("claude exited with error: %w (stderr=%q)", runErr, truncate(stderr.String(), 500))
	}
	return result, nil
}

func applyClaudeResultTelemetry(result *TurnResult, raw claudeJSONResult) {
	// modelUsage is a result-only field, whereas Claude emits the primary
	// model and current token footprint earlier in the stream. Join them here
	// once both are known. Older Claude versions may omit either signal.
	if usage, ok := raw.ModelUsage[result.Model]; ok {
		result.Context.WindowTokens = usage.ContextWindow
	} else if len(raw.ModelUsage) == 1 {
		// A one-model turn is unambiguous even if an older init event omitted
		// its model field.
		for model, usage := range raw.ModelUsage {
			if result.Model == "" {
				result.Model = model
			}
			result.Context.WindowTokens = usage.ContextWindow
		}
	}
}

func (h *codexHarness) RunTurn(ctx context.Context, prompt, sessionID string, onEvent func(string)) (TurnResult, error) {
	return h.runAppServer(ctx, prompt, sessionID, onEvent)
}

// appServerMessage is deliberately a small envelope. The App Server schema is
// richer, but this adapter needs only request matching and a few event types.
type appServerMessage struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (h *codexHarness) runAppServer(ctx context.Context, prompt, sessionID string, onEvent func(string)) (result TurnResult, err error) {
	cmd := exec.CommandContext(ctx, h.bin, "app-server", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return result, fmt.Errorf("codex app-server: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result, fmt.Errorf("codex app-server: stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("codex app-server: start: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	encoder := json.NewEncoder(stdin)
	reader := bufio.NewReader(stdout)
	send := func(id int, method string, params any) error {
		return encoder.Encode(struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params any    `json:"params"`
		}{id, method, params})
	}
	next := func() (appServerMessage, error) {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && len(line) == 0 {
			return appServerMessage{}, readErr
		}
		var message appServerMessage
		if unmarshalErr := json.Unmarshal([]byte(strings.TrimSpace(line)), &message); unmarshalErr != nil {
			return appServerMessage{}, fmt.Errorf("invalid JSON-RPC output: %w", unmarshalErr)
		}
		return message, nil
	}
	waitForResponse := func(id int) (appServerMessage, error) {
		for {
			message, readErr := next()
			if readErr != nil {
				return appServerMessage{}, readErr
			}
			if message.Method != "" {
				parseAppServerEvent(message, &result, onEvent)
				continue
			}
			if message.ID != nil && *message.ID == id {
				if message.Error != nil {
					return appServerMessage{}, fmt.Errorf("%s", message.Error.Message)
				}
				return message, nil
			}
		}
	}

	if err := send(1, "initialize", appServerInitializeParams()); err != nil {
		return result, fmt.Errorf("codex app-server: initialize: %w", err)
	}
	if _, err := waitForResponse(1); err != nil {
		return result, appServerFailure(err, stderr.String())
	}

	var threadParams map[string]any
	if sessionID == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return result, fmt.Errorf("codex app-server: get working directory: %w", cwdErr)
		}
		threadParams = map[string]any{"cwd": cwd}
	} else {
		threadParams = map[string]any{"threadId": sessionID, "excludeTurns": true}
	}
	method := "thread/start"
	if sessionID != "" {
		method = "thread/resume"
	}
	if err := send(2, method, threadParams); err != nil {
		return result, fmt.Errorf("codex app-server: %s: %w", method, err)
	}
	threadResponse, err := waitForResponse(2)
	if err != nil {
		return result, appServerFailure(err, stderr.String())
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResponse.Result, &thread); err != nil || thread.Thread.ID == "" {
		return result, fmt.Errorf("codex app-server: %s returned no thread id", method)
	}
	result.SessionID = thread.Thread.ID

	if err := send(3, "turn/start", appServerTurnParams(result.SessionID, prompt)); err != nil {
		return result, fmt.Errorf("codex app-server: turn/start: %w", err)
	}
	if _, err := waitForResponse(3); err != nil {
		return result, appServerFailure(err, stderr.String())
	}

	for {
		message, readErr := next()
		if readErr != nil {
			return result, appServerFailure(readErr, stderr.String())
		}
		if message.Method == "turn/completed" {
			parseAppServerEvent(message, &result, onEvent)
			return result, nil
		}
		parseAppServerEvent(message, &result, onEvent)
	}
}

// appServerInitializeParams declares the optional protocol features this
// adapter uses. In particular, thread/resume.excludeTurns is experimental;
// without advertising it, the server rejects every resumed session before a
// turn can start.
func appServerInitializeParams() map[string]any {
	return map[string]any{
		"clientInfo": map[string]string{"name": "ostraka", "version": "0"},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
}

// appServerTurnParams preserves the old exec adapter's noninteractive
// behaviour. Ostraka already runs inside its own sandbox, and the TUI does
// not implement App Server's approval-request protocol; accepting the default
// on-request policy would otherwise leave a turn waiting forever.
func appServerTurnParams(threadID, prompt string) map[string]any {
	return map[string]any{
		"threadId":       threadID,
		"input":          []map[string]string{{"type": "text", "text": prompt}},
		"approvalPolicy": "never",
		"sandboxPolicy":  map[string]string{"type": "dangerFullAccess"},
	}
}

func appServerFailure(err error, stderr string) error {
	if stderr == "" {
		return fmt.Errorf("codex app-server: %w", err)
	}
	return fmt.Errorf("codex app-server: %w (stderr=%q)", err, truncate(stderr, 500))
}

// parseAppServerEvent translates the provider's notifications into the small
// provider-neutral surface exposed by Harness. Unknown notifications are
// intentionally ignored so App Server additions do not break dispatch.
func parseAppServerEvent(message appServerMessage, result *TurnResult, onEvent func(string)) {
	switch message.Method {
	case "thread/tokenUsage/updated":
		var params struct {
			TokenUsage struct {
				Total struct {
					TotalTokens int64 `json:"totalTokens"`
				} `json:"total"`
				ModelContextWindow int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			result.Context = ContextUsage{
				UsedTokens:   params.TokenUsage.Total.TotalTokens,
				WindowTokens: params.TokenUsage.ModelContextWindow,
			}
		}
	case "item/completed":
		var params struct {
			Item struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Command string          `json:"command"`
				Name    string          `json:"name"`
				Input   json.RawMessage `json:"input"`
			} `json:"item"`
		}
		if json.Unmarshal(message.Params, &params) != nil {
			return
		}
		switch params.Item.Type {
		case "agentMessage":
			if text := strings.TrimSpace(params.Item.Text); text != "" {
				result.ResultText = text
				if onEvent != nil {
					onEvent(text)
				}
			}
		case "commandExecution":
			if params.Item.Command != "" && onEvent != nil {
				onEvent("⚒ shell  " + oneLine(params.Item.Command, 120))
			}
		default:
			if params.Item.Name == "" || onEvent == nil {
				return
			}
			if summary := summarizeToolInput(params.Item.Input); summary != "" {
				onEvent("⚒ " + params.Item.Name + "  " + summary)
			} else {
				onEvent("⚒ " + params.Item.Name)
			}
		}
	case "turn/completed":
		var params struct {
			Turn struct {
				DurationMs int64  `json:"durationMs"`
				Status     string `json:"status"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			result.DurationMs = params.Turn.DurationMs
			result.IsError = params.Turn.Status != "" && params.Turn.Status != "completed"
		}
	}
}

// parseStreamLine decodes one stream-json line, reporting any display-worthy
// content through onEvent. It returns the final result payload once the
// terminating "result" event arrives.
func parseStreamLine(line string, onEvent func(string)) (claudeJSONResult, bool) {
	return parseClaudeStreamLine(line, nil, onEvent)
}

// parseClaudeStreamLine translates Claude Code's stream-json protocol into
// the provider-neutral result surface. result is optional to keep the parser
// convenient for callers that only need the terminating result payload.
func parseClaudeStreamLine(line string, result *TurnResult, onEvent func(string)) (claudeJSONResult, bool) {
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
	if result != nil {
		switch env.Type {
		case "system":
			// The init event's top-level model is the resolved session model.
			if env.Model != "" {
				result.Model = env.Model
			}
		case "assistant":
			// The message-level model reflects a fallback if one occurred.
			if env.Message.Model != "" {
				result.Model = env.Message.Model
			}
			if used := env.Message.Usage.contextTokens(); used > 0 {
				result.Context.UsedTokens = used
			}
		}
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
