package supervisor

import (
	"bytes"
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

// Claude Code automatically uses a one-hour cache TTL on a subscription. It
// falls back to five minutes while spending usage credits, which the harness
// cannot observe, so this remains a recommendation rather than a guarantee.
const claudeSubscriptionCacheTTL = time.Hour

// GPT-5.6 caches remain eligible for reuse for 30 minutes after their last
// write or reuse. OpenAI may retain a cache longer, so this too is only a
// recommendation threshold.
const codexCacheTTL = 30 * time.Minute

type sessionFile struct {
	Provider Provider `json:"provider"`
	// Model applies only when SessionID is empty: it selects the model for
	// the next fresh session launch. A resumed session ignores it and keeps
	// whatever model it already started with.
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	SessionID string `json:"session_id"`
	// PromptedTurns is the item-turn boundary included in the most recent
	// provider prompt. Nil identifies session files written before this cursor
	// was introduced.
	PromptedTurns *int      `json:"prompted_turns,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// sessionsFile keeps each item in its own provider conversation. The old
// flat form is deliberately not migrated into an arbitrary item: doing so
// would preserve precisely the cross-item context sharing this replaces.
type sessionsFile struct {
	Sessions        map[string]sessionFile `json:"sessions"`
	DefaultProvider Provider               `json:"default_provider,omitempty"`
	ModelDefaults   map[Provider]string    `json:"model_defaults,omitempty"`
	EffortDefaults  map[Provider]string    `json:"effort_defaults,omitempty"`
}

// Provider names a harness supported by the supervisor. A session ID is
// meaningful only to the provider that created it.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
)

func (p Provider) valid() bool { return p == ProviderClaude || p == ProviderCodex }

func loadSessions(root string) (sessionsFile, error) {
	b, err := os.ReadFile(sessionPath(root))
	if os.IsNotExist(err) {
		return sessionsFile{Sessions: make(map[string]sessionFile)}, nil
	}
	if err != nil {
		return sessionsFile{}, err
	}
	var sf sessionsFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return sessionsFile{}, err
	}
	if sf.Sessions == nil {
		// This is a pre-per-item file. Do not resume it for a new item.
		return sessionsFile{Sessions: make(map[string]sessionFile)}, nil
	}
	for itemID, session := range sf.Sessions {
		if session.Provider == "" {
			session.Provider = ProviderClaude
			sf.Sessions[itemID] = session
		}
		if !session.Provider.valid() {
			return sessionsFile{}, fmt.Errorf("item %s: unknown provider %q", itemID, session.Provider)
		}
	}
	return sf, nil
}

func loadItemSession(root, itemID string) (sessionFile, error) {
	sessions, err := loadSessions(root)
	if err != nil {
		return sessionFile{}, err
	}
	return itemSession(sessions, itemID), nil
}

func itemSession(sessions sessionsFile, itemID string) sessionFile {
	session, ok := sessions.Sessions[itemID]
	if !ok {
		provider := sessions.DefaultProvider
		if !provider.valid() {
			provider = ProviderClaude
		}
		return sessionFile{Provider: provider, Model: sessions.ModelDefaults[provider], Effort: sessions.EffortDefaults[provider]}
	}
	return session
}

func loadEffortDefault(root string, provider Provider) (string, error) {
	sessions, err := loadSessions(root)
	if err != nil {
		return "", err
	}
	return sessions.EffortDefaults[provider], nil
}

func loadModelDefault(root string, provider Provider) (string, error) {
	sessions, err := loadSessions(root)
	if err != nil {
		return "", err
	}
	return sessions.ModelDefaults[provider], nil
}

// mutateSessions protects the full read/modify/write transaction; readers see
// atomic snapshots, including when the web server attaches to the TUI.
func mutateSessions(root string, update func(*sessionsFile) error) error {
	unlock, err := lockSessions(root)
	if err != nil {
		return err
	}
	defer unlock()
	sessions, err := loadSessions(root)
	if err != nil {
		return err
	}
	before, err := json.Marshal(sessions)
	if err != nil {
		return err
	}
	if err := update(&sessions); err != nil {
		return err
	}
	after, err := json.Marshal(sessions)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(sessionPath(root), data)
}

func saveItemSession(root, itemID string, session sessionFile) error {
	if !session.Provider.valid() {
		return fmt.Errorf("unknown provider %q", session.Provider)
	}
	return mutateSessions(root, func(s *sessionsFile) error {
		session.UpdatedAt = time.Now().UTC()
		s.Sessions[itemID] = session
		return nil
	})
}

// SelectNextSession sets the next turn's provider/model in a fresh context.
// It never starts or interrupts an agent and is safe from an attached server.
func SelectNextSession(root, itemID string, provider Provider, model, effort string) error {
	if !provider.valid() {
		return fmt.Errorf("unknown provider %q", provider)
	}
	return mutateSessions(root, func(s *sessionsFile) error {
		s.Sessions[itemID] = sessionFile{Provider: provider, Model: model, Effort: effort, UpdatedAt: time.Now().UTC()}
		if s.ModelDefaults == nil {
			s.ModelDefaults = make(map[Provider]string)
		}
		if s.EffortDefaults == nil {
			s.EffortDefaults = make(map[Provider]string)
		}
		s.ModelDefaults[provider] = model
		s.EffortDefaults[provider] = effort
		s.DefaultProvider = provider
		return nil
	})
}

func prepareItemSession(root, itemID string) (sessionFile, error) {
	var selected sessionFile
	err := mutateSessions(root, func(s *sessionsFile) error {
		selected = itemSession(*s, itemID)
		if sessionIsStale(selected, time.Now()) {
			selected.SessionID = ""
			selected.PromptedTurns = nil
			selected.UpdatedAt = time.Now().UTC()
			s.Sessions[itemID] = selected
		}
		return nil
	})
	return selected, err
}

func sessionIsStale(session sessionFile, now time.Time) bool {
	var ttl time.Duration
	switch session.Provider {
	case ProviderClaude:
		ttl = claudeSubscriptionCacheTTL
	case ProviderCodex:
		ttl = codexCacheTTL
	default:
		return false
	}
	return session.SessionID != "" && !session.UpdatedAt.IsZero() && now.Sub(session.UpdatedAt) >= ttl
}

// sessionGuard serializes session reads and writes within a supervisor. A new
// session selected while a turn is running must not be overwritten by that
// turn's now-stale completion result.
type sessionGuard struct{ mu sync.Mutex }
