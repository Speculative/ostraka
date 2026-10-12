package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/Speculative/ostraka/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestReviewChangesIsFourthTabAfterProjectContext(t *testing.T) {
	st, err := store.NewStore(filepath.Join(t.TempDir(), ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(st, nil, nil)
	m.width = 120
	m.height = 30

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = next.(model)
	if m.projectPane != 1 {
		t.Fatalf("key 3 opened pane %d, want Project Context", m.projectPane)
	}

	next, cmd := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = next.(model)
	if m.projectPane != reviewProjectPane || cmd == nil {
		t.Fatalf("key 4 opened pane %d with command %v, want Review changes and a diff load", m.projectPane, cmd != nil)
	}
	if state := m.ReviewViewState(); !state.Active || !state.Loading {
		t.Fatalf("review view state after opening: %#v", state)
	}
	header := ansi.Strip(m.renderHeader())
	if strings.Count(header, "[") != 1 || !strings.Contains(header, "[Review changes]") {
		t.Fatalf("review tab is not the sole active tab: %q", header)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = next.(model)
	if m.projectPane != 1 {
		t.Fatalf("key 3 from Review changes opened pane %d, want Project Context", m.projectPane)
	}
}

func TestReviewSelectionExposesPathSideRangeAndExcerpt(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width = 110
	m.height = 30
	m.projectPane = reviewProjectPane
	m.focus = focusReadingPane
	m.reviewDiff = reviewdiff.Diff{
		Root: "/repo",
		Head: "0123456789abcdef",
		Files: []reviewdiff.File{{
			Path:    "new/name.go",
			OldPath: "old/name.go",
			Status:  "modified",
			Lines: []reviewdiff.Line{
				{Text: "@@ -10,3 +20,3 @@", Kind: '@'},
				{Text: "same", Kind: ' ', OldLine: 10, NewLine: 20},
				{Text: "removed", Kind: '-', OldLine: 11},
				{Text: "added", Kind: '+', NewLine: 21},
				{Text: "after", Kind: ' ', OldLine: 12, NewLine: 22},
			},
		}},
	}
	m.projectEntries = reviewFileEntries(m.reviewDiff, false)
	m.reviewCursor = 2
	m.reviewAnchor = -1
	m.reviewSide = "new"
	m.conv.Width = 75
	m.conv.Height = 15
	m.updateConv()

	// Start on a removed line, extend through the preceding context, and finish
	// the range. Deleted code anchors to the old path and old-side line numbers.
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = next.(model)
	next, _ = m.handleReviewKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	next, _ = m.handleReviewKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	selection, marked, err := m.ReviewSelection()
	if err != nil || !marked {
		t.Fatalf("selection: %#v marked=%v err=%v", selection, marked, err)
	}
	if selection.Path != "old/name.go" || selection.Side != "old" ||
		selection.StartLine != 10 || selection.EndLine != 11 ||
		selection.Excerpt != "same\nremoved" {
		t.Fatalf("selection did not preserve its source anchor: %#v", selection)
	}
	state := m.ReviewViewState()
	if !state.Active || !state.HasRange || state.FilePath != "new/name.go" || state.Side != "old" || state.LineIndex != 1 {
		t.Fatalf("review browser state does not expose current selection: %#v", state)
	}
}

func TestReviewHunkNavigationMovesBetweenFirstCodeLines(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.projectPane = reviewProjectPane
	m.reviewDiff = reviewdiff.Diff{Files: []reviewdiff.File{{
		Path:   "file.go",
		Status: "modified",
		Lines: []reviewdiff.Line{
			{Text: "@@ -1 +1 @@", Kind: '@'},
			{Text: "one", Kind: ' ', OldLine: 1, NewLine: 1},
			{Text: "@@ -10 +10 @@", Kind: '@'},
			{Text: "ten", Kind: ' ', OldLine: 10, NewLine: 10},
		},
	}}}
	m.projectEntries = reviewFileEntries(m.reviewDiff, false)
	m.reviewCursor = 1
	m.conv.Width = 60
	m.conv.Height = 12
	m.updateConv()

	m.moveReviewHunk(1)
	if m.reviewCursor != 3 {
		t.Fatalf("next hunk cursor = %d, want 3", m.reviewCursor)
	}
	m.moveReviewHunk(-1)
	if m.reviewCursor != 1 {
		t.Fatalf("previous hunk cursor = %d, want 1", m.reviewCursor)
	}
}

func reviewDisplayModel() model {
	m := newModel(nil, nil, nil)
	m.projectPane = reviewProjectPane
	m.width, m.height = 160, 30
	m.conv.Width, m.conv.Height = 90, 15
	m.reviewAnchor = -1
	m.reviewSide = "new"
	m.reviewDiff = reviewdiff.Diff{Files: []reviewdiff.File{{Path: "file.go", Status: "modified", Lines: []reviewdiff.Line{
		{Text: "@@ -10 +10 @@", Kind: '@'},
		{Text: "before", Kind: '-', OldLine: 10},
		{Text: "after", Kind: '+', NewLine: 10},
	}, FullLines: []reviewdiff.Line{
		{Text: "@@ -1,11 +1,11 @@", Kind: '@'},
		{Text: "context", Kind: ' ', OldLine: 1, NewLine: 1},
		{Text: "before", Kind: '-', OldLine: 10},
		{Text: "after", Kind: '+', NewLine: 10},
	}}}}
	m.projectEntries = reviewFileEntries(m.reviewDiff, false)
	m.reviewCursor = 1
	m.updateConv()
	return m
}

func TestReviewFocusRerendersImmediately(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := reviewDisplayModel()
	m.reviewSide = "old"
	m.updateConv()
	unfocused := m.conv.View()
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = next.(model)
	focused := m.conv.View()
	if focused == unfocused {
		t.Fatal("focus did not change rendered highlight")
	}
	next, _ = m.handleReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = next.(model)
	if m.conv.View() != unfocused {
		t.Fatal("blur left cursor highlight behind")
	}
}

func TestReviewContextAndSplitPreserveSourceSelection(t *testing.T) {
	m := reviewDisplayModel()
	m.reviewAnchor = 1
	m.reviewCursor = 1
	m.reviewSide = "old"
	before, _, err := m.ReviewSelection()
	if err != nil {
		t.Fatal(err)
	}
	m.toggleReviewContext()
	if m.reviewCursor != 2 || m.reviewAnchor != 2 {
		t.Fatalf("context did not map selection: %d %d", m.reviewCursor, m.reviewAnchor)
	}
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = next.(model)
	after, _, err := m.ReviewSelection()
	if err != nil || after != before {
		t.Fatalf("selection changed: %#v %v", after, err)
	}
	if m.reviewLineOffsets[2] != m.reviewLineOffsets[3] {
		t.Fatal("replacement cells are not aligned")
	}
	if !strings.Contains(ansi.Strip(m.conv.View()), "│") {
		t.Fatal("split divider missing")
	}
	m.toggleReviewContext()
	after, _, err = m.ReviewSelection()
	if err != nil || after != before {
		t.Fatalf("return to diff changed selection: %#v %v", after, err)
	}
}

func TestReviewSearchFindsCodeAndFilename(t *testing.T) {
	m := reviewDisplayModel()
	m.focus = focusReadingPane
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(model)
	m.filterInput.SetValue("after")
	if !strings.Contains(ansi.Strip(m.renderFooter()), "Search:") {
		t.Fatal("search input not visible")
	}
	next, _ = m.handleReviewKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.reviewSearching || m.reviewCursor != 2 {
		t.Fatalf("search cursor=%d active=%v", m.reviewCursor, m.reviewSearching)
	}
	m.reviewDiff.Files = append(m.reviewDiff.Files, reviewdiff.File{Path: "other.go", Status: "untracked"})
	m.projectEntries = reviewFileEntries(m.reviewDiff, false)
	m.reviewSearchFiles = true
	m.reviewSearchQuery = "other"
	m.findReviewMatch(1)
	if m.selected != 1 {
		t.Fatal("filename search did not select file")
	}
}

func TestReviewStatusDotsUseFileStatusColors(t *testing.T) {
	d := reviewdiff.Diff{Files: []reviewdiff.File{{Status: "modified"}, {Status: "untracked"}, {Status: "deleted"}, {Status: "added"}}}
	entries := reviewFileEntries(d, false)
	for i, want := range []lipgloss.Color{"226", "42", "203", "42"} {
		if entries[i].statusColor != want {
			t.Fatalf("dot %d = %q, want %q", i, entries[i].statusColor, want)
		}
	}
}

func TestReviewFullContextAnchorIsValidatedOutsideNormalHunk(t *testing.T) {
	m := reviewDisplayModel()
	anchor := &models.ReviewAnchor{File: "file.go", Side: "new", StartLine: 1, EndLine: 1, Excerpt: "context"}
	if !reviewAnchorMatchesDiff(m.reviewDiff, anchor) {
		t.Fatal("valid full-context anchor flagged stale")
	}
	anchor.Excerpt = "changed elsewhere"
	if reviewAnchorMatchesDiff(m.reviewDiff, anchor) {
		t.Fatal("stale full-context anchor accepted")
	}
}

func reviewKey(m model, key string) model {
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(model)
}

func TestReviewSlashStartsFreshSearch(t *testing.T) {
	m := reviewDisplayModel()
	m.reviewSearchQuery = "previous"
	m.filterInput.SetValue("previous")
	m = reviewKey(m, "/")
	if m.filterInput.Value() != "" || m.reviewSearchQuery != "" || !m.reviewSearching {
		t.Fatal("slash did not clear previous search")
	}
	m.filterInput.SetValue("after")
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m = reviewKey(m, "/")
	if m.filterInput.Value() != "" || m.reviewSearchQuery != "" {
		t.Fatal("second search reused the previous query")
	}
}

func reviewNavigationModel() model {
	m := reviewDisplayModel()
	m.reviewDiff.Files[0].Lines = []reviewdiff.Line{
		{Kind: '@', Text: "@@ -1,4 +1,3 @@"},
		{Kind: ' ', Text: "context", OldLine: 1, NewLine: 1},
		{Kind: '-', Text: "removed one", OldLine: 2},
		{Kind: '-', Text: "removed two", OldLine: 3},
		{Kind: '+', Text: "added one", NewLine: 2},
		{Kind: ' ', Text: "tail", OldLine: 4, NewLine: 3},
		{Kind: '@', Text: "@@ -10 +9,0 @@"},
		{Kind: '-', Text: "removed ten", OldLine: 10},
		{Kind: '@', Text: "@@ -11,0 +10 @@"},
		{Kind: '+', Text: "added ten", NewLine: 10},
	}
	m.reviewSplit = true
	m.focus = focusReadingPane
	m.reviewCursor = 1
	m.updateConv()
	return m
}

func TestReviewSplitMovementStaysOnChosenSide(t *testing.T) {
	m := reviewNavigationModel()
	for _, want := range []int{4, 5, 9, 9} {
		m = reviewKey(m, "j")
		if m.reviewCursor != want || m.reviewSide != "new" {
			t.Fatalf("new navigation: cursor=%d side=%s, want %d", m.reviewCursor, m.reviewSide, want)
		}
	}
	for _, want := range []int{5, 4, 1, 1} {
		m = reviewKey(m, "k")
		if m.reviewCursor != want || m.reviewSide != "new" {
			t.Fatalf("reverse new navigation: cursor=%d side=%s, want %d", m.reviewCursor, m.reviewSide, want)
		}
	}
	m = reviewKey(m, "j") // added one, paired with removed one
	m = reviewKey(m, "s")
	if m.reviewSide != "old" || m.reviewCursor != 2 {
		t.Fatalf("side switch lost paired row: %d %s", m.reviewCursor, m.reviewSide)
	}
	m = reviewKey(m, "s")
	if m.reviewSide != "new" || m.reviewCursor != 4 {
		t.Fatal("switch back lost paired row")
	}
	m = reviewKey(m, "s")
	for _, want := range []int{3, 5, 7, 7} {
		m = reviewKey(m, "j")
		if m.reviewCursor != want || m.reviewSide != "old" {
			t.Fatalf("old navigation: cursor=%d side=%s, want %d", m.reviewCursor, m.reviewSide, want)
		}
	}
	// Starting a selection on context must retain the explicitly selected side.
	m = reviewKey(m, "k")
	m = reviewKey(m, "v")
	if m.reviewSide != "old" {
		t.Fatal("v reset the selected side")
	}
	m = reviewKey(m, "k")
	selection, marked, err := m.ReviewSelection()
	if err != nil || !marked || selection.Side != "old" || selection.Excerpt != "removed two\ntail" {
		t.Fatalf("old range: %#v %v", selection, err)
	}
}

func TestReviewSplitHunksAndSearchRespectChosenSide(t *testing.T) {
	m := reviewNavigationModel()
	m = reviewKey(m, "]")
	if m.reviewCursor != 9 || m.reviewSide != "new" {
		t.Fatal("new hunk jump landed on removal-only hunk")
	}
	m = reviewKey(m, "[")
	if m.reviewCursor != 1 {
		t.Fatal("backward jump did not skip removal-only hunk")
	}
	m = reviewKey(m, "s")
	m = reviewKey(m, "]")
	if m.reviewCursor != 7 || m.reviewSide != "old" {
		t.Fatal("old hunk jump failed")
	}
	m.reviewSearchFiles = false
	m.reviewSearchQuery = "added"
	m.findReviewMatch(1)
	if m.reviewCursor != 7 || m.reviewSide != "old" || m.err == nil {
		t.Fatal("old-side search crossed to additions")
	}
	m.err = nil
	m.reviewSearchQuery = "removed two"
	m.findReviewMatch(1)
	if m.reviewCursor != 3 || m.reviewSide != "old" {
		t.Fatal("old-side search missed removal")
	}
}

func TestReviewSplitEmptySideRequiresExplicitSwitch(t *testing.T) {
	m := reviewNavigationModel()
	m.reviewDiff.Files[0].Lines = []reviewdiff.Line{{Text: "deleted", Kind: '-', OldLine: 1}}
	m.reviewCursor = 0
	m.updateConv()
	if m.reviewCursor != -1 || m.reviewSide != "new" {
		t.Fatal("empty new side automatically switched to old")
	}
	m = reviewKey(m, "j")
	m = reviewKey(m, "v")
	if m.reviewAnchor != -1 || m.reviewSelecting {
		t.Fatal("empty side allowed a source selection")
	}
	m = reviewKey(m, "s")
	if m.reviewCursor != 0 || m.reviewSide != "old" {
		t.Fatal("explicit switch did not reach deleted code")
	}
	m = reviewKey(m, "v")
	selection, marked, err := m.ReviewSelection()
	if err != nil || !marked || selection.Excerpt != "deleted" || selection.Side != "old" {
		t.Fatalf("deleted selection: %#v %v", selection, err)
	}
}

func TestFullScreenReviewSearchKeepsTypingKeysInSearch(t *testing.T) {
	m := reviewDisplayModel()
	m = m.enterCopyMode()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(model)
	for _, key := range []rune{'f', 'h', 'q'} {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		m = next.(model)
	}
	if !m.copyMode || !m.reviewSearching || m.filterInput.Value() != "fhq" {
		t.Fatalf("search keys escaped full screen: full=%v query=%q", m.copyMode, m.filterInput.Value())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if !m.copyMode || m.reviewSearching {
		t.Fatal("cancelling search left full screen")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(model)
	if m.copyMode {
		t.Fatal("f did not restore normal layout")
	}
}
