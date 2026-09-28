package store

import (
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestAgentSessionLabelIncludesModelAndEffort(t *testing.T) {
	got := AgentSessionLabel(models.Activity{
		Result: "codex",
		Model:  "gpt-5.6-luna",
		Effort: "xhigh",
	})
	if got != "codex gpt-5.6-luna xhigh" {
		t.Fatalf("label = %q, want %q", got, "codex gpt-5.6-luna xhigh")
	}
}

func TestAgentSessionLabelKeepsLegacyProviderOnlyActivityReadable(t *testing.T) {
	if got := AgentSessionLabel(models.Activity{Result: "claude"}); got != "claude" {
		t.Fatalf("label = %q, want %q", got, "claude")
	}
	if got := AgentSessionLabel(models.Activity{}); got != "provider" {
		t.Fatalf("empty label = %q, want %q", got, "provider")
	}
}
