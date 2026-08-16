package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ostraka/internal/models"
	"ostraka/internal/supervisor"
)

func TestSessionKeyEnterAdvancesToModelStepAndTriggersLoad(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeSession
	m.sessionIdx = sessionProviderIndex(supervisor.ProviderCodex)

	next, cmd := m.handleSessionKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if got.mode != modeSessionModel {
		t.Fatalf("mode = %v, want modeSessionModel", got.mode)
	}
	if got.sessionProvider != supervisor.ProviderCodex {
		t.Errorf("sessionProvider = %v, want codex", got.sessionProvider)
	}
	if !got.sessionModelsLoading {
		t.Error("expected sessionModelsLoading while models load")
	}
	if cmd == nil {
		t.Error("expected a command to fetch models")
	}
}

func TestSessionModelKeyEscReturnsToProviderStep(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeSessionModel

	next, _ := m.handleSessionModelKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(model)
	if got.mode != modeSession {
		t.Errorf("mode = %v, want modeSession (one step back, not all the way out)", got.mode)
	}
}

func TestSessionModelKeyEnterStartsFreshSessionWithChosenModel(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".ostraka")
	sup := supervisor.New(root)
	t.Cleanup(sup.Shutdown)

	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.selected = 0
	m.mode = modeSessionModel
	m.sessionProvider = supervisor.ProviderClaude
	m.sessionModels = []supervisor.ModelOption{{ID: "opus", DisplayName: "Opus"}}
	m.sessionModelIdx = 0

	next, _ := m.handleSessionModelKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if got.mode != modeNav {
		t.Fatalf("mode = %v, want modeNav", got.mode)
	}
	provider, chosenModel, _, _ := sup.Session("item-1")
	if provider != supervisor.ProviderClaude || chosenModel != "opus" {
		t.Errorf("Session = (%v, %q), want (claude, opus)", provider, chosenModel)
	}
}

func TestSessionModelKeyEnterIsANoOpWhileModelsAreStillLoading(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".ostraka")
	sup := supervisor.New(root)
	t.Cleanup(sup.Shutdown)

	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.selected = 0
	m.mode = modeSessionModel
	m.sessionProvider = supervisor.ProviderClaude
	m.sessionModelsLoading = true
	m.sessionModels = nil

	next, _ := m.handleSessionModelKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if got.mode != modeSessionModel {
		t.Errorf("mode = %v, want modeSessionModel (enter with nothing to choose must not proceed)", got.mode)
	}
}

func TestModelsLoadedMsgPopulatesSessionModelPopupState(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeSessionModel
	m.sessionProvider = supervisor.ProviderClaude
	m.sessionModelsLoading = true

	opts := []supervisor.ModelOption{
		{ID: "opus", DisplayName: "Opus"},
		{ID: "sonnet", DisplayName: "Sonnet", Default: true},
	}
	next, _ := m.Update(modelsLoadedMsg{provider: supervisor.ProviderClaude, models: opts})
	got := next.(model)
	if got.sessionModelsLoading {
		t.Error("still loading after modelsLoadedMsg landed")
	}
	if len(got.sessionModels) != 2 {
		t.Fatalf("sessionModels = %v", got.sessionModels)
	}
	if got.sessionModelIdx != 1 {
		t.Errorf("sessionModelIdx = %d, want 1 (the default option preselected)", got.sessionModelIdx)
	}
}

func TestModelsLoadedMsgIgnoresStaleProviderResponse(t *testing.T) {
	// The user can back out of the model step and reopen it against a
	// different provider before a slow Codex app-server round trip returns.
	m := newModel(nil, nil, nil)
	m.mode = modeSessionModel
	m.sessionProvider = supervisor.ProviderClaude
	m.sessionModelsLoading = true

	next, _ := m.Update(modelsLoadedMsg{provider: supervisor.ProviderCodex, models: []supervisor.ModelOption{{ID: "x"}}})
	got := next.(model)
	if !got.sessionModelsLoading {
		t.Error("a response for a provider no longer selected cleared the loading state")
	}
	if len(got.sessionModels) != 0 {
		t.Error("a response for a provider no longer selected populated sessionModels")
	}
}
