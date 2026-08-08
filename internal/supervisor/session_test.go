package supervisor

import (
	"os"
	"testing"
)

func TestSessionDefaultsLegacyFilesToClaude(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPath(root), []byte(`{"session_id":"legacy"}`), 0644); err != nil {
		t.Fatal(err)
	}
	sf, err := loadSession(root)
	if err != nil {
		t.Fatal(err)
	}
	if sf.Provider != ProviderClaude || sf.SessionID != "legacy" {
		t.Errorf("got %+v, want legacy Claude session", sf)
	}
}

func TestSessionRoundTripsProvider(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(root, sessionFile{Provider: ProviderCodex, SessionID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	sf, err := loadSession(root)
	if err != nil {
		t.Fatal(err)
	}
	if sf.Provider != ProviderCodex || sf.SessionID != "thread-1" {
		t.Errorf("got %+v", sf)
	}
}
