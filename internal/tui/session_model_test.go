package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ostraka/internal/models"
	"ostraka/internal/supervisor"
)

type agentInfoSupervisor struct {
	info    supervisor.TurnInfo
	hasInfo bool
	model   string
	effort  string
}

func (*agentInfoSupervisor) Enqueue(string) {}

func (s *agentInfoSupervisor) Session(string) (supervisor.Provider, string, string, string, time.Time) {
	return supervisor.ProviderClaude, s.model, s.effort, "", time.Time{}
}

func (*agentInfoSupervisor) SessionIsStale(string) bool { return false }

func (s *agentInfoSupervisor) LastTurnInfo(string) (supervisor.TurnInfo, bool) {
	return s.info, s.hasInfo
}

func (*agentInfoSupervisor) PreferredModel(supervisor.Provider) string  { return "" }
func (*agentInfoSupervisor) PreferredEffort(supervisor.Provider) string { return "" }

func (*agentInfoSupervisor) StartNewSession(string, supervisor.Provider, string, string) error {
	return nil
}

func (*agentInfoSupervisor) AvailableModels(context.Context, supervisor.Provider) ([]supervisor.ModelOption, error) {
	return nil, nil
}

func (*agentInfoSupervisor) Busy() (string, bool) { return "", false }

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
	m.sessionModels = []supervisor.ModelOption{{ID: "opus", DisplayName: "Opus", SupportedReasoningEfforts: []string{"low", "high"}, DefaultReasoningEffort: "high"}}
	m.sessionModelIdx = 0

	next, _ := m.handleSessionModelKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if got.mode != modeSessionEffort {
		t.Fatalf("mode = %v, want modeSessionEffort", got.mode)
	}
	next, _ = got.handleSessionEffortKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(model)
	if got.mode != modeNav {
		t.Fatalf("mode = %v, want modeNav", got.mode)
	}
	provider, chosenModel, _, _, _ := sup.Session("item-1")
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

func TestEffortStepFallsBackToSelectedModelDefaultWhenSavedUnsupported(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	if err := sup.StartNewSession("item-1", supervisor.ProviderCodex, "old-model", "ultra"); err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.sessionProvider = supervisor.ProviderCodex
	m.sessionModels = []supervisor.ModelOption{{ID: "new-model", SupportedReasoningEfforts: []string{"low", "medium", "high"}, DefaultReasoningEffort: "medium"}}
	m.mode = modeSessionModel
	next, _ := m.handleSessionModelKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if got.mode != modeSessionEffort || got.sessionEffortIdx != 1 {
		t.Errorf("mode/index = %v/%d, want effort step/default medium", got.mode, got.sessionEffortIdx)
	}
}

// newSessionModelTestSupervisor gives modelsLoadedMsg tests a real
// Supervisor to read Session() from — the handler now looks up the item's
// already-chosen model to decide what to preselect.
func newSessionModelTestSupervisor(t *testing.T) *supervisor.Supervisor {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".ostraka")
	sup := supervisor.New(root)
	t.Cleanup(sup.Shutdown)
	return sup
}

func TestModelsLoadedMsgPopulatesSessionModelPopupState(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.selected = 0
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
		t.Errorf("sessionModelIdx = %d, want 1 (item has no prior model, falls back to the provider default)", got.sessionModelIdx)
	}
}

func TestModelsLoadedMsgPreselectsTheItemsAlreadyChosenModel(t *testing.T) {
	// Reopening the picker on an item that already has a model must not look
	// like it forgot the choice and reset to the provider's own default.
	sup := newSessionModelTestSupervisor(t)
	if err := sup.StartNewSession("item-1", supervisor.ProviderClaude, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.selected = 0
	m.mode = modeSessionModel
	m.sessionProvider = supervisor.ProviderClaude
	m.sessionModelsLoading = true

	opts := []supervisor.ModelOption{
		{ID: "opus", DisplayName: "Opus"},
		{ID: "sonnet", DisplayName: "Sonnet", Default: true},
	}
	next, _ := m.Update(modelsLoadedMsg{provider: supervisor.ProviderClaude, models: opts})
	got := next.(model)
	if got.sessionModelIdx != 0 {
		t.Errorf("sessionModelIdx = %d, want 0 (opus, the item's already-chosen model, not the provider default)", got.sessionModelIdx)
	}
}

func TestModelsLoadedMsgIgnoresStaleProviderResponse(t *testing.T) {
	// The user can back out of the model step and reopen it against a
	// different provider before a slow Codex app-server round trip returns.
	sup := newSessionModelTestSupervisor(t)
	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.selected = 0
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

func TestRenderAgentInfoShowsTheChosenModelBeforeTheFirstTurn(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	if err := sup.StartNewSession("item-1", supervisor.ProviderClaude, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, nil, sup)

	// No turn has run this process yet (LastTurnInfo is empty), so this must
	// fall back to the explicitly selected model rather than going blank.
	got := m.renderAgentInfo("item-1")
	if got != "opus high " {
		t.Errorf("renderAgentInfo = %q, want %q", got, "opus high ")
	}
}

func TestRenderAgentInfoKeepsContextPercentWithEffortAfterATurn(t *testing.T) {
	sup := &agentInfoSupervisor{
		info:    supervisor.TurnInfo{Model: "claude-sonnet-5", Context: supervisor.ContextUsage{UsedTokens: 20, WindowTokens: 100}},
		hasInfo: true,
		model:   "opus",
		effort:  "high",
	}
	m := newModel(nil, nil, sup)

	if got := m.renderAgentInfo("item-1"); got != "claude-sonnet-5 high 80% left " {
		t.Errorf("renderAgentInfo = %q, want model, effort, and remaining context", got)
	}
}

func TestRenderAgentInfoIsBlankForAnItemWithNoModelChosen(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	m := newModel(nil, nil, sup)

	// A never-touched item has no explicit model and no turn: the harness's
	// own default isn't knowable ahead of a turn, so this stays blank.
	if got := m.renderAgentInfo("item-1"); got != "" {
		t.Errorf("renderAgentInfo = %q, want empty", got)
	}
}

func TestChosenModelBecomesTheDefaultForFutureItems(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	if err := sup.StartNewSession("item-1", supervisor.ProviderClaude, "opus", "high"); err != nil {
		t.Fatal(err)
	}

	provider, model, _, sessionID, _ := sup.Session("item-2")
	if provider != supervisor.ProviderClaude || model != "opus" || sessionID != "" {
		t.Fatalf("new item session = (%q, %q, %q), want (claude, opus, empty)", provider, model, sessionID)
	}

	m := newModel(nil, nil, sup)
	if got := m.renderAgentInfo("item-2"); got != "opus high " {
		t.Errorf("renderAgentInfo before first turn = %q, want %q", got, "opus high ")
	}
}

func TestCodexSelectionAppearsOnUntouchedItemsBeforeFirstTurn(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	if err := sup.StartNewSession("item-1", supervisor.ProviderCodex, "gpt-5.6-sol", "xhigh"); err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, nil, sup)
	if got := m.renderAgentInfo("backlogged-item"); got != "gpt-5.6-sol xhigh " {
		t.Errorf("renderAgentInfo = %q", got)
	}
}

func TestModelsLoadedMsgUsesProviderPreferenceWhenSwitchingProvider(t *testing.T) {
	sup := newSessionModelTestSupervisor(t)
	if err := sup.StartNewSession("codex-item", supervisor.ProviderCodex, "gpt-5.6-sol", "high"); err != nil {
		t.Fatal(err)
	}
	if err := sup.StartNewSession("item-1", supervisor.ProviderClaude, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, nil, sup)
	m.items = []models.Item{{ID: "item-1"}}
	m.mode = modeSessionModel
	m.sessionProvider = supervisor.ProviderCodex
	m.sessionModelsLoading = true

	opts := []supervisor.ModelOption{
		{ID: "gpt-5.5", DisplayName: "GPT-5.5", Default: true},
		{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol"},
	}
	next, _ := m.Update(modelsLoadedMsg{provider: supervisor.ProviderCodex, models: opts})
	if got := next.(model).sessionModelIdx; got != 1 {
		t.Errorf("sessionModelIdx = %d, want 1 (saved Codex preference)", got)
	}
}
