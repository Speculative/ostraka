package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type closeBuffer struct{ bytes.Buffer }

func (*closeBuffer) Close() error { return nil }

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

func TestClaudeRunArgsEnablePartialMessages(t *testing.T) {
	args := claudeRunArgs("hello", "", "", "")
	if !strings.Contains(strings.Join(args, " "), "--include-partial-messages") {
		t.Fatalf("Claude args do not enable reasoning-phase boundaries: %q", args)
	}
}

func TestParseClaudeStreamLineEmitsOneIndicatorPerThinkingPhase(t *testing.T) {
	start := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}}`
	delta := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}}`
	var got []string
	onEvent := func(s string) { got = append(got, s) }

	parseClaudeStreamLine(start, nil, onEvent)
	parseClaudeStreamLine(delta, nil, onEvent)
	parseClaudeStreamLine(start, nil, onEvent)

	if !reflect.DeepEqual(got, []string{"⚙  reasoning", "⚙  reasoning"}) {
		t.Fatalf("reasoning indicators = %q, want one per thinking block start", got)
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

func TestParseAppServerEventTracksUsageStartedToolsAndCompletedMessages(t *testing.T) {
	var result TurnResult
	var got []string
	onEvent := func(s string) { got = append(got, s) }

	parseAppServerEvent(appServerMessage{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"tokenUsage":{"last":{"totalTokens":15019},"total":{"totalTokens":150190},"modelContextWindow":258400}}`),
	}, &result, onEvent)
	parseAppServerEvent(appServerMessage{
		Method: "item/started",
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

func TestParseAppServerEventShowsStartedCommandBeforeCompletionWithoutDuplicate(t *testing.T) {
	var result TurnResult
	var got []string
	onEvent := func(s string) { got = append(got, s) }
	started := appServerMessage{
		Method: "item/started",
		Params: json.RawMessage(`{"item":{"id":"cmd-1","type":"commandExecution","command":"sleep 30"}}`),
	}
	completed := appServerMessage{
		Method: "item/completed",
		Params: json.RawMessage(`{"item":{"id":"cmd-1","type":"commandExecution","command":"sleep 30","status":"completed"}}`),
	}

	parseAppServerEvent(started, &result, onEvent)
	if len(got) != 1 || !strings.Contains(got[0], "sleep 30") {
		t.Fatalf("live trace immediately after start = %q, want the running command", got)
	}
	parseAppServerEvent(completed, &result, onEvent)
	if len(got) != 1 {
		t.Fatalf("live trace after completion = %q, want no duplicate command", got)
	}
}

func TestParseAppServerEventShowsStartedMCPAndDynamicTools(t *testing.T) {
	for _, tc := range []struct {
		name     string
		itemType string
		tool     string
		args     string
		want     string
	}{
		{name: "MCP tool", itemType: "mcpToolCall", tool: "search", args: `{"query":"live progress"}`, want: "search  live progress"},
		{name: "dynamic tool", itemType: "dynamicToolCall", tool: "deploy", args: `{"path":"staging"}`, want: "deploy  staging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			params := `{"item":{"type":` + strconv.Quote(tc.itemType) + `,"tool":` + strconv.Quote(tc.tool) + `,"arguments":` + tc.args + `}}`
			parseAppServerEvent(appServerMessage{Method: "item/started", Params: json.RawMessage(params)}, &TurnResult{}, func(s string) {
				got = append(got, s)
			})
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("live trace = %q, want one summary containing %q", got, tc.want)
			}
		})
	}
}

func TestParseAppServerEventShowsOneIndicatorPerReasoningPhase(t *testing.T) {
	var got []string
	onEvent := func(s string) { got = append(got, s) }
	start := appServerMessage{
		Method: "item/started",
		Params: json.RawMessage(`{"item":{"id":"reasoning-1","type":"reasoning","summary":[],"content":[]}}`),
	}
	completed := appServerMessage{
		Method: "item/completed",
		Params: json.RawMessage(`{"item":{"id":"reasoning-1","type":"reasoning","summary":[],"content":[]}}`),
	}

	parseAppServerEvent(start, &TurnResult{}, onEvent)
	parseAppServerEvent(completed, &TurnResult{}, onEvent)
	if !reflect.DeepEqual(got, []string{"⚙  reasoning"}) {
		t.Fatalf("reasoning indicators = %q, want only the phase start", got)
	}
}

func TestParseAppServerEventInterruptedTurnRetainsStartedCommand(t *testing.T) {
	var result TurnResult
	var got []string
	onEvent := func(s string) { got = append(got, s) }
	parseAppServerEvent(appServerMessage{
		Method: "item/started",
		Params: json.RawMessage(`{"item":{"id":"cmd-1","type":"commandExecution","command":"go test ./..."}}`),
	}, &result, onEvent)
	parseAppServerEvent(appServerMessage{
		Method: "turn/completed",
		Params: json.RawMessage(`{"turn":{"status":"interrupted"}}`),
	}, &result, onEvent)

	if !result.IsError {
		t.Fatal("interrupted turn was not marked as unsuccessful")
	}
	if len(got) != 1 || !strings.Contains(got[0], "go test ./...") {
		t.Fatalf("live trace after interruption = %q, want the already-started command", got)
	}
}

func TestParseAppServerEventUsesLastUsageNotCumulativeTotal(t *testing.T) {
	var result TurnResult
	parseAppServerEvent(appServerMessage{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"tokenUsage":{"last":{"totalTokens":94000},"total":{"totalTokens":2682109},"modelContextWindow":258400}}`),
	}, &result, nil)

	if result.Context != (ContextUsage{UsedTokens: 94000, WindowTokens: 258400}) {
		t.Fatalf("context = %+v, want latest context usage rather than cumulative total", result.Context)
	}
}

func TestParseAppServerEventDoesNotUseImpossibleCumulativeFallback(t *testing.T) {
	var result TurnResult
	parseAppServerEvent(appServerMessage{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"tokenUsage":{"total":{"totalTokens":2682109},"modelContextWindow":258400}}`),
	}, &result, nil)

	if result.Context != (ContextUsage{WindowTokens: 258400}) {
		t.Fatalf("context = %+v, want no impossible cumulative usage", result.Context)
	}
}

func TestParseAppServerEventMarksFailedTurn(t *testing.T) {
	result := TurnResult{}
	parseAppServerEvent(appServerMessage{
		Method: "turn/completed",
		Params: json.RawMessage(`{"turn":{"status":"failed","error":{"message":"quota exceeded"}}}`),
	}, &result, nil)
	if !result.IsError {
		t.Error("failed turn was not marked as an error")
	}
	if result.ErrorText != "quota exceeded" {
		t.Errorf("failure text = %q, want quota exceeded", result.ErrorText)
	}
}

func TestTurnErrorTextFallsBackToResultText(t *testing.T) {
	got := turnErrorText(TurnResult{ResultText: "provider stopped"})
	if got != "provider stopped" {
		t.Errorf("error text = %q", got)
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
	b, err := json.Marshal(appServerTurnParams("thread-123", "hello", ""))
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

func TestAppServerTurnParamsIncludeEffortOnlyWhenProvided(t *testing.T) {
	if got := appServerTurnParams("thread-123", "hello", "xhigh")["effort"]; got != "xhigh" {
		t.Errorf("effort = %v, want xhigh", got)
	}
	if _, ok := appServerTurnParams("thread-123", "hello", "")["effort"]; ok {
		t.Error("empty effort should be omitted")
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
		if !reflect.DeepEqual(opt.SupportedReasoningEfforts, []string{"low", "medium", "high", "xhigh", "max"}) {
			t.Errorf("option %q efforts = %v", opt.ID, opt.SupportedReasoningEfforts)
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
		{"id":"gpt-5.6-sol","displayName":"GPT-5.6-Sol","hidden":false,"isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"medium","description":"balanced"},{"reasoningEffort":"xhigh","description":"deep"}],"defaultReasoningEffort":"medium"},
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
	if !reflect.DeepEqual(got[0].SupportedReasoningEfforts, []string{"medium", "xhigh"}) || got[0].DefaultReasoningEffort != "medium" {
		t.Errorf("first option effort metadata = %+v", got[0])
	}
	if got[1].ID != "gpt-5.6-terra" || got[1].Default {
		t.Errorf("second option = %+v", got[1])
	}
}

func TestAppServerTurnIDReadsStartResponseAndNotification(t *testing.T) {
	for name, message := range map[string]appServerMessage{
		"start response":       {Result: json.RawMessage(`{"turn":{"id":"turn-from-response"}}`)},
		"started notification": {Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-from-notification"}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			want := "turn-from-response"
			if name == "started notification" {
				want = "turn-from-notification"
			}
			if got := appServerTurnID(message); got != want {
				t.Fatalf("turn id = %q, want %q", got, want)
			}
		})
	}
}

func TestCodexInterruptSendsThreadAndTurnIDs(t *testing.T) {
	var output closeBuffer
	h := &codexHarness{
		active: &appServerConn{stdin: &output},
		thread: "thread-123",
		turn:   "turn-456",
	}
	if err := h.Interrupt(); err != nil {
		t.Fatal(err)
	}
	var request struct {
		ID     int            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(output.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != "turn/interrupt" || request.Params["threadId"] != "thread-123" || request.Params["turnId"] != "turn-456" {
		t.Fatalf("interrupt request = %+v", request)
	}
}
