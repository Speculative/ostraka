package tui

import (
	"strings"
	"testing"

	"ostraka/internal/models"
	"ostraka/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestWrapTextPreservesShortLines(t *testing.T) {
	in := "short line\n\nanother one"
	if got := wrapText(in, 40); got != in {
		t.Errorf("got %q want %q", got, in)
	}
}

func TestWrapTextZeroWidthPassesThrough(t *testing.T) {
	in := "a line far longer than nothing"
	if got := wrapText(in, 0); got != in {
		t.Errorf("got %q want %q", got, in)
	}
}

func TestWrapTextBreaksAtWordBoundary(t *testing.T) {
	got := wrapText("aaa bbb ccc ddd", 7)
	want := "aaa bbb\nccc ddd"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWrapTextPreservesBlankLines(t *testing.T) {
	got := wrapText("aaa bbb ccc\n\nddd", 7)
	want := "aaa bbb\nccc\n\nddd"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWrapTextPreservesIndentOnContinuation(t *testing.T) {
	// Indented code blocks in turn bodies must keep their shape, including on
	// the lines the wrap creates.
	got := wrapText("    aaa bbb ccc", 11)
	want := "    aaa bbb\n    ccc"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWrapTextDropsIndentPastHalfWidth(t *testing.T) {
	// 8 spaces of indent in a 10-column pane leaves 2 usable columns, so the
	// indent is abandoned rather than shredding the words.
	got := wrapText("        aaa bbb", 10)
	if strings.HasPrefix(got, " ") {
		t.Errorf("indent should have been dropped, got %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > 10 {
			t.Errorf("line %q exceeds width 10", line)
		}
	}
}

func TestWrapTextHardSplitsUnbreakableToken(t *testing.T) {
	got := wrapText("https://example.com/a/very/long/path", 10)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the token to be split, got %q", got)
	}
	for _, line := range lines {
		if len([]rune(line)) > 10 {
			t.Errorf("line %q exceeds width 10", line)
		}
	}
	if joined := strings.Join(lines, ""); joined != "https://example.com/a/very/long/path" {
		t.Errorf("split lost characters: %q", joined)
	}
}

func TestWrapTextNeverExceedsWidth(t *testing.T) {
	body := "A turn body with a mix of prose, an indented block\n\n    code line that is quite long indeed\n\nand a trailing paragraph."
	for _, w := range []int{1, 2, 5, 13, 40, 200} {
		for _, line := range strings.Split(wrapText(body, w), "\n") {
			if len([]rune(line)) > w {
				t.Errorf("width %d: line %q exceeds it", w, line)
			}
		}
	}
}

func TestCurrentInputHeightUsesSoftWrappedRows(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.input.SetWidth(5)
	m.input.SetValue("abcdefghijk")

	visualLines := m.inputVisualLineCount()
	if visualLines <= 1 {
		t.Errorf("visual line count = %d, want soft wrapping", visualLines)
	}
	if got := m.currentInputHeight(); got != visualLines {
		t.Errorf("input height = %d, want visual line count %d", got, visualLines)
	}
}

func TestCurrentInputHeightCapsSoftWrappedRows(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.input.SetWidth(1)
	m.input.SetValue(strings.Repeat("x", inputMaxHeight+3))

	if got := m.inputVisualLineCount(); got <= inputMaxHeight {
		t.Errorf("visual line count = %d, want more than max height %d", got, inputMaxHeight)
	}
	if got := m.currentInputHeight(); got != inputMaxHeight {
		t.Errorf("input height = %d, want max %d", got, inputMaxHeight)
	}
}

func TestEmptyComposerHeightDoesNotIncludeViewportPadding(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.width = 100
	m.height = 30
	m = m.recalcLayout()

	if got := m.inputVisualLineCount(); got != 1 {
		t.Errorf("visual line count = %d, want one empty content row", got)
	}
	if got := m.input.Height(); got != inputMinHeight {
		t.Errorf("composer height = %d, want minimum %d", got, inputMinHeight)
	}
}

func TestSoftWrapGrowthReclaimsTextareaScrollOffset(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.width = 40
	m.height = 30
	m = m.recalcLayout()
	m.input.Focus()

	for range 500 {
		oldH := m.input.Height()
		next, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
		m = next.(model)
		if m.input.Height() > oldH {
			if got := textareaViewport(&m.input).YOffset; got != 0 {
				t.Errorf("textarea offset after growth = %d, want 0", got)
			}
			return
		}
	}
	t.Fatal("input did not grow after soft wrapping")
}

func TestBracketedPasteAddsMultilineComposerContent(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.width = 80
	m.height = 30
	m = m.recalcLayout()
	m.input.Focus()

	paste := "first line\nq\nctrl+s is text, not a command\nlast line"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(paste), Paste: true})
	got := next.(model)
	if got.mode != modeCompose {
		t.Fatalf("paste changed mode to %v, want compose", got.mode)
	}
	if got.input.Value() != paste {
		t.Errorf("pasted content = %q, want %q", got.input.Value(), paste)
	}
	if got.input.Height() < inputMinHeight {
		t.Errorf("composer height = %d, want at least %d", got.input.Height(), inputMinHeight)
	}
}

func TestEditorWordShortcutBindings(t *testing.T) {
	m := newModel(nil, nil, nil)
	for name, keys := range map[string][]string{
		"word backward":     m.input.KeyMap.WordBackward.Keys(),
		"word forward":      m.input.KeyMap.WordForward.Keys(),
		"body delete word":  m.input.KeyMap.DeleteWordBackward.Keys(),
		"title delete word": m.title.KeyMap.DeleteWordBackward.Keys(),
	} {
		joined := strings.Join(keys, ",")
		want := "ctrl+w"
		if name == "word backward" {
			want = "ctrl+left"
		}
		if name == "word forward" {
			want = "ctrl+right"
		}
		if !strings.Contains(joined, want) {
			t.Errorf("%s bindings %q do not include %q", name, joined, want)
		}
	}
}

func TestTurnDraftRestoresAndCtrlCClearsIt(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "draft target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(item.ID, "saved\ndraft"); err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	next, _ := m.openComposer()
	got := next.(model)
	if got.input.Value() != "saved\ndraft" {
		t.Fatalf("restored draft = %q", got.input.Value())
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got = next.(model)
	if got.mode != modeNav {
		t.Errorf("mode after ctrl+c = %v, want navigation", got.mode)
	}
	if content, err := s.LoadDraft(item.ID); err != nil || content != "" {
		t.Errorf("draft after ctrl+c = %q, %v; want empty, nil", content, err)
	}
}

func TestEscapeCheckpointsTurnDraft(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "draft target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	next, _ := m.openComposer()
	got := next.(model)
	got.input.SetValue("keep this")
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = next.(model)
	if got.mode != modeNav {
		t.Errorf("mode after esc = %v, want navigation", got.mode)
	}
	if content, err := s.LoadDraft(item.ID); err != nil || content != "keep this" {
		t.Errorf("draft after esc = %q, %v; want %q, nil", content, err, "keep this")
	}
}

func TestPendingTurnDraftShowsCollapsedComposer(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "draft target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(item.ID, "saved draft\nwith more content"); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.width = 100
	m.height = 30
	m.refreshPendingDraft()
	m = m.recalcLayout()

	if m.mode != modeNav {
		t.Fatalf("mode = %v, want navigation", m.mode)
	}
	if m.pendingDraftItemID != item.ID {
		t.Fatalf("pending draft item = %q, want %q", m.pendingDraftItemID, item.ID)
	}
	if !m.composerVisible() {
		t.Fatal("saved draft composer is not visible")
	}
	if got := m.input.Height(); got != inputMinHeight {
		t.Errorf("collapsed composer height = %d, want %d", got, inputMinHeight)
	}
	if got := m.input.Value(); got != "saved draft\nwith more content" {
		t.Errorf("preview content = %q", got)
	}
}

func TestEscapeLeavesTurnDraftComposerCollapsed(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "draft target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.width = 100
	m.height = 30
	next, _ := m.openComposer()
	m = next.(model)
	m.input.SetValue("keep this draft")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if m.mode != modeNav {
		t.Fatalf("mode after esc = %v, want navigation", m.mode)
	}
	if !m.composerVisible() {
		t.Fatal("saved draft composer disappeared after esc")
	}
	if got := m.input.Height(); got != inputMinHeight {
		t.Errorf("composer height after esc = %d, want %d", got, inputMinHeight)
	}
}

func TestDismissingTallTurnDraftKeepsFrameHeight(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "draft target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, &fakeSupervisor{})
	m.items = []models.Item{item}
	m.width = 100
	m.height = 30
	next, _ := m.openComposer()
	m = next.(model)
	m.input.SetValue(strings.Repeat("a tall draft line\n", inputMaxHeight+3))
	m = m.recalcLayout()
	if got := m.input.Height(); got != inputMaxHeight {
		t.Fatalf("active tall composer height = %d, want %d", got, inputMaxHeight)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if got := m.input.Height(); got != inputMinHeight {
		t.Fatalf("dismissed composer height = %d, want %d", got, inputMinHeight)
	}
	if got := lipgloss.Height(m.View()); got != m.height {
		t.Fatalf("dismissed frame height = %d, want terminal height %d", got, m.height)
	}
}
