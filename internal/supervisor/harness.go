package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
	RunTurn(ctx context.Context, prompt, sessionID string) (TurnResult, error)
}

// claudeHarness drives the Claude Code CLI in headless mode.
type claudeHarness struct {
	bin string
}

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

func (h *claudeHarness) RunTurn(ctx context.Context, prompt, sessionID string) (TurnResult, error) {
	args := []string{"-p", prompt, "--output-format", "json", "--dangerously-skip-permissions"}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.CommandContext(ctx, h.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var raw claudeJSONResult
	if jsonErr := json.Unmarshal(stdout.Bytes(), &raw); jsonErr != nil {
		return TurnResult{}, fmt.Errorf("claude: malformed JSON output (run err=%v, stderr=%q, stdout=%q): %w",
			runErr, truncate(stderr.String(), 500), truncate(stdout.String(), 500), jsonErr)
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
