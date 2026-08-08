package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func supervisorDir(root string) string {
	return filepath.Join(root, "supervisor")
}

func sessionPath(root string) string {
	return filepath.Join(supervisorDir(root), "session.json")
}

type sessionFile struct {
	Provider  Provider  `json:"provider"`
	SessionID string    `json:"session_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Provider names a harness supported by the supervisor. It is persisted with
// the session because a session ID is meaningful only to the harness that
// created it.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
)

func (p Provider) valid() bool { return p == ProviderClaude || p == ProviderCodex }

// loadSessionID returns "" (no error) if no session has been persisted yet —
// callers treat that as "start a fresh harness session". A malformed file is
// returned as an error; callers should fall back to "" rather than treat it
// as fatal, since losing session continuity is cheaper than getting stuck.
func loadSession(root string) (sessionFile, error) {
	b, err := os.ReadFile(sessionPath(root))
	if os.IsNotExist(err) {
		return sessionFile{Provider: ProviderClaude}, nil
	}
	if err != nil {
		return sessionFile{}, err
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return sessionFile{}, err
	}
	// Files written before provider support resumed Claude by default.
	if sf.Provider == "" {
		sf.Provider = ProviderClaude
	}
	if !sf.Provider.valid() {
		return sessionFile{}, fmt.Errorf("unknown provider %q", sf.Provider)
	}
	return sf, nil
}

func loadSessionID(root string) (string, error) {
	sf, err := loadSession(root)
	return sf.SessionID, err
}

func saveSession(root string, sf sessionFile) error {
	sf.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionPath(root), b, 0644)
}

func saveSessionID(root, id string) error {
	return saveSession(root, sessionFile{Provider: ProviderClaude, SessionID: id})
}

// sessionGuard serializes session reads and writes within a supervisor. A new
// session selected while a turn is running must not be overwritten by that
// turn's now-stale completion result.
type sessionGuard struct{ mu sync.Mutex }
