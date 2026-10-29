package supervisor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type switchingModelHarness struct {
	noModelDiscovery
	t     *testing.T
	root  string
	calls int
}

func (h *switchingModelHarness) RunTurn(_ context.Context, _ string, sessionID, model, _ string, _ func(string)) (TurnResult, error) {
	h.calls++
	if h.calls == 1 {
		if model != "old-model" {
			h.t.Fatalf("current model = %q", model)
		}
		// An attached browser changes the next selection while this old turn runs.
		if err := SelectNextSession(h.root, "item-1", ProviderClaude, "new-model", "high"); err != nil {
			h.t.Fatal(err)
		}
		snapshot, err := ReadSessionSnapshot(h.root, "item-1")
		if err != nil || snapshot.Current == nil || snapshot.Current.Model != "old-model" || snapshot.Model != "new-model" {
			h.t.Fatalf("mid-turn snapshot = %+v, %v", snapshot, err)
		}
		return TurnResult{SessionID: "old-secret", Model: "resolved-old-model"}, nil
	}
	if model != "new-model" || sessionID != "" {
		h.t.Fatalf("next launch: %q, %q", model, sessionID)
	}
	return TurnResult{SessionID: "new-secret", Model: "resolved-new-model"}, nil
}

func TestAttachedModelChangeSurvivesRunningTurnCompletion(t *testing.T) {
	h := &switchingModelHarness{t: t}
	s := newTestSupervisor(t, h)
	h.root = s.root
	if err := SelectNextSession(s.root, "item-1", ProviderClaude, "old-model", "medium"); err != nil {
		t.Fatal(err)
	}
	s.dispatch(enqueueMsg{itemID: "item-1"})
	snapshot, err := ReadSessionSnapshot(s.root, "item-1")
	if err != nil || snapshot.Model != "new-model" || snapshot.Current.Model != "resolved-old-model" {
		t.Fatalf("after old completion: %+v, %v", snapshot, err)
	}
	sf, err := loadItemSession(s.root, "item-1")
	if err != nil || sf.SessionID != "" {
		t.Fatalf("old session resurrected: %+v, %v", sf, err)
	}
	s.dispatch(enqueueMsg{itemID: "item-1"})
	snapshot, err = ReadSessionSnapshot(s.root, "item-1")
	if err != nil || snapshot.Model != "new-model" || snapshot.Current.Model != "resolved-new-model" {
		t.Fatalf("after next turn: %+v, %v", snapshot, err)
	}
}

func TestConcurrentAttachedModelChoicesPreserveOtherItems(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := SelectNextSession(root, fmt.Sprintf("item-%d", n), ProviderClaude, fmt.Sprintf("model-%d", n), "high"); err != nil {
				t.Error(err)
			}
		}(n)
	}
	wg.Wait()
	for n := 0; n < 20; n++ {
		sf, err := loadItemSession(root, fmt.Sprintf("item-%d", n))
		if err != nil || sf.Model != fmt.Sprintf("model-%d", n) {
			t.Fatalf("lost item %d: %+v, %v", n, sf, err)
		}
	}
	sf, err := loadSessions(root)
	if err != nil || !strings.HasPrefix(sf.ModelDefaults[ProviderClaude], "model-") || sf.DefaultProvider != ProviderClaude {
		t.Fatalf("defaults: %+v, %v", sf, err)
	}
}
