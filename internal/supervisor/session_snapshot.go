package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ModelSelection identifies the provider/model used by a dispatched turn.
type ModelSelection struct {
	Provider Provider `json:"provider"`
	Model    string   `json:"model"`
	Effort   string   `json:"effort"`
}

// SessionSnapshot separates the next selection from the latest dispatched
// model. It omits provider conversation IDs and prompt cursors.
type SessionSnapshot struct {
	Provider  Provider        `json:"provider"`
	Model     string          `json:"model"`
	Effort    string          `json:"effort"`
	UpdatedAt time.Time       `json:"updated_at"`
	Stale     bool            `json:"stale"`
	Current   *ModelSelection `json:"current,omitempty"`
}

// ReadSessionSnapshot reads persisted state without constructing a supervisor:
// constructing one would recover dispatches and sweep another host's live logs.
func ReadSessionSnapshot(root, itemID string) (SessionSnapshot, error) {
	sf, err := loadItemSession(root, itemID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	snapshot := SessionSnapshot{Provider: sf.Provider, Model: sf.Model, Effort: sf.Effort, UpdatedAt: sf.UpdatedAt, Stale: sessionIsStale(sf, time.Now())}
	data, err := os.ReadFile(filepath.Join(supervisorDir(root), "model-"+itemID+".json"))
	if err == nil {
		var current ModelSelection
		if json.Unmarshal(data, &current) == nil {
			snapshot.Current = &current
			if sf.SessionID != "" && snapshot.Model == "" && current.Provider == snapshot.Provider {
				snapshot.Model = current.Model
			}
		}
	}
	if snapshot.Current == nil && sf.SessionID != "" {
		snapshot.Current = &ModelSelection{Provider: sf.Provider, Model: sf.Model, Effort: sf.Effort}
	}
	return snapshot, nil
}

func writeCurrentModel(root, itemID string, selection ModelSelection) error {
	data, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(supervisorDir(root), "model-"+itemID+".json"), data)
}

// DiscoverModels lists provider choices without creating another supervisor.
func DiscoverModels(ctx context.Context, provider Provider) ([]ModelOption, error) {
	switch provider {
	case ProviderCodex:
		return newCodexHarness().AvailableModels(ctx)
	case ProviderClaude:
		return newClaudeHarness().AvailableModels(ctx)
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}
