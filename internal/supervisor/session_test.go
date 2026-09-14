package supervisor

import (
	"os"
	"testing"
	"time"
)

func TestLegacySharedSessionIsNotReusedForAnItem(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPath(root), []byte(`{"session_id":"legacy"}`), 0644); err != nil {
		t.Fatal(err)
	}
	sf, err := loadItemSession(root, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if sf.Provider != ProviderClaude || sf.SessionID != "" {
		t.Errorf("got %+v, want a fresh Claude session", sf)
	}
}

func TestSessionStaleness(t *testing.T) {
	now := time.Now()
	if !sessionIsStale(sessionFile{Provider: ProviderClaude, SessionID: "old", UpdatedAt: now.Add(-claudeSubscriptionCacheTTL)}, now) {
		t.Error("session at the age threshold should be stale")
	}
	if sessionIsStale(sessionFile{Provider: ProviderClaude, SessionID: "recent", UpdatedAt: now.Add(-claudeSubscriptionCacheTTL + time.Second)}, now) {
		t.Error("recent session should not be stale")
	}
	if sessionIsStale(sessionFile{Provider: ProviderClaude, UpdatedAt: now.Add(-2 * claudeSubscriptionCacheTTL)}, now) {
		t.Error("empty session should not be stale")
	}
	if !sessionIsStale(sessionFile{Provider: ProviderCodex, SessionID: "old", UpdatedAt: now.Add(-codexCacheTTL)}, now) {
		t.Error("Codex session at its cache threshold should be stale")
	}
	if sessionIsStale(sessionFile{Provider: ProviderCodex, SessionID: "recent", UpdatedAt: now.Add(-codexCacheTTL + time.Second)}, now) {
		t.Error("recent Codex session should not be stale")
	}
}

func TestItemSessionsRoundTripIndependently(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveItemSession(root, "item-1", sessionFile{Provider: ProviderCodex, SessionID: "thread-1", PromptedTurns: intPointer(7)}); err != nil {
		t.Fatal(err)
	}
	if err := saveItemSession(root, "item-2", sessionFile{Provider: ProviderClaude, SessionID: "thread-2"}); err != nil {
		t.Fatal(err)
	}
	sf, err := loadItemSession(root, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if sf.Provider != ProviderCodex || sf.SessionID != "thread-1" || sf.PromptedTurns == nil || *sf.PromptedTurns != 7 {
		t.Errorf("got %+v", sf)
	}
	other, err := loadItemSession(root, "item-2")
	if err != nil || other.Provider != ProviderClaude || other.SessionID != "thread-2" {
		t.Errorf("got %+v, %v", other, err)
	}
}

func TestEffortPersistsPerItemAndProviderPreference(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	s := New(root)
	t.Cleanup(s.Shutdown)
	if err := s.StartNewSession("item-1", ProviderClaude, "opus", "xhigh"); err != nil {
		t.Fatal(err)
	}
	_, _, effort, _, _ := s.Session("item-1")
	if effort != "xhigh" {
		t.Errorf("item effort = %q", effort)
	}
	_, _, inherited, _, _ := s.Session("item-2")
	if inherited != "xhigh" {
		t.Errorf("provider preference = %q", inherited)
	}
}

func TestLatestSelectionIsInheritedByUntouchedItems(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	s := New(root)
	t.Cleanup(s.Shutdown)
	if err := s.StartNewSession("item-1", ProviderCodex, "gpt-5.6-sol", "xhigh"); err != nil {
		t.Fatal(err)
	}

	provider, model, effort, sessionID, _ := s.Session("backlogged-item")
	if provider != ProviderCodex || model != "gpt-5.6-sol" || effort != "xhigh" || sessionID != "" {
		t.Fatalf("untouched session = (%q, %q, %q, %q), want (codex, gpt-5.6-sol, xhigh, empty)", provider, model, effort, sessionID)
	}

	// An item with established state keeps it when the global selection moves.
	if err := s.StartNewSession("item-2", ProviderClaude, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	provider, model, effort, _, _ = s.Session("item-1")
	if provider != ProviderCodex || model != "gpt-5.6-sol" || effort != "xhigh" {
		t.Fatalf("existing item changed with default: (%q, %q, %q)", provider, model, effort)
	}
}
