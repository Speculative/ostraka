package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestReadingPaneNavigationSelectsItemsAndKeepsListHighlight(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width = 100
	m.height = 30
	m.items = []models.Item{
		{ID: "one", Channel: models.ChannelInbox, Status: models.StatusActive, Title: "one"},
		{ID: "two", Channel: models.ChannelInbox, Status: models.StatusActive, Title: "two"},
	}
	m.allItems = append([]models.Item(nil), m.items...)
	m.selected = 0
	m.conv.Width = 70
	m.conv.Height = 20
	m.updateConv()

	listBefore, _ := m.renderList(20)
	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.focus != focusReadingPane {
		t.Fatalf("focus after Right = %v, want reading pane", m.focus)
	}

	listDuringReading, _ := m.renderList(20)
	if listDuringReading != listBefore {
		t.Fatal("selected list row changed when focus moved to the reading pane")
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.selected != 0 || m.focus != focusReadingPane {
		t.Fatalf("after Down in reading pane: selected=%d focus=%v, want item 0 and reading pane", m.selected, m.focus)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(model)
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.selected != 1 || m.focus != focusItemList {
		t.Fatalf("after Down in item list: selected=%d focus=%v, want item 1 and item list", m.selected, m.focus)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.focus != focusReadingPane {
		t.Fatalf("focus after Right = %v, want reading pane", m.focus)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	m = next.(model)
	if m.focus != focusItemList {
		t.Fatalf("focus after application-mode Left = %v, want item list", m.focus)
	}
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyShiftRight})
	m = next.(model)
	if m.focus != focusReadingPane {
		t.Fatalf("focus after application-mode Right = %v, want reading pane", m.focus)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m = next.(model)
	if m.focus != focusItemList {
		t.Fatalf("focus after h = %v, want item list", m.focus)
	}
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = next.(model)
	if m.focus != focusReadingPane {
		t.Fatalf("focus after l = %v, want reading pane", m.focus)
	}
}

func TestPaneFocusIsNotRenderedInHeader(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width = 120

	header := m.renderHeader()
	if strings.Contains(header, "focus") {
		t.Fatalf("initial header contains focus indicator = %q", header)
	}

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.focus != focusReadingPane {
		t.Fatalf("focus after Right = %v, want reading pane", m.focus)
	}
	header = m.renderHeader()
	if strings.Contains(header, "focus") {
		t.Fatalf("reading header contains focus indicator = %q", header)
	}
}

func TestReadingPaneUpDownSelectsTurnsWithoutChangingItem(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	m := newModel(nil, nil, nil)
	m.items = []models.Item{{
		ID:      "one",
		Channel: models.ChannelInbox,
		Status:  models.StatusActive,
		Title:   "one",
		Body:    "body",
		Turns: []models.Turn{
			{Actor: models.ActorUser, Timestamp: t0, Content: "first turn"},
			{Actor: models.ActorAgent, Timestamp: t0.Add(time.Minute), Content: "second turn"},
		},
	}}
	m.allItems = append([]models.Item(nil), m.items...)
	m.selected = 0
	m.conv.Width = 70
	m.conv.Height = 20
	m.updateConv()

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.selected != 0 || m.convSelection != 1 {
		t.Fatalf("after Right: item=%d turn=%d, want item 0 and latest turn 1", m.selected, m.convSelection)
	}
	if !strings.Contains(m.conv.View(), "\x1b[48;5;237m") {
		t.Fatalf("selected turn has no selection background: %q", m.conv.View())
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.selected != 0 || m.convSelection != 0 {
		t.Fatalf("after Up: item=%d turn=%d, want item 0 and turn 0", m.selected, m.convSelection)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.selected != 0 || m.convSelection != 1 {
		t.Fatalf("after Down: item=%d turn=%d, want item 0 and turn 1", m.selected, m.convSelection)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(model)
	if m.selected != 0 || m.convSelection != 0 {
		t.Fatalf("after k in reading pane: item=%d turn=%d, want item 0 and turn 0", m.selected, m.convSelection)
	}
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(model)
	if m.selected != 0 || m.convSelection != 1 {
		t.Fatalf("after j in reading pane: item=%d turn=%d, want item 0 and turn 1", m.selected, m.convSelection)
	}
}

func TestSpaceFoldsFamiliesInItemListAndTracesInReadingPane(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "one", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{
		ID:      "trace-one",
		Status:  "interrupted",
		Content: "partial trace",
	}); err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.allItems = append([]models.Item(nil), m.items...)
	m.selected = 0

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	if !m.collapsed[item.ID] {
		t.Fatal("Space in the item list did not fold the selected family")
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	if m.convSelection < 0 || !m.traceExpanded[traceSelectionKey(item.ID, m.convSelectable[m.convSelection])] {
		t.Fatal("Space in the reading pane did not expand the selected trace")
	}
}
