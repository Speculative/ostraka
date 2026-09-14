package store

import (
	"os"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
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

func TestNewItemDraftRoundTripAndClear(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := NewItemDraft{
		Channel:     models.ChannelInbox,
		Parent:      "20260812-123456",
		RelatedFrom: "20260812-654321",
		Title:       "unfinished item",
		Body:        "unfinished\nbody",
		BodyStarted: true,
	}
	if err := s.SaveNewItemDraft(want); err != nil {
		t.Fatalf("SaveNewItemDraft: %v", err)
	}
	got, err := s.LoadNewItemDraft()
	if err != nil || got != want {
		t.Fatalf("LoadNewItemDraft = %#v, %v; want %#v, nil", got, err, want)
	}
	info, err := os.Stat(s.newItemDraftPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("new item draft permissions = %o, want 600", info.Mode().Perm())
	}
	if err := s.ClearNewItemDraft(); err != nil {
		t.Fatalf("ClearNewItemDraft: %v", err)
	}
	if got, err := s.LoadNewItemDraft(); err != nil || got != (NewItemDraft{}) {
		t.Errorf("LoadNewItemDraft after clear = %#v, %v; want zero, nil", got, err)
	}
	if err := s.SaveNewItemDraft(NewItemDraft{Channel: models.ChannelInbox, Title: "  \t"}); err != nil {
		t.Fatalf("SaveNewItemDraft blank: %v", err)
	}
	if _, err := os.Stat(s.newItemDraftPath()); !os.IsNotExist(err) {
		t.Errorf("blank new item draft file exists: %v", err)
	}
}
