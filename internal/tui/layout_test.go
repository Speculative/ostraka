package tui

import (
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func resizeKey(m model, key string) model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(model)
}

func TestPaneResizeAcrossViewsAndFocus(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width, m.height = 120, 30
	m = m.recalcLayout()
	for _, view := range []listView{channelView(models.ChannelInbox), archiveView} {
		m.view = view
		for _, pane := range []int{0, 1, 2, reviewProjectPane} {
			m.projectPane = pane
			for _, focus := range []paneFocus{focusItemList, focusReadingPane} {
				m.focus = focus
				m = m.recalcLayout()
				before := m.listWidth()
				m = resizeKey(m, ">")
				if m.listWidth() != before+2 || m.conv.Width != m.width-m.listWidth()-3 {
					t.Fatalf("view %v pane %d focus %v: inconsistent resized layout", view, pane, focus)
				}
				if got := lipgloss.Width(m.View()); got != m.width {
					t.Fatalf("resized view width = %d, want %d", got, m.width)
				}
			}
		}
	}
	m = resizeKey(m, "<")
	if m.listWidth() != 66 {
		t.Fatalf("shared width after view changes = %d, want 66", m.listWidth())
	}
}

func TestPaneResizeBoundsAndTerminalResize(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width, m.height = 80, 24
	m = m.recalcLayout()
	for range 50 {
		m = resizeKey(m, ">")
	}
	if m.listWidth() != 76 || m.conv.Width != 1 {
		t.Fatalf("maximum split: list %d, reader %d", m.listWidth(), m.conv.Width)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = next.(model)
	if m.listWidth() > 36 || m.listWidth()+1+m.conv.Width+2 > 40 {
		t.Fatal("terminal shrink left an oversized pane")
	}
	for range 50 {
		m = resizeKey(m, "<")
	}
	if m.listWidth() != 24 {
		t.Fatalf("minimum width = %d, want 24", m.listWidth())
	}
	m = resizeKey(m, ">")
	if m.listWidth() != 26 {
		t.Fatal("resize at the minimum accumulated invisible key presses")
	}
}

func TestPaneResizeRefreshesHelpListCache(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width, m.height = 120, 30
	m = m.recalcLayout()
	m = resizeKey(m, "?")
	before := m.modalListPanelCache
	m = resizeKey(m, ">")
	if !m.modalListPanelCacheValid || m.modalListPanelCache == before {
		t.Fatal("help retained its old list geometry")
	}
	if m.shortcuts.Width != m.width-m.listWidth()-3 || lipgloss.Width(m.View()) != m.width {
		t.Fatal("help did not adopt the resized split")
	}
}

func TestPaneResizeKeysRemainTextInSearchAndPaste(t *testing.T) {
	for _, review := range []bool{false, true} {
		m := newModel(nil, nil, nil)
		m.width, m.height = 120, 30
		m = m.recalcLayout()
		if review {
			m.projectPane = reviewProjectPane
			m.reviewSearching = true
		} else {
			m.mode = modeFilter
		}
		m.filterInput.Focus()
		m = resizeKey(m, ">")
		m = resizeKey(m, "<")
		if m.filterInput.Value() != "><" || m.listWidth() != 36 {
			t.Fatal("resize shortcuts intercepted search text")
		}
	}
	m := newModel(nil, nil, nil)
	m.width, m.height = 120, 30
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(">"), Paste: true})
	if next.(model).listWidth() != 36 {
		t.Fatal("pasted shortcut resized the pane")
	}
}

func TestPaneResizeKeysRemainTextInEditors(t *testing.T) {
	for _, mode := range []uiMode{modeCompose, modeTitle} {
		m := newModel(nil, nil, nil)
		m.width, m.height = 120, 30
		m.mode = mode
		m.input.Focus()
		m.title.Focus()
		m = m.recalcLayout()
		m = resizeKey(m, ">")
		m = resizeKey(m, "<")
		value := m.input.Value()
		if mode == modeTitle {
			value = m.title.Value()
		}
		if value != "><" || m.listWidth() != 36 {
			t.Fatalf("mode %v: resize keys intercepted editor text %q", mode, value)
		}
	}
}
