package supervisor

import "time"

// SessionSnapshot is the non-secret session metadata displayed by attached
// clients. It deliberately omits provider conversation IDs and prompt cursors.
type SessionSnapshot struct {
	Provider  Provider  `json:"provider"`
	Model     string    `json:"model"`
	Effort    string    `json:"effort"`
	UpdatedAt time.Time `json:"updated_at"`
	Stale     bool      `json:"stale"`
}

// ReadSessionSnapshot reads persisted state without constructing a supervisor:
// constructing one would recover dispatches and sweep another host's live logs.
func ReadSessionSnapshot(root, itemID string) (SessionSnapshot, error) {
	sf, err := loadItemSession(root, itemID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	return SessionSnapshot{sf.Provider, sf.Model, sf.Effort, sf.UpdatedAt, sessionIsStale(sf, time.Now())}, nil
}
