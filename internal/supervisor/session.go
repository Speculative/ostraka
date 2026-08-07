package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func supervisorDir(root string) string {
	return filepath.Join(root, "supervisor")
}

func sessionPath(root string) string {
	return filepath.Join(supervisorDir(root), "session.json")
}

type sessionFile struct {
	SessionID string    `json:"session_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// loadSessionID returns "" (no error) if no session has been persisted yet —
// callers treat that as "start a fresh harness session". A malformed file is
// returned as an error; callers should fall back to "" rather than treat it
// as fatal, since losing session continuity is cheaper than getting stuck.
func loadSessionID(root string) (string, error) {
	b, err := os.ReadFile(sessionPath(root))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return "", err
	}
	return sf.SessionID, nil
}

func saveSessionID(root, id string) error {
	b, err := json.MarshalIndent(sessionFile{SessionID: id, UpdatedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionPath(root), b, 0644)
}
