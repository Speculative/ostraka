package supervisor

import (
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

func TestParseCodexLineTracksThreadAndLiveTrace(t *testing.T) {
	var result TurnResult
	var got []string
	parseCodexLine(`{"type":"thread.started","thread_id":"thread-123"}`, &result, func(s string) { got = append(got, s) })
	parseCodexLine(`{"type":"item.completed","item":{"type":"command_execution","command":"go test ./..."}}`, &result, func(s string) { got = append(got, s) })
	parseCodexLine(`{"type":"item.completed","item":{"type":"agent_message","text":"All tests pass."}}`, &result, func(s string) { got = append(got, s) })

	if result.SessionID != "thread-123" {
		t.Errorf("session = %q, want thread-123", result.SessionID)
	}
	if result.ResultText != "All tests pass." {
		t.Errorf("result = %q", result.ResultText)
	}
	if len(got) != 2 || !strings.Contains(got[0], "go test ./...") || got[1] != "All tests pass." {
		t.Errorf("live trace = %q", got)
	}
}

func TestParseCodexLineIgnoresUnknownEvents(t *testing.T) {
	result := TurnResult{}
	parseCodexLine(`{"type":"turn.started"}`, &result, nil)
	parseCodexLine(`not json`, &result, nil)
	if result.SessionID != "" || result.ResultText != "" {
		t.Errorf("unknown event changed result: %+v", result)
	}
}
