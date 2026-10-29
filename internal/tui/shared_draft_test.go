package tui

import (
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestSharedDraftClearDoesNotBounceFromComposer(t *testing.T) {
	for _, event := range []string{"safety", "refresh", "close"} {
		t.Run(event, func(t *testing.T) {
			s, err := store.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			item, err := s.CreateItem(models.ChannelInbox, "shared draft", "body", models.TypeThread, models.StatusActive, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SaveDraft(item.ID, "First"); err != nil {
				t.Fatal(err)
			}
			m := newModel(s, nil, nil)
			m.items = []models.Item{item}
			next, _ := m.openComposer()
			m = next.(model)
			// The web client sends its full turn and removes the shared draft.
			if err := s.ClearDraft(item.ID); err != nil {
				t.Fatal(err)
			}
			switch event {
			case "safety":
				next, _ = m.Update(draftSafetyMsg{})
				m = next.(model)
			case "refresh":
				m.refreshPendingDraft()
			case "close":
				m.checkpointTurnDraft()
			}
			if m.input.Value() != "" {
				t.Fatalf("composer restored stale text: %q", m.input.Value())
			}
			// Repeated checkpoints must not recreate the removed draft.
			m.checkpointTurnDraft()
			if got, err := s.LoadDraft(item.ID); err != nil || got != "" {
				t.Fatalf("shared draft = %q, %v", got, err)
			}
		})
	}
}

func TestSharedDraftRefreshPreservesLocalEdits(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "shared draft", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	next, _ := m.openComposer()
	m = next.(model)
	if err := s.SaveDraft(item.ID, "from web"); err != nil {
		t.Fatal(err)
	}
	m.refreshPendingDraft()
	if m.input.Value() != "from web" {
		t.Fatalf("clean composer = %q", m.input.Value())
	}
	m.input.SetValue("local unsaved edit")
	if err := s.ClearDraft(item.ID); err != nil {
		t.Fatal(err)
	}
	m.refreshPendingDraft()
	if m.input.Value() != "local unsaved edit" {
		t.Fatalf("lost local edit: %q", m.input.Value())
	}
	m.checkpointTurnDraft()
	if got, err := s.LoadDraft(item.ID); err != nil || got != "local unsaved edit" {
		t.Fatalf("local edit not saved: %q, %v", got, err)
	}
	// Once saved, this editor must follow a later external clear.
	if err := s.ClearDraft(item.ID); err != nil {
		t.Fatal(err)
	}
	m.checkpointTurnDraft()
	if m.input.Value() != "" {
		t.Fatalf("saved editor did not follow external clear: %q", m.input.Value())
	}
}

func TestIndependentComposersFollowSharedDraft(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "shared", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	clients := make([]model, 2)
	for i := range clients {
		m := newModel(s, nil, nil)
		m.items = []models.Item{item}
		next, _ := m.openComposer()
		clients[i] = next.(model)
	}
	for _, writer := range []int{0, 1} {
		clients[writer].input.SetValue("edited in either composer")
		clients[writer].checkpointTurnDraft()
		clients[1-writer].refreshPendingDraft()
		if clients[1-writer].input.Value() != "edited in either composer" {
			t.Fatal("peer did not receive draft")
		}
		if _, err := s.AddTurn(item.ID, models.ActorUser, clients[1-writer].input.Value()); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearDraft(item.ID); err != nil {
			t.Fatal(err)
		}
		for i := range clients {
			clients[i].refreshPendingDraft()
			clients[i].checkpointTurnDraft()
			if clients[i].input.Value() != "" {
				t.Fatal("sent draft survived in a composer")
			}
		}
		if draft, err := s.LoadDraft(item.ID); err != nil || draft != "" {
			t.Fatalf("sent draft bounced back: %q, %v", draft, err)
		}
	}
}
