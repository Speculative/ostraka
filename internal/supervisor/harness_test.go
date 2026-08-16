package supervisor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Lines below are the real shapes emitted by
// `claude -p --output-format stream-json --verbose`.

func TestParseStreamLineReturnsResult(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"done","session_id":"abc-123",` +
		`"total_cost_usd":0.5,"duration_ms":1200,"num_turns":4,"is_error":false}`
	got, ok := parseStreamLine(line, nil)
	if !ok {
		t.Fatal("result event was not recognised")
	}
	if got.SessionID != "abc-123" || got.NumTurns != 4 || got.Result != "done" {
		t.Errorf("got %+v", got)
	}
}

func TestParseClaudeStreamLineTracksResolvedModelAndLiveContext(t *testing.T) {
	var result TurnResult
	parseClaudeStreamLine(`{"type":"system","subtype":"init","model":"claude-sonnet-5"}`, &result, nil)
	parseClaudeStreamLine(`{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":8500,"output_tokens":1200,"cache_creation_input_tokens":5000,"cache_read_input_tokens":2000}}}`, &result, nil)

	if result.Model != "claude-sonnet-5" {
		t.Errorf("model = %q", result.Model)
	}
	// Claude Code's context meter is input-only: cache writes and reads count,
	// while output tokens do not.
	if result.Context.UsedTokens != 15500 {
		t.Errorf("used tokens = %d, want 15500", result.Context.UsedTokens)
	}
}

func TestParseClaudeStreamLineTakesTheSessionIDFromTheFirstEvent(t *testing.T) {
	// A turn stopped before its result event still has to be resumable, and
	// the init event is where the id first appears.
	var result TurnResult
	parseClaudeStreamLine(`{"type":"system","subtype":"init","session_id":"abc-123","model":"claude-sonnet-5"}`, &result, nil)

	if result.SessionID != "abc-123" {
		t.Errorf("session id = %q, want abc-123", result.SessionID)
	}
}

func TestClaudeResultUsesPrimaryModelContextWindow(t *testing.T) {
	// A helper model must not replace the primary model's context window.
	var result TurnResult
	parseClaudeStreamLine(`{"type":"system","subtype":"init","model":"claude-sonnet-5"}`, &result, nil)
	parseClaudeStreamLine(`{"type":"assistant","message":{"usage":{"input_tokens":100}}}`, &result, nil)
	raw, ok := parseClaudeStreamLine(`{"type":"result","session_id":"s","modelUsage":{"claude-sonnet-5":{"contextWindow":1000000},"claude-haiku-4-5":{"contextWindow":200000}}}`, &result, nil)
	if !ok {
		t.Fatal("result event was not recognised")
	}
	applyClaudeResultTelemetry(&result, raw)
	if result.Context.WindowTokens != 1000000 {
		t.Errorf("primary context window = %d, want 1000000", result.Context.WindowTokens)
	}
}

func TestParseStreamLineIgnoresNonResultEvents(t *testing.T) {
	// Anything that is not the terminating result must not be mistaken for it,
	// or the turn would end early with a zero session id.
	for _, line := range []string{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta"}}`,
		`{"type":"rate_limit_event"}`,
		``,
		`not json at all`,
	} {
		if _, ok := parseStreamLine(line, nil); ok {
			t.Errorf("line %q was treated as the result event", line)
		}
	}
}

func TestParseStreamLineEmitsToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash",` +
		`"input":{"command":"ls /tmp","description":"List files in /tmp"}}]}}`
	var got []string
	parseStreamLine(line, func(s string) { got = append(got, s) })

	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %q", len(got), got)
	}
	if !strings.Contains(got[0], "Bash") {
		t.Errorf("tool name missing from %q", got[0])
	}
	// description wins over command — it is the human-readable field.
	if !strings.Contains(got[0], "List files in /tmp") {
		t.Errorf("input summary missing from %q", got[0])
	}
}

func TestParseStreamLineEmitsText(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"  Working on it.  "}]}}`
	var got []string
	parseStreamLine(line, func(s string) { got = append(got, s) })

	if len(got) != 1 || got[0] != "Working on it." {
		t.Errorf("got %q, want [\"Working on it.\"]", got)
	}
}

func TestParseStreamLineSkipsEmptyThinking(t *testing.T) {
	// Headless mode streams thinking blocks with their text omitted, so they
	// carry nothing to display. Emitting a blank line per block would fill the
	// pane with nothing.
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"x"}]}}`
	var got []string
	parseStreamLine(line, func(s string) { got = append(got, s) })

	if len(got) != 0 {
		t.Errorf("thinking block produced %q, want nothing", got)
	}
}

func TestSummarizeToolInputPrefersReadableField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"description over command", `{"command":"ls -la","description":"List files"}`, "List files"},
		{"command when no description", `{"command":"ls -la"}`, "ls -la"},
		{"file path", `{"file_path":"/a/b.go","old_string":"x","new_string":"y"}`, "/a/b.go"},
		{"newlines flattened", `{"command":"a\nb"}`, "a b"},
	} {
		if got := summarizeToolInput([]byte(tc.input)); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestSummarizeToolInputFallsBackToKeys(t *testing.T) {
	// An unrecognised tool should still say something, and say it the same way
	// every time — map iteration order must not leak into the display.
	got := summarizeToolInput([]byte(`{"zeta":1,"alpha":2,"mid":3}`))
	if got != "alpha, mid, zeta" {
		t.Errorf("got %q, want sorted key list", got)
	}
}

func TestSummarizeToolInputHandlesEmptyAndBroken(t *testing.T) {
	for _, in := range []string{``, `null`, `not json`, `{}`} {
		if got := summarizeToolInput([]byte(in)); got != "" {
			t.Errorf("input %q produced %q, want empty", in, got)
		}
	}
}

func TestTruncateCountsRunesNotBytes(t *testing.T) {
	// Byte slicing would cut a multi-byte rune in half and emit mojibake.
	got := truncate("ααααα", 3)
	if got != "ααα…" {
		t.Errorf("got %q want %q", got, "ααα…")
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("short string was altered: %q", got)
	}
}

func TestParseAppServerEventTracksUsageAndCompletedItems(t *testing.T) {
	var result TurnResult
	var got []string
	onEvent := func(s string) { got = append(got, s) }

	parseAppServerEvent(appServerMessage{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"tokenUsage":{"total":{"totalTokens":15019},"modelContextWindow":258400}}`),
	}, &result, onEvent)
	parseAppServerEvent(appServerMessage{
		Method: "item/completed",
		Params: json.RawMessage(`{"item":{"type":"commandExecution","command":"go test ./..."}}`),
	}, &result, onEvent)
	parseAppServerEvent(appServerMessage{
		Method: "item/completed",
		Params: json.RawMessage(`{"item":{"type":"agentMessage","text":"  All tests pass.  "}}`),
	}, &result, onEvent)
	parseAppServerEvent(appServerMessage{
		Method: "turn/completed",
		Params: json.RawMessage(`{"turn":{"status":"completed","durationMs":1200}}`),
	}, &result, onEvent)

	if result.Context != (ContextUsage{UsedTokens: 15019, WindowTokens: 258400}) {
		t.Errorf("context = %+v", result.Context)
	}
	if result.ResultText != "All tests pass." || result.DurationMs != 1200 || result.IsError {
		t.Errorf("result = %+v", result)
	}
	if len(got) != 2 || !strings.Contains(got[0], "go test ./...") || got[1] != "All tests pass." {
		t.Errorf("live trace = %q", got)
	}
}

func TestParseAppServerEventMarksFailedTurn(t *testing.T) {
	result := TurnResult{}
	parseAppServerEvent(appServerMessage{
		Method: "turn/completed",
		Params: json.RawMessage(`{"turn":{"status":"failed"}}`),
	}, &result, nil)
	if !result.IsError {
		t.Error("failed turn was not marked as an error")
	}
}

func TestAppServerInitializeParamsEnablesExperimentalAPI(t *testing.T) {
	b, err := json.Marshal(appServerInitializeParams())
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		Capabilities struct {
			ExperimentalAPI bool `json:"experimentalApi"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &params); err != nil {
		t.Fatal(err)
	}
	if !params.Capabilities.ExperimentalAPI {
		t.Fatal("initialize request must enable experimentalApi for thread/resume.excludeTurns")
	}
}

func TestAppServerTurnParamsUseNonInteractivePolicy(t *testing.T) {
	b, err := json.Marshal(appServerTurnParams("thread-123", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		ThreadID       string `json:"threadId"`
		ApprovalPolicy string `json:"approvalPolicy"`
		SandboxPolicy  struct {
			Type string `json:"type"`
		} `json:"sandboxPolicy"`
	}
	if err := json.Unmarshal(b, &params); err != nil {
		t.Fatal(err)
	}
	if params.ThreadID != "thread-123" || params.ApprovalPolicy != "never" || params.SandboxPolicy.Type != "dangerFullAccess" {
		t.Errorf("turn params = %s", b)
	}
}

func TestClaudeAvailableModelsOffersADefaultAndTheDocumentedAliases(t *testing.T) {
	// There is no discovery RPC for Claude, so this is a static list — but it
	// must still include an explicit default and be free of duplicate IDs.
	h := newClaudeHarness()
	opts, err := h.AvailableModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	haveDefault := false
	for _, opt := range opts {
		if opt.DisplayName == "" {
			t.Errorf("option %+v has no display name", opt)
		}
		if seen[opt.ID] {
			t.Errorf("duplicate model id %q", opt.ID)
		}
		seen[opt.ID] = true
		if opt.Default {
			haveDefault = true
		}
	}
	if !haveDefault {
		t.Error("no option marked as the default")
	}
}

// parseCodexModelList is exercised directly against a canned response shape
// rather than a live app-server, since the parsing rules (drop hidden
// entries, thread isDefault through) don't need a subprocess to verify.
func TestParseCodexModelListDropsHiddenEntriesAndKeepsDefault(t *testing.T) {
	var raw codexModelListResult
	body := `{"data":[
		{"id":"gpt-5.6-sol","displayName":"GPT-5.6-Sol","hidden":false,"isDefault":true},
		{"id":"gpt-5.6-terra","displayName":"GPT-5.6-Terra","hidden":false,"isDefault":false},
		{"id":"gpt-legacy","displayName":"GPT-Legacy","hidden":true,"isDefault":false}
	]}`
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	got := parseCodexModelList(raw)
	if len(got) != 2 {
		t.Fatalf("parsed %d options, want 2 (hidden entry must be dropped): %+v", len(got), got)
	}
	if got[0].ID != "gpt-5.6-sol" || !got[0].Default {
		t.Errorf("first option = %+v, want the isDefault entry preserved", got[0])
	}
	if got[1].ID != "gpt-5.6-terra" || got[1].Default {
		t.Errorf("second option = %+v", got[1])
	}
}
