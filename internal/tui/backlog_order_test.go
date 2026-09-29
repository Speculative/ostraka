package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestPrepareGroupedUsesExplicitBacklogOrderOnlyForParkedFamilies(t *testing.T) {
	items := []models.Item{
		mkItem("a", models.StatusBacklog),
		mkItem("b", models.StatusBacklog),
		mkItem("c", models.StatusBacklog),
	}
	got, _ := channelView(models.ChannelInbox).prepareGroupedWithOrder(items, true, nil, []string{"c", "a", "b"})
	if ids := itemIDs(got); !reflect.DeepEqual(ids, []string{"c", "a", "b"}) {
		t.Fatalf("explicit backlog order = %v, want c,a,b", ids)
	}

	items[0].Status = models.StatusActive
	items = append(items, models.Item{
		ID: "a-child", Parent: "a", Channel: models.ChannelInbox,
		Status: models.StatusPendingAgent, Created: t0, Title: "a child",
	})
	got, _ = channelView(models.ChannelInbox).prepareGroupedWithOrder(items, true, nil, []string{"c", "a", "b"})
	if ids := itemIDs(got); !reflect.DeepEqual(ids, []string{"a", "a-child", "c", "b"}) {
		t.Fatalf("urgent child ignored explicit backlog order = %v", ids)
	}
}

func TestBacklogMoveModePersistsFamilyPriority(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateItem(models.ChannelInbox, "a", "a", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateItem(models.ChannelInbox, "b", "b", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateItem(models.ChannelInbox, "c", "c", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.ListItems(store.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m.allItems = all
	order, err := s.BacklogOrder()
	if err != nil {
		t.Fatal(err)
	}
	m.items, _ = m.view.prepareGroupedWithOrder(all, true, m.collapsed, order)
	m.selected = 1 // b

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = next.(model)
	if !m.backlogMoveMode {
		t.Fatal("v did not enter backlog move mode")
	}
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = next.(model)
	if got, want := m.selectedID(), b.ID; got != want {
		t.Fatalf("selection after moving = %q, want %q", got, want)
	}
	order, err = s.BacklogOrder()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{a.ID, c.ID, b.ID}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order after j = %v, want a,c,b", order)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = next.(model)
	if m.backlogMoveMode {
		t.Fatal("v did not leave backlog move mode")
	}
}

func TestBacklogMoveModeRejectsFamilyPromotedByChildAttention(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateItem(models.ChannelInbox, "root", "root", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubthread(root.ID, "urgent child", "child", models.TypeThread, models.StatusPendingAgent); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListItems(store.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m.allItems = all
	order, err := s.BacklogOrder()
	if err != nil {
		t.Fatal(err)
	}
	m.items, _ = m.view.prepareGroupedWithOrder(all, true, m.collapsed, order)
	m.selected = 0

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	got := next.(model)
	if got.backlogMoveMode {
		t.Fatal("v entered move mode for a family promoted by child attention")
	}
	if got.err == nil {
		t.Fatal("v did not explain why the promoted family could not be prioritized")
	}
}

func TestBacklogMoveModeExplainsChildUnderArchivedRoot(t *testing.T) {
	root := mkItem("archived-root", models.StatusArchived)
	child := mkItem("live-child", models.StatusBacklog)
	child.Parent = root.ID
	all := []models.Item{root, child}

	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m.allItems = all
	m.items, _ = m.view.prepareGroupedWithOrder(all, true, m.collapsed, nil)
	m.selected = 0

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	got := next.(model)
	if got.backlogMoveMode {
		t.Fatal("v entered move mode for a child under an archived root")
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "parent \"archived-root\" is archived") {
		t.Fatalf("v error = %v, want archived-parent guidance", got.err)
	}
}

func itemIDs(items []models.Item) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
