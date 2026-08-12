package store

import (
	"os"
	"testing"
)

func TestDraftRoundTripAndClear(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "20260812-123456"
	const content = "first line\n\nsecond line"
	if err := s.SaveDraft(id, content); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	got, err := s.LoadDraft(id)
	if err != nil || got != content {
		t.Fatalf("LoadDraft = %q, %v; want %q, nil", got, err, content)
	}
	info, err := os.Stat(s.draftPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("draft permissions = %o, want 600", info.Mode().Perm())
	}
	if err := s.ClearDraft(id); err != nil {
		t.Fatalf("ClearDraft: %v", err)
	}
	if got, err := s.LoadDraft(id); err != nil || got != "" {
		t.Errorf("LoadDraft after clear = %q, %v; want empty, nil", got, err)
	}
}
