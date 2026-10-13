package tui

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func vimTestModel(text string, cursor int) model {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.input.Focus()
	m.input.SetValue(text)
	setTextareaCursorOffset(&m.input, cursor)
	m.vim.normal = true
	return m
}

func vimTestKeys(m model, keys ...string) model {
	for _, key := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		switch key {
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		}
		next, _ := m.handleInputKey(msg)
		m = next.(model)
	}
	return m
}

func TestVimMotions(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		cursor     int
		keys       []string
		want       int
	}{
		{"count and word", "one two three", 0, []string{"2", "w"}, 8},
		{"word punctuation", "one,two three", 0, []string{"w"}, 3},
		{"big word", "one,two three", 0, []string{"W"}, 8},
		{"unicode word", "αβ 文本", 0, []string{"w"}, 3},
		{"word end", "one two", 0, []string{"e"}, 2},
		{"word backward", "one two", 4, []string{"b"}, 0},
		{"empty word", "", 0, []string{"b", "e", "w"}, 0},
		{"horizontal boundary", "abc\ndef", 0, []string{"9", "l"}, 2},
		{"first nonblank", "  abc", 4, []string{"^"}, 2},
		{"line end", "abc\ndef", 0, []string{"$"}, 2},
		{"count line end", "abc\ndef", 0, []string{"2", "$"}, 6},
		{"last line", "abc\n  def", 0, []string{"G"}, 4},
		{"numbered line", "a\nb\nc", 4, []string{"2", "g", "g"}, 2},
		{"numbered G", "a\nb\nc", 0, []string{"2", "G"}, 2},
		{"desired column", "abcd\nx\nabcd", 3, []string{"j", "j"}, 10},
		{"empty line", "abcd\n\nabcd", 3, []string{"j", "j"}, 9},
		{"find count", "a.b.c", 0, []string{"2", "f", "."}, 3},
		{"find repeat", "a.b.c", 0, []string{"f", ".", ";"}, 3},
		{"find reverse", "a.b.c", 0, []string{"2", "f", ".", ","}, 1},
		{"till repeat", "a.b.c", 0, []string{"t", ".", ";"}, 2},
		{"find literal digit", "a2b2c", 0, []string{"f", "2"}, 1},
		{"unmatched find", "abc", 1, []string{"f", "x"}, 1},
		{"unknown prefix", "abc", 1, []string{"g", "q", "l"}, 2},
		{"matching nested bracket", "x(a(b)c)z", 0, []string{"%"}, 7},
		{"reverse bracket", "x(a(b)c)z", 7, []string{"%"}, 1},
		{"multiline bracket", "{\n文\n}", 0, []string{"%"}, 4},
		{"unmatched bracket", "x(ab", 0, []string{"%"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := vimTestKeys(vimTestModel(tt.text, tt.cursor), tt.keys...)
			if got := textareaCursorOffset(m.input); got != tt.want {
				t.Fatalf("cursor = %d, want %d", got, tt.want)
			}
			if m.input.Value() != tt.text || !m.vim.normal {
				t.Fatal("movement changed text or editing mode")
			}
		})
	}
}

func TestVimInsertEntries(t *testing.T) {
	for _, tt := range []struct {
		key, want string
		cursor    int
	}{
		{"i", "  abc\ndef", 3},
		{"a", "  abc\ndef", 4},
		{"I", "  abc\ndef", 2},
		{"A", "  abc\ndef", 5},
		{"o", "  abc\n\ndef", 6},
		{"O", "\n  abc\ndef", 0},
	} {
		t.Run(tt.key, func(t *testing.T) {
			m := vimTestKeys(vimTestModel("  abc\ndef", 3), tt.key)
			if m.vim.normal || m.input.Value() != tt.want || textareaCursorOffset(m.input) != tt.cursor {
				t.Fatalf("entry: normal=%v text=%q cursor=%d", m.vim.normal, m.input.Value(), textareaCursorOffset(m.input))
			}
			m = vimTestKeys(m, "Z", "esc")
			if !m.vim.normal || !strings.Contains(m.input.Value(), "Z") {
				t.Fatal("insert did not return to normal with inserted text")
			}
		})
	}
}

func TestVimMentionEscapePrecedence(t *testing.T) {
	m := vimTestModel("@", 1)
	m.vim.normal = false
	m.allItems = []models.Item{{ID: "abcd-efgh", Title: "Target", Status: models.StatusActive}}
	m.refreshMentionPicker()
	m = vimTestKeys(m, "esc")
	if m.mentionPicker.open || m.vim.normal || m.input.Value() != "@" {
		t.Fatal("first escape must only dismiss the picker")
	}
	m = vimTestKeys(m, "esc")
	if !m.vim.normal || m.mode != modeCompose || m.input.Value() != "@" {
		t.Fatal("second escape must enter normal mode without blurring")
	}
}

func TestVimNormalPasteAndShortcutIsolation(t *testing.T) {
	m := vimTestModel("abc", 0)
	m = vimTestKeys(m, "q", "z", "1", "?", "enter")
	if m.mode != modeCompose || m.input.Value() != "abc" || !m.vim.normal {
		t.Fatal("unbound normal keys leaked into application shortcuts or typing")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q\n@"), Paste: true})
	m = next.(model)
	if m.input.Value() != "q\n@abc" || !m.vim.normal || m.mentionPicker.open {
		t.Fatalf("normal paste: text=%q normal=%v picker=%v", m.input.Value(), m.vim.normal, m.mentionPicker.open)
	}
}

func TestVimEditingCommands(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		cursor     int
		keys       []string
		want       string
	}{
		{"delete word", "one two three", 0, []string{"d", "w"}, "two three"},
		{"operator count", "one two three four", 0, []string{"d", "2", "w"}, "three four"},
		{"multiply counts", "a b c d e f", 0, []string{"2", "d", "2", "w"}, "e f"},
		{"change word", "one two", 0, []string{"c", "w", "new", "esc"}, "new two"},
		{"change inner word", "one two", 5, []string{"c", "i", "w", "new", "esc"}, "one new"},
		{"delete around word", "one two", 5, []string{"d", "a", "w"}, "one"},
		{"inner punctuation", "one,two", 3, []string{"d", "i", "w"}, "onetwo"},
		{"delete line", "a\nb\nc", 2, []string{"d", "d"}, "a\nc"},
		{"delete final line", "a\nb", 2, []string{"d", "d"}, "a"},
		{"delete counted lines", "a\nb\nc", 0, []string{"2", "d", "d"}, "c"},
		{"delete vertical", "a\nb\nc", 0, []string{"d", "j"}, "c"},
		{"change line", "a\nb\nc", 2, []string{"c", "c", "new", "esc"}, "a\nnew\nc"},
		{"change final line", "a\nb", 2, []string{"c", "c", "new", "esc"}, "a\nnew"},
		{"change all lines", "a\nb", 0, []string{"2", "c", "c", "new", "esc"}, "new"},
		{"delete suffix", "abc\ndef", 1, []string{"D"}, "a\ndef"},
		{"change suffix", "abc\ndef", 1, []string{"C", "x", "esc"}, "ax\ndef"},
		{"substitute char", "abc", 1, []string{"s", "x", "esc"}, "axc"},
		{"substitute line", "abc\ndef", 1, []string{"S", "x", "esc"}, "x\ndef"},
		{"delete char", "abc", 1, []string{"x"}, "ac"},
		{"delete last with right motion", "abc", 2, []string{"d", "l"}, "ab"},
		{"delete empty final line", "abc\n", 4, []string{"d", "d"}, "abc"},
		{"adjacent till", "a:b", 0, []string{"d", "t", ":"}, ":b"},
		{"unmatched operator bracket", "abc", 0, []string{"d", "%"}, "abc"},
		{"missing find repeat", "abc", 0, []string{"d", ";"}, "abc"},
		{"delete preceding", "abc", 1, []string{"X"}, "bc"},
		{"replace count", "abcd", 1, []string{"2", "r", "Z"}, "aZZd"},
		{"replace too many", "abc", 1, []string{"3", "r", "Z"}, "abc"},
		{"find replace key", "a r b", 0, []string{"f", "r", "x"}, "a  b"},
		{"delete find", "abc:def", 0, []string{"d", "f", ":"}, "def"},
		{"delete till", "abc:def", 0, []string{"d", "t", ":"}, ":def"},
		{"delete backwards find", "abc:def", 6, []string{"d", "F", ":"}, "abc"},
		{"delete bracket", "a(b)c", 1, []string{"d", "%"}, "ac"},
		{"invalid operator", "abc", 0, []string{"d", "z"}, "abc"},
		{"invalid text object", "abc", 0, []string{"d", "i", "z"}, "abc"},
		{"delete unicode", "αβ 文本", 0, []string{"d", "w"}, "文本"},
		{"put characters", "abc", 0, []string{"x", "p"}, "bac"},
		{"put before", "abc", 0, []string{"x", "P"}, "abc"},
		{"yank and put word", "one two", 0, []string{"y", "w", "P"}, "one one two"},
		{"yank and put line", "a\nb", 0, []string{"y", "y", "p"}, "a\na\nb"},
		{"put final line", "a\nb", 2, []string{"y", "y", "p"}, "a\nb\nb"},
		{"put line before", "a\nb", 2, []string{"y", "y", "P"}, "a\nb\nb"},
		{"put last deleted line", "a\nb", 2, []string{"d", "d", "p"}, "a\nb"},
		{"put count", "ab", 0, []string{"x", "3", "P"}, "aaab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := vimTestKeys(vimTestModel(tt.text, tt.cursor), tt.keys...)
			if m.input.Value() != tt.want || !m.vim.normal {
				t.Fatalf("text=%q normal=%v, want %q in normal mode", m.input.Value(), m.vim.normal, tt.want)
			}
		})
	}
}

func TestVimUndoUnitsAndRedo(t *testing.T) {
	for _, keys := range [][]string{
		{"i", "new", "text", "esc"},
		{"3", "i", "new", "text", "esc"},
		{"o", "new", "text", "esc"},
		{"3", "o", "new", "text", "esc"},
		{"c", "w", "new", "text", "esc"},
		{"d", "w"},
		{"r", "x"},
	} {
		m := vimTestKeys(vimTestModel("one two", 0), keys...)
		changed := m.input.Value()
		m = vimTestKeys(m, "u")
		if m.input.Value() != "one two" {
			t.Fatalf("undo %v yielded %q", keys, m.input.Value())
		}
		m = vimTestKeys(m, "ctrl+r")
		if m.input.Value() != changed {
			t.Fatalf("redo %v yielded %q, want %q", keys, m.input.Value(), changed)
		}
	}
	m := vimTestKeys(vimTestModel("abcd", 0), "x", "x", "2", "u")
	if m.input.Value() != "abcd" {
		t.Fatal("counted undo failed")
	}
	m = vimTestKeys(m, "x", "ctrl+r")
	if m.input.Value() != "bcd" {
		t.Fatal("new edit did not invalidate redo")
	}
}

func TestVimRepeatCompletedMention(t *testing.T) {
	m := vimTestModel("", 0)
	m.vim.normal = false
	m.allItems = []models.Item{{ID: "abcd-efgh", Title: "Target", Status: models.StatusActive}}
	m = vimTestKeys(m, "@", "enter", "esc")
	inserted := m.input.Value()
	if inserted != "@abcd-efgh " {
		t.Fatalf("mention was not completed: %q", inserted)
	}
	m = vimTestKeys(m, ".")
	if m.input.Value() != "@abcd-efgh@abcd-efgh  " || m.mentionPicker.open || !m.vim.normal {
		t.Fatalf("repeated mention text=%q picker=%v normal=%v", m.input.Value(), m.mentionPicker.open, m.vim.normal)
	}
	m = vimTestKeys(m, "u")
	if m.input.Value() != inserted {
		t.Fatalf("repeated mention undo=%q", m.input.Value())
	}
}

func TestVimPasteUndoAndMentionInsertUndo(t *testing.T) {
	m := vimTestModel("abc", 0)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q\n@"), Paste: true})
	m = vimTestKeys(next.(model), "u")
	if m.input.Value() != "abc" {
		t.Fatal("normal paste was not one undo unit")
	}
	m = vimTestModel("", 0)
	m.vim.normal = false
	m.allItems = []models.Item{{ID: "abcd-efgh", Title: "Target", Status: models.StatusActive}}
	m = vimTestKeys(m, "@", "enter", "esc", "u")
	if m.input.Value() != "" {
		t.Fatalf("mention insertion undo yielded %q", m.input.Value())
	}
}

func TestVimCursorShapesPreserveTextAndWidth(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	for _, text := range []string{"", "abc", "文本", "a\u0301bc", "one two three four\nfive"} {
		m := vimTestModel(text, 0)
		m.input.SetWidth(10)
		m.input.SetHeight(4)
		m.input.Cursor.Blink = false
		block := m.turnTextareaView()
		m.vim.normal = false
		line := m.turnTextareaView()
		if ansi.Strip(block) != ansi.Strip(line) || lipgloss.Width(block) != lipgloss.Width(line) || lipgloss.Height(block) != lipgloss.Height(line) {
			t.Fatalf("cursor changed text or geometry for %q: block=%q line=%q", text, block, line)
		}
		if !strings.Contains(block, "[7m") || !strings.Contains(line, "\x1b[4") || strings.Contains(line, "[7m") {
			t.Fatalf("missing block/underline distinction for %q: block=%q line=%q", text, block, line)
		}
	}
}

func TestVimRepeatAndCountedInsert(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		keys       []string
		want       string
	}{
		{"delete different length words", "a longer third", []string{"d", "w", "."}, "third"},
		{"change different length words", "a longer third", []string{"c", "w", "Z", "esc", "w", "."}, "Z Z third"},
		{"insert repeat", "xyz", []string{"i", "a", "esc", "l", "."}, "aaxyz"},
		{"identical insertion anchor", "abc", []string{"i", "a", "esc", "w", "."}, "aabac"},
		{"counted insert", "xyz", []string{"3", "i", "a", "esc"}, "aaaxyz"},
		{"counted append", "xyz", []string{"2", "A", "!", "esc"}, "xyz!!"},
		{"counted open", "xyz", []string{"3", "o", "a", "esc"}, "xyz\na\na\na"},
		{"counted open above", "xyz", []string{"3", "O", "a", "esc"}, "a\na\na\nxyz"},
		{"counted blank open", "xyz", []string{"3", "o", "esc"}, "xyz\n\n\n"},
		{"repeat retains insert count", "xyz", []string{"2", "i", "a", "esc", "l", "."}, "aaaaxyz"},
		{"repeat overrides insert count", "xyz", []string{"2", "i", "a", "esc", "l", "3", "."}, "aaaaaxyz"},
		{"repeat overrides operator count", "a b c d e f g", []string{"d", "2", "w", "3", "."}, "f g"},
		{"repeat last char", "abcd", []string{"x", "."}, "cd"},
		{"repeat replace", "abcd", []string{"r", "Z", "l", "."}, "ZZcd"},
		{"repeat change without inserted text", "one two three", []string{"c", "w", "esc", "w", "."}, "  three"},
		{"repeat survives movement and undo", "abcd", []string{"x", "u", "l", "."}, "acd"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := vimTestKeys(vimTestModel(tt.text, 0), tt.keys...)
			if m.input.Value() != tt.want || !m.vim.normal {
				t.Fatalf("text=%q normal=%v, want %q", m.input.Value(), m.vim.normal, tt.want)
			}
		})
	}
}

func TestVimVisualSelection(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		cursor     int
		keys       []string
		want       string
	}{
		{"characters", "abcd", 0, []string{"v", "l", "d"}, "cd"},
		{"reverse characters", "abcd", 2, []string{"v", "h", "d"}, "ad"},
		{"change selection", "abcd", 0, []string{"v", "l", "c", "Z", "esc"}, "Zcd"},
		{"yank selection", "abcd", 0, []string{"v", "l", "y", "P"}, "ababcd"},
		{"line selection", "a\nb\nc", 0, []string{"V", "j", "d"}, "c"},
		{"reverse line selection", "a\nb\nc", 4, []string{"V", "k", "d"}, "a"},
		{"switch selection kind", "a\nb\nc", 0, []string{"v", "j", "V", "d"}, "c"},
		{"visual cancel", "abcd", 0, []string{"v", "l", "esc", "x"}, "acd"},
		{"cancel pending visual find", "abcd", 0, []string{"v", "f", "esc", "x"}, "bcd"},
		{"visual repeat", "abcdef", 0, []string{"v", "l", "d", "."}, "ef"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := vimTestKeys(vimTestModel(tt.text, tt.cursor), tt.keys...)
			if m.input.Value() != tt.want || m.vim.visual || !m.vim.normal {
				t.Fatalf("text=%q visual=%v normal=%v, want %q", m.input.Value(), m.vim.visual, m.vim.normal, tt.want)
			}
		})
	}
}

func TestVimVisualHighlightPreservesGeometry(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := vimTestModel("文本 a\u0301bc def ghi\nsecond", 0)
	m.input.SetWidth(10)
	m.input.SetHeight(4)
	m = vimTestKeys(m, "v", "j")
	view := m.turnTextareaView()
	m.vim.visual = false
	unselected := m.turnTextareaView()
	if ansi.Strip(view) != ansi.Strip(unselected) || lipgloss.Width(view) != lipgloss.Width(unselected) || !strings.Contains(view, "48;5;237") {
		t.Fatalf("selection damaged text/width or missing highlight: %q", view)
	}
}

func TestVimSubmitBothModes(t *testing.T) {
	for _, normal := range []bool{false, true} {
		s, err := store.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		item, err := s.CreateItem(models.ChannelInbox, "target", "body", models.TypeThread, models.StatusBacklog, "")
		if err != nil {
			t.Fatal(err)
		}
		m := newModel(s, nil, nil)
		m.items = []models.Item{item}
		next, _ := m.openComposer()
		m = next.(model)
		m.vim.normal = normal
		m.input.SetValue("send this")
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		m = next.(model)
		saved, err := s.GetItem(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if m.mode != modeNav || len(saved.Turns) != 1 || saved.Turns[0].Content != "send this" {
			t.Fatalf("normal=%v mode=%v turns=%v", normal, m.mode, saved.Turns)
		}
	}
}

func TestVimNormalShiftTabPreservesEditor(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items, m.allItems = []models.Item{item}, []models.Item{item}
	next, _ := m.openComposer()
	m = next.(model)
	m.input.SetValue("draft")
	setTextareaCursorOffset(&m.input, 2)
	m.vim.normal = true
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if !m.vim.normal || m.mode != modeCompose || m.input.Value() != "draft" || textareaCursorOffset(m.input) != 2 {
		t.Fatal("execution mode change disturbed the normal editor")
	}
	stored, err := s.GetItem(item.ID)
	if err != nil || stored.Mode != models.AgentModeGoal {
		t.Fatalf("execution mode was not saved: %v, %v", stored.Mode, err)
	}
}

func TestVimWrappedRowsDifferFromLogicalLines(t *testing.T) {
	m := vimTestModel("one two three four five six\nend", 1)
	m.input.SetWidth(10)
	m.input.SetHeight(8)
	_ = m.input.View()
	logical := vimTestKeys(m, "j")
	if textareaCursorOffset(logical.input) != 29 {
		t.Fatalf("logical j cursor = %d", textareaCursorOffset(logical.input))
	}
	// Recreate the model: textarea copies share their viewport internally.
	m = vimTestModel("one two three four five six\nend", 1)
	m.input.SetWidth(10)
	m.input.SetHeight(8)
	_ = m.input.View()
	displayed := vimTestKeys(m, "g", "j")
	p := textareaCursorOffset(displayed.input)
	if p <= 1 || p >= 28 {
		t.Fatalf("display-row gj cursor = %d, want inside wrapped first line", p)
	}
}
