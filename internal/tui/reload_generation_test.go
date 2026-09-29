package tui

import (
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestItemsLoadedIgnoresStaleGeneration(t *testing.T) {
	current := mkItem("current", models.StatusActive)
	stale := mkItem("stale", models.StatusActive)
	m := newModel(nil, nil, nil)
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m.itemsLoadGeneration = 2

	next, _ := m.Update(itemsLoadedMsg{
		allItems:    []models.Item{current},
		view:        m.view,
		showBacklog: true,
		generation:  2,
	})
	m = next.(model)
	if got := m.selectedID(); got != current.ID {
		t.Fatalf("current load selected %q, want %q", got, current.ID)
	}

	next, _ = m.Update(itemsLoadedMsg{
		allItems:    []models.Item{stale},
		view:        m.view,
		showBacklog: true,
		generation:  1,
	})
	m = next.(model)
	if got := m.selectedID(); got != current.ID {
		t.Fatalf("stale load replaced selection with %q, want %q", got, current.ID)
	}
	if len(m.items) != 1 || m.items[0].ID != current.ID {
		t.Fatalf("stale load replaced items with %+v", m.items)
	}
}
