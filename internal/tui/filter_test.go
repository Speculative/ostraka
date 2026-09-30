package tui

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestFilterItemsSearchesFullItemTextAndFields(t *testing.T) {
	items := []models.Item{
		{
			ID: "one", Status: models.StatusBacklog, Group: "v2", Title: "Cache rollout",
			Body:  "Deploy the worker",
			Turns: []models.Turn{{Content: "The retry path still needs coverage"}},
		},
		{ID: "two", Status: models.StatusBacklog, Group: "v1", Title: "Unrelated", Body: "Cache is not this item"},
		{ID: "three", Status: models.StatusActive, Group: "v2", Title: "Cache rollout"},
	}

	got := filterItems(items, "retry group:v2 status:backlog")
	if len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("filtered IDs = %v, want [one]", itemIDs(got))
	}

	got = filterItems(items, `"worker" group:v2`)
	if len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("quoted filtered IDs = %v, want [one]", itemIDs(got))
	}
}

func TestFilterItemsSupportsUngroupedAndLegacyDone(t *testing.T) {
	items := []models.Item{
		{ID: "empty", Status: models.StatusActive},
		{ID: "grouped", Status: models.StatusActive, Group: "v2"},
		{ID: "old", Status: "done"},
	}
	if got := filterItems(items, "group:none"); len(got) != 2 || got[0].ID != "empty" || got[1].ID != "old" {
		t.Fatalf("group:none IDs = %v, want [empty old]", itemIDs(got))
	}
	if got := filterItems(items, "status:archived"); len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("status:archived IDs = %v, want [old]", itemIDs(got))
	}
}

func TestFilterFieldClausesAreInactiveUntilTheyHaveAValueAndThenUsePrefixes(t *testing.T) {
	items := []models.Item{
		{ID: "v2", Group: "v2", Status: models.StatusPendingUser, Channel: models.ChannelInbox, Title: "gadget"},
		{ID: "v3", Group: "v3", Status: models.StatusPendingAgent, Channel: models.ChannelInbox},
		{ID: "other", Group: "other", Status: models.StatusActive, Channel: models.ChannelInbox},
	}

	for _, query := range []string{"group:", "status:", "channel:"} {
		if got := filterItems(items, query); len(got) != len(items) {
			t.Errorf("%q matched %v, want all items", query, itemIDs(got))
		}
	}
	for _, query := range []string{"g", "gr", "gro", "grou", "group"} {
		if got := filterItems(items, query); len(got) != len(items) {
			t.Errorf("incomplete field %q matched %v, want all items", query, itemIDs(got))
		}
	}
	if got := filterItems(items, "group:v"); len(got) != 2 || got[0].ID != "v2" || got[1].ID != "v3" {
		t.Errorf("group:v matched %v, want [v2 v3]", itemIDs(got))
	}
	if got := filterItems(items, "group:v2"); len(got) != 1 || got[0].ID != "v2" {
		t.Errorf("group:v2 matched %v, want [v2]", itemIDs(got))
	}
	if got := filterItems(items, "status:p"); len(got) != 2 || got[0].ID != "v2" || got[1].ID != "v3" {
		t.Errorf("status:p matched %v, want [v2 v3]", itemIDs(got))
	}
	if got := filterItems(items, "channel:i"); len(got) != len(items) {
		t.Errorf("channel:i matched %v, want all inbox items", itemIDs(got))
	}
	if got := filterItems(items, "group:does-not-exist"); len(got) != 0 {
		t.Errorf("group:does-not-exist matched %v, want no items", itemIDs(got))
	}
}

func TestFilterQueryCanBeCancelledAndApplied(t *testing.T) {
	items := []models.Item{
		{ID: "one", Channel: models.ChannelInbox, Status: models.StatusActive, Title: "first"},
		{ID: "two", Channel: models.ChannelInbox, Status: models.StatusActive, Title: "second"},
	}
	m := newModel(nil, nil, &fakeSupervisor{})
	m.width, m.height = 100, 20
	m.view = channelView(models.ChannelInbox)
	m.showBacklog = true
	m.allItems = append([]models.Item(nil), items...)
	m.items = append([]models.Item(nil), items...)
	m.selected = 1

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(model)
	if m.mode != modeFilter {
		t.Fatalf("filter mode = %v, want modeFilter", m.mode)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first")})
	m = next.(model)
	if len(m.items) != 1 || m.items[0].ID != "one" {
		t.Fatalf("live filter items = %v, want [one]", itemIDs(m.items))
	}
	if m.listBaseAvailRows() != 15 { // height - header/footer - panel padding - filter row
		t.Fatalf("list rows with filter = %d, want 15", m.listBaseAvailRows())
	}
	if plain := ansi.Strip(m.View()); !strings.Contains(plain, "/ first") {
		t.Fatalf("active filter row missing from view: %q", plain)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.mode != modeNav || m.filterQuery != "" || len(m.items) != 2 || m.selected != 1 {
		t.Fatalf("cancelled filter state = mode %v query %q items %v selected %d", m.mode, m.filterQuery, itemIDs(m.items), m.selected)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeNav || m.filterQuery != "second" || len(m.items) != 1 || m.items[0].ID != "two" {
		t.Fatalf("applied filter state = mode %v query %q items %v", m.mode, m.filterQuery, itemIDs(m.items))
	}
	if plain := ansi.Strip(m.View()); !strings.Contains(plain, "/ second") {
		t.Fatalf("applied filter row missing from view: %q", plain)
	}
}
