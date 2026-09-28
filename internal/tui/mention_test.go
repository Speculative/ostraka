package tui

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestMentionTokenAtHandlesBoundariesUnicodeAndCursorInsideToken(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cursor int
		want   mentionToken
		ok     bool
	}{
		{name: "empty token", text: "write @ here", cursor: 7, want: mentionToken{start: 6, end: 7}, ok: true},
		{name: "multiline", text: "first\nSee @ab here", cursor: len([]rune("first\nSee @a")), want: mentionToken{start: 10, end: 13, query: "ab"}, ok: true},
		{name: "cursor inside token", text: "See @abcd now", cursor: len([]rune("See @ab")), want: mentionToken{start: 4, end: 9, query: "abcd"}, ok: true},
		{name: "unicode before token", text: "日本語 @id", cursor: len([]rune("日本語 @id")), want: mentionToken{start: 4, end: 7, query: "id"}, ok: true},
		{name: "word boundary rejects adjacent unicode", text: "日本語@id", cursor: len([]rune("日本語@id")), ok: false},
		{name: "punctuation dismisses", text: "See @ab,", cursor: len([]rune("See @ab,")), ok: false},
		{name: "punctuation before is a boundary", text: "See/@ab", cursor: len([]rune("See/@ab")), want: mentionToken{start: 4, end: 7, query: "ab"}, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mentionTokenAt(tt.text, tt.cursor)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("mentionTokenAt(%q, %d) = %#v, %v; want %#v, %v", tt.text, tt.cursor, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRankMentionCandidates(t *testing.T) {
	items := []models.Item{
		{ID: "target", Title: "Exact target", Status: models.StatusArchived},
		{ID: "zzzz-aaaa", Title: "Exact target", Status: models.StatusArchived},
		{ID: "ab12-cdef", Title: "A title substring", Status: models.StatusActive},
		{ID: "ab99-zzzz", Title: "Another live item", Status: models.StatusActive},
		{ID: "dead-beef", Title: "Abacus words", Status: models.StatusArchived},
		{ID: "live-one", Title: "The target word", Status: models.StatusActive},
		{ID: "old-one", Title: "The target word", Status: models.StatusArchived},
	}

	got := rankMentionCandidates("ab", items)
	if ids := mentionIDs(got); !strings.EqualFold(strings.Join(ids, ","), "ab12-cdef,ab99-zzzz,dead-beef") {
		t.Fatalf("ID ranking = %v", ids)
	}

	got = rankMentionCandidates("target", items)
	if ids := mentionIDs(got); !strings.EqualFold(strings.Join(ids, ","), "target,live-one,zzzz-aaaa,old-one") {
		t.Fatalf("title ranking = %v", ids)
	}
}

func TestReplaceMentionPreservesSurroundingUnicodeText(t *testing.T) {
	text := "前文 @tit 后文"
	start := len([]rune("前文 "))
	end := len([]rune("前文 @tit"))
	got, cursor := replaceMention(text, start, end, "abcd-efgh")
	want := "前文 @abcd-efgh  后文"
	if got != want {
		t.Fatalf("replacement = %q, want %q", got, want)
	}
	if cursor != len([]rune("前文 @abcd-efgh ")) {
		t.Fatalf("cursor = %d, want %d", cursor, len([]rune("前文 @abcd-efgh ")))
	}
}

func TestMentionTextareaCursorSurvivesSoftWrapAndMidBufferSelection(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.input.SetWidth(10)
	m.input.SetHeight(inputMaxHeight)
	m.input.Focus()
	m.input.SetValue("前置 text @ca and suffix")
	cursor := len([]rune("前置 text @ca"))
	setTextareaCursorOffset(&m.input, cursor)
	if got := textareaCursorOffset(m.input); got != cursor {
		t.Fatalf("cursor offset = %d, want %d", got, cursor)
	}
	m.allItems = []models.Item{{ID: "abcd-efgh", Title: "Cache item", Status: models.StatusActive}}
	m.refreshMentionPicker()
	if !m.mentionPicker.open || m.mentionPicker.start != len([]rune("前置 text ")) {
		t.Fatalf("picker = %#v, want token at mid-buffer", m.mentionPicker)
	}
	next, _ := m.chooseMention()
	m = next.(model)
	want := "前置 text @abcd-efgh  and suffix"
	if m.input.Value() != want {
		t.Fatalf("mid-buffer replacement = %q, want %q", m.input.Value(), want)
	}
	wantCursor := len([]rune("前置 text @abcd-efgh "))
	if got := textareaCursorOffset(m.input); got != wantCursor {
		t.Fatalf("mid-buffer cursor = %d, want %d", got, wantCursor)
	}
}

func TestMentionPickerKeysAndOverlay(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.width = 100
	m.height = 30
	m = m.recalcLayout()
	m.input.Focus()
	m.allItems = []models.Item{
		{ID: "first-one", Title: "First title", Status: models.StatusActive},
		{ID: "second-two", Title: "Second title", Status: models.StatusArchived},
	}
	m.input.SetValue("link @title")
	m.input.CursorEnd()
	m.refreshMentionPicker()
	if !m.mentionPicker.open || len(m.mentionPicker.items) != 2 {
		t.Fatalf("picker = %#v, want open with two results", m.mentionPicker)
	}

	next, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.mentionPicker.selected != 1 {
		t.Fatalf("selected = %d, want second result", m.mentionPicker.selected)
	}

	next, _ = m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mentionPicker.open {
		t.Fatal("picker remained open after selection")
	}
	if got, want := m.input.Value(), "link @second-two "; got != want {
		t.Fatalf("selected replacement = %q, want %q", got, want)
	}
	if got := textareaCursorOffset(m.input); got != len([]rune(m.input.Value())) {
		t.Fatalf("cursor offset = %d, want end %d", got, len([]rune(m.input.Value())))
	}

	m.input.SetValue("link @title")
	m.input.CursorEnd()
	m.refreshMentionPicker()
	plain := ansi.Strip(strings.Join(m.overlayMentionPicker(strings.Split(strings.Repeat("conversation\n", 20), "\n")), "\n"))
	for _, want := range []string{"First title", "@first-one", "active", "Second title", "archived"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("overlay %q missing %q", plain, want)
		}
	}
}

func TestMentionPickerAnchorsAboveToken(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.width = 100
	m.height = 30
	m.input.Focus()
	m.allItems = []models.Item{{ID: "abcd-efgh", Title: "Target item", Status: models.StatusActive}}
	m.input.SetValue("first line\nsecond @target")
	m.input.CursorEnd()
	m = m.recalcLayout()
	m.refreshMentionPicker()
	// View renders the textarea before the picker is placed, which populates
	// the textarea viewport used by the anchor calculation.
	m.input.View()
	viewportBefore := textareaViewport(&m.input).YOffset

	panelWidth := m.width - (m.listWidth() + 1)
	panelLines := make([]string, 50)
	for i := range panelLines {
		panelLines[i] = strings.Repeat(".", panelWidth)
	}
	panel := strings.Join(panelLines, "\n")
	got := ansi.Strip(m.overlayMentionPickerAtCursor(panel))
	gotLines := strings.Split(got, "\n")

	visualRow, column := textareaVisualPosition(m.input, m.mentionPicker.start)
	if got := textareaViewport(&m.input).YOffset; got != viewportBefore {
		t.Fatalf("anchor measurement changed textarea scroll offset from %d to %d", viewportBefore, got)
	}
	top := m.conv.Height + 2 + visualRow - textareaViewport(&m.input).YOffset - lipgloss.Height(m.mentionPickerBox())
	left := 1 + textareaGutterWidth(m.input) + column
	left = max(0, min(left, panelWidth-lipgloss.Width(m.mentionPickerBox())))
	if top <= 0 {
		t.Fatalf("anchor top = %d, want room above the composer cursor", top)
	}
	headerRow := -1
	for i, line := range gotLines {
		if strings.Contains(line, "mention item") {
			headerRow = i
			break
		}
	}
	if headerRow != top+1 {
		t.Fatalf("picker header row = %d, want %d", headerRow, top+1)
	}
	borderColumn := strings.IndexRune(gotLines[top], '╭')
	if borderColumn < 0 || borderColumn != left {
		t.Fatalf("picker left column = %d, want %d in %q", borderColumn, left, gotLines[top])
	}
}

func TestMentionPickerEscapeOnlyDismissesPicker(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeCompose
	m.input.Focus()
	m.input.SetValue("@")
	m.input.CursorEnd()
	m.allItems = []models.Item{{ID: "abcd-efgh", Title: "Target", Status: models.StatusActive}}
	m.refreshMentionPicker()
	next, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEscape})
	got := next.(model)
	if got.mode != modeCompose || got.input.Value() != "@" || got.mentionPicker.open {
		t.Fatalf("escape changed composer: mode=%v value=%q picker=%v", got.mode, got.input.Value(), got.mentionPicker.open)
	}
}

func TestMentionSelectionPersistsCanonicalIDAndBacklink(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "Canonical target", "target body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}

	// Exercise the new-item body composer, selecting by title rather than by
	// an already-known ID. The persisted body must contain the exact canonical
	// ID so the store's existing mention scanner can derive the backlink.
	m := newModel(s, nil, nil)
	m.mode = modeCompose
	m.draft = true
	m.draftSelected = true
	m.draftChannel = models.ChannelInbox
	m.title.SetValue("Source item")
	m.input.Focus()
	m.input.SetValue("See @canonical")
	m.input.CursorEnd()
	m.allItems = []models.Item{target}
	m.refreshMentionPicker()
	next, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if got, want := m.input.Value(), "See @"+target.ID+" "; got != want {
		t.Fatalf("selected body = %q, want %q", got, want)
	}
	next, _ = m.handleInputKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if m.mode != modeNav {
		t.Fatalf("new-item submit mode = %v, want navigation", m.mode)
	}

	items, err := s.ListItems(store.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var source models.Item
	for _, item := range items {
		if item.Title == "Source item" {
			source = item
		}
	}
	if source.ID == "" || !containsString(source.Mentions, target.ID) {
		t.Fatalf("source = %#v, want mention %q", source, target.ID)
	}
	backlinks, err := s.BacklinkItems(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlinks) != 1 || backlinks[0].ID != source.ID {
		t.Fatalf("backlinks = %#v, want source %q", backlinks, source.ID)
	}

	turnSource, err := s.CreateItem(models.ChannelInbox, "Turn source", "opening body", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	tm := newModel(s, nil, nil)
	tm.mode = modeCompose
	tm.items = []models.Item{turnSource}
	tm.selected = 0
	tm.draftItemID = turnSource.ID
	tm.input.Focus()
	tm.input.SetValue("A turn to @canonical")
	tm.input.CursorEnd()
	tm.allItems = append(items, turnSource)
	tm.refreshMentionPicker()
	next, _ = tm.handleInputKey(tea.KeyMsg{Type: tea.KeyTab})
	tm = next.(model)
	next, _ = tm.handleInputKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	tm = next.(model)
	if tm.mode != modeNav {
		t.Fatalf("turn submit mode = %v, want navigation", tm.mode)
	}

	updated, err := s.GetItem(turnSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(updated.Mentions, target.ID) {
		t.Fatalf("turn source = %#v, want mention %q", updated, target.ID)
	}
	backlinks, err = s.BacklinkItems(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlinks) != 2 || !containsItemID(backlinks, source.ID) || !containsItemID(backlinks, turnSource.ID) {
		t.Fatalf("backlinks after turn = %#v, want %q and %q", backlinks, source.ID, turnSource.ID)
	}
}

func mentionIDs(items []models.Item) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsItemID(items []models.Item, want string) bool {
	for _, item := range items {
		if item.ID == want {
			return true
		}
	}
	return false
}
