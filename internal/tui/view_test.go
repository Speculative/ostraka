package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func item(ch models.Channel, st models.Status, created time.Time, turns ...time.Time) models.Item {
	it := models.Item{Channel: ch, Status: st, Created: created, Title: string(st)}
	for _, ts := range turns {
		it.Turns = append(it.Turns, models.Turn{Timestamp: ts})
	}
	return it
}

var t0 = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func TestChannelViewExcludesTerminalItems(t *testing.T) {
	// Terminal items live in the archive and nowhere else — that is the whole
	// point of the fourth tab.
	v := channelView(models.ChannelInbox)
	for _, st := range []models.Status{models.StatusDone, models.StatusArchived} {
		if v.includes(item(models.ChannelInbox, st, t0), true) {
			t.Errorf("%q appeared in a channel view", st)
		}
	}
}

func TestConversationShowsPersistedDispatchFailure(t *testing.T) {
	st, err := store.NewStore(filepath.Join(t.TempDir(), ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := st.CreateItem(models.ChannelInbox, "failed agent", "body", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	sup := &fakeSupervisor{dispatchError: "codex: turn failed: quota exceeded"}
	m := newModel(st, nil, sup)
	m.items = []models.Item{item}
	m.selected = 0
	m.conv.Width = 80
	m.conv.Height = 20
	m.updateConv()

	view := m.conv.View()
	if !strings.Contains(view, "agent dispatch failed") || !strings.Contains(view, "quota exceeded") {
		t.Fatalf("conversation omitted dispatch failure: %q", view)
	}
	if m.convFailure == 0 {
		t.Fatal("conversation did not track the dispatch failure")
	}
}

func TestInterruptKeysStopTheActiveDispatch(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		t.Run(key.String(), func(t *testing.T) {
			sup := &fakeSupervisor{busyID: "item-1"}
			m := newModel(nil, nil, sup)
			m.items = []models.Item{{ID: "item-1", Status: models.StatusAgentAcknowledged}}
			m.selected = 0

			if _, cmd := m.handleNavKey(key); cmd != nil {
				t.Fatal("interrupt key unexpectedly returned a command")
			}
			if sup.interrupts != 1 {
				t.Fatalf("interrupt calls = %d, want 1", sup.interrupts)
			}
		})
	}
}

func TestChannelViewExcludesOtherChannels(t *testing.T) {
	v := channelView(models.ChannelInbox)
	if v.includes(item(models.Channel("other"), models.StatusActive, t0), true) {
		t.Error("an item from another channel appeared in the inbox view")
	}
}

func TestBacklogIsHiddenUnlessRevealed(t *testing.T) {
	v := channelView(models.ChannelInbox)
	backlog := item(models.ChannelInbox, models.StatusBacklog, t0)

	if v.includes(backlog, false) {
		t.Error("backlog item shown with the toggle off")
	}
	if !v.includes(backlog, true) {
		// Without this the only route back to a parked item is the CLI.
		t.Error("backlog item still hidden with the toggle on")
	}
}

func TestBacklogToggleDoesNotAffectLiveStatuses(t *testing.T) {
	v := channelView(models.ChannelInbox)
	for _, st := range []models.Status{
		models.StatusActive, models.StatusPendingUser,
		models.StatusPendingAgent, models.StatusAgentAcknowledged,
	} {
		if !v.includes(item(models.ChannelInbox, st, t0), false) {
			t.Errorf("%q hidden when only backlog should be", st)
		}
	}
}

func TestArchiveViewTakesTerminalItemsFromInbox(t *testing.T) {
	if !archiveView.includes(item(models.ChannelInbox, models.StatusArchived, t0), false) {
		t.Error("archived inbox item missing from the archive")
	}
	if archiveView.includes(item(models.ChannelInbox, models.StatusActive, t0), true) {
		t.Error("live inbox item leaked into the archive")
	}
}

func TestSortOrdersByWhoOwesTheNextMove(t *testing.T) {
	all := []models.Item{
		item(models.ChannelInbox, models.StatusBacklog, t0),
		item(models.ChannelInbox, models.StatusPendingUser, t0),
		item(models.ChannelInbox, models.StatusAgentAcknowledged, t0),
		item(models.ChannelInbox, models.StatusActive, t0),
		item(models.ChannelInbox, models.StatusPendingAgent, t0),
	}
	sortForDisplay(all)

	want := []models.Status{
		models.StatusAgentAcknowledged, // running now
		models.StatusPendingAgent,      // queued for the agent
		models.StatusPendingUser,       // waiting on you
		models.StatusActive,
		models.StatusBacklog,
	}
	for i, w := range want {
		if all[i].Status != w {
			t.Errorf("position %d: got %q want %q", i, all[i].Status, w)
		}
	}
}

func TestSortPutsRecentActivityFirstWithinAStatus(t *testing.T) {
	old := item(models.ChannelInbox, models.StatusPendingUser, t0)
	mid := item(models.ChannelInbox, models.StatusPendingUser, t0, t0.Add(time.Hour))
	recent := item(models.ChannelInbox, models.StatusPendingUser, t0, t0.Add(2*time.Hour))

	all := []models.Item{old, recent, mid}
	sortForDisplay(all)

	if !lastActivity(all[0]).Equal(t0.Add(2 * time.Hour)) {
		t.Errorf("most recent item is not first: %v", lastActivity(all[0]))
	}
	if !lastActivity(all[2]).Equal(t0) {
		t.Errorf("least recent item is not last: %v", lastActivity(all[2]))
	}
}

func TestLastActivityUsesTheNewestTurn(t *testing.T) {
	// The body is the opening statement, so a turnless item is as old as it is.
	if got := lastActivity(item(models.ChannelInbox, models.StatusActive, t0)); !got.Equal(t0) {
		t.Errorf("turnless item: got %v want %v", got, t0)
	}
	withTurns := item(models.ChannelInbox, models.StatusActive, t0, t0.Add(time.Hour), t0.Add(3*time.Hour))
	if got := lastActivity(withTurns); !got.Equal(t0.Add(3 * time.Hour)) {
		t.Errorf("got %v, want the last turn's timestamp", got)
	}
}

func TestUnknownStatusSortsLast(t *testing.T) {
	// An item written by a newer CLI must not silently claim the top row.
	all := []models.Item{
		item(models.ChannelInbox, models.Status("from-the-future"), t0),
		item(models.ChannelInbox, models.StatusBacklog, t0),
	}
	sortForDisplay(all)
	if all[0].Status != models.StatusBacklog {
		t.Errorf("unknown status outranked backlog: %q first", all[0].Status)
	}
}

func TestPrepareFiltersAndSortsTogether(t *testing.T) {
	all := []models.Item{
		item(models.Channel("other"), models.StatusActive, t0),                         // wrong channel
		item(models.ChannelInbox, models.StatusArchived, t0),                           // terminal
		item(models.ChannelInbox, models.StatusBacklog, t0),                            // hidden
		item(models.ChannelInbox, models.StatusPendingUser, t0),                        // keep
		item(models.ChannelInbox, models.StatusPendingAgent, t0, t0.Add(-9*time.Hour)), // keep, outranks
	}
	got, hidden := channelView(models.ChannelInbox).prepare(all, false)

	if len(got) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(got), got)
	}
	// Rank beats recency: the older pending-agent item still comes first.
	if got[0].Status != models.StatusPendingAgent || got[1].Status != models.StatusPendingUser {
		t.Errorf("got %q then %q", got[0].Status, got[1].Status)
	}
	// Only the backlog item was suppressed — the wrong-channel and terminal
	// items are not this view's to report.
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
}

func TestPrepareCountsOnlyBacklogAsHidden(t *testing.T) {
	all := []models.Item{
		item(models.Channel("other"), models.StatusBacklog, t0), // other channel
		item(models.ChannelInbox, models.StatusArchived, t0),    // terminal
		item(models.ChannelInbox, models.StatusBacklog, t0),     // the only one
	}
	if _, hidden := channelView(models.ChannelInbox).prepare(all, false); hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	if _, hidden := channelView(models.ChannelInbox).prepare(all, true); hidden != 0 {
		t.Errorf("hidden = %d with the toggle on, want 0", hidden)
	}
}

func TestInitialBacklogFitsOnlyWhenInboxLeavesListSpace(t *testing.T) {
	m := model{width: 80, height: 10} // six list rows after chrome
	all := []models.Item{
		item(models.ChannelInbox, models.StatusActive, t0),
		item(models.ChannelInbox, models.StatusBacklog, t0),
	}
	if !m.initialBacklogFits(all) {
		t.Error("short inbox should show backlog on startup")
	}

	all = append(all, item(models.ChannelInbox, models.StatusBacklog, t0))
	if m.initialBacklogFits(all) {
		t.Error("inbox that fills the list should hide backlog on startup")
	}
}

func TestInitialBacklogFitUsesRenderedRowHeights(t *testing.T) {
	m := model{width: 80, height: 9} // five list rows after chrome
	all := []models.Item{
		item(models.ChannelInbox, models.StatusActive, t0),
		item(models.ChannelInbox, models.StatusBacklog, t0),
	}
	all[1].Title = strings.Repeat("backlog ", 20)
	if m.initialBacklogFits(all) {
		t.Error("wrapped titles that fill the list should hide backlog")
	}
}

func TestFirstInboxLoadAppliesAutomaticBacklogVisibility(t *testing.T) {
	m := model{view: channelView(models.ChannelInbox), width: 80, height: 10}
	all := []models.Item{
		item(models.ChannelInbox, models.StatusActive, t0),
		item(models.ChannelInbox, models.StatusBacklog, t0),
	}
	out, _ := m.Update(itemsLoadedMsg{allItems: all, view: m.view})
	got := out.(model)
	if !got.backlogVisibilityInitialized || !got.showBacklog {
		t.Error("first short inbox load should enable backlog visibility")
	}
	if len(got.items) != len(all) {
		t.Errorf("shown items = %d, want %d", len(got.items), len(all))
	}
}

func TestItemsLoadDoesNotUndoNewerBacklogToggle(t *testing.T) {
	backlog := item(models.ChannelInbox, models.StatusBacklog, t0)
	m := model{
		view:                         channelView(models.ChannelInbox),
		width:                        80,
		height:                       10,
		showBacklog:                  true,
		backlogVisibilityInitialized: true,
		items:                        []models.Item{backlog},
	}

	// This response was started before b enabled backlog. It must not hide the
	// row after the model has moved on to the newer visibility choice.
	out, _ := m.Update(itemsLoadedMsg{
		items:         nil,
		hiddenBacklog: 1,
		view:          m.view,
		showBacklog:   false,
	})
	got := out.(model)
	if !got.showBacklog || len(got.items) != 1 || got.items[0].Status != models.StatusBacklog {
		t.Errorf("stale load replaced current backlog view: %+v", got)
	}
}

func TestHiddenBacklogLabel(t *testing.T) {
	if got := hiddenBacklogLabel(0); got != "" {
		t.Errorf("zero should render nothing, got %q", got)
	}
	if got := hiddenBacklogLabel(1); got != "1 backlog item (b)" {
		t.Errorf("singular: got %q", got)
	}
	if got := hiddenBacklogLabel(3); got != "3 backlog items (b)" {
		t.Errorf("plural: got %q", got)
	}
}

func TestPrepareLeavesTheInputAlone(t *testing.T) {
	// prepare runs on the store's slice; sorting it in place would reorder the
	// caller's data as a side effect.
	all := []models.Item{
		item(models.ChannelInbox, models.StatusBacklog, t0),
		item(models.ChannelInbox, models.StatusPendingAgent, t0),
	}
	channelView(models.ChannelInbox).prepare(all, true) //nolint:errcheck

	if all[0].Status != models.StatusBacklog {
		t.Errorf("input was reordered: %q first", all[0].Status)
	}
}

func TestViewLabels(t *testing.T) {
	for _, tc := range []struct {
		view listView
		want string
	}{
		{channelView(models.ChannelInbox), "Inbox"},
		{archiveView, "Archive"},
	} {
		if got := tc.view.label(); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}

func TestHiddenBacklogLabelFitsTheNarrowestList(t *testing.T) {
	// The list column bottoms out at listMinContentWidth. A label wider than
	// that gets wrapped by lipgloss mid-phrase, which reads as a typo.
	for _, n := range []int{1, 2, 9, 10, 99, 100, 999} {
		if got := hiddenBacklogLabel(n); len([]rune(got)) > listMinContentWidth {
			t.Errorf("n=%d: %q is %d wide, max %d", n, got, len([]rune(got)), listMinContentWidth)
		}
	}
}

func TestHiddenBacklogRowFillsTheColumn(t *testing.T) {
	label := hiddenBacklogLabel(3)
	for _, w := range []int{25, 28, 34, 46, 60} {
		got := hiddenBacklogRow(label, w)
		if n := len([]rune(got)); n != w {
			t.Errorf("width %d: row is %d wide: %q", w, n, got)
		}
		if !strings.Contains(got, label) {
			t.Errorf("width %d: label missing from %q", w, got)
		}
	}
}

func TestHiddenBacklogRowCentresTheLabel(t *testing.T) {
	got := hiddenBacklogRow("abc", 13)
	// 13 - 3 - 2 spaces = 8 rule characters, split evenly.
	if got != "──── abc ────" {
		t.Errorf("got %q", got)
	}
}

func TestHiddenBacklogRowDropsRulesWhenTooNarrow(t *testing.T) {
	// A single dash on one side reads as damage rather than decoration, and
	// the label is the half that carries meaning.
	label := hiddenBacklogLabel(3)
	for _, w := range []int{0, 1, listMinContentWidth} {
		if got := hiddenBacklogRow(label, w); got != label {
			t.Errorf("width %d: got %q, want the bare label", w, got)
		}
	}
}

func TestHiddenBacklogRowIsEmptyWithoutALabel(t *testing.T) {
	if got := hiddenBacklogRow("", 40); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestWindowListRowsFitsWithoutScrolling(t *testing.T) {
	rows := []string{"a", "b", "c"}
	got, offset, total := windowListRows(rows, 0, 1, 10)
	if len(got) != 3 {
		t.Errorf("got %d rows, want all 3: %v", len(got), got)
	}
	if offset != 0 || total != 3 {
		t.Errorf("got offset=%d total=%d, want 0, 3", offset, total)
	}
}

func TestWindowListRowsFallsForwardIfGivenStartDoesNotFitSelection(t *testing.T) {
	// The defensive fallback: start=0 does not actually fit selecting the
	// last row in a 4-line budget, so windowListRows must still push forward
	// far enough to show it rather than clip it. In normal operation the
	// caller (ensureListOffsetVisible) is what keeps start honest; this
	// covers the case where it isn't.
	rows := []string{"a\nA", "b\nB", "c\nC", "d\nD", "e\nE"}
	got, offset, total := windowListRows(rows, 0, 4, 4)
	want := []string{"d\nD", "e\nE"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if offset != 6 || total != 10 {
		t.Errorf("got offset=%d total=%d, want 6, 10", offset, total)
	}
}

func TestWindowListRowsKeepsAGivenStartThatStillFitsSelection(t *testing.T) {
	// This is the behavior the earlier from-scratch search got wrong: a
	// start that already shows the selection must not move just because a
	// tighter-fitting start also exists.
	rows := []string{"a\nA", "b\nB", "c\nC", "d\nD", "e\nE"}
	got, offset, total := windowListRows(rows, 2, 3, 4)
	want := []string{"c\nC", "d\nD"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if offset != 4 || total != 10 {
		t.Errorf("got offset=%d total=%d, want 4, 10", offset, total)
	}
}

func TestWindowListRowsClampsStartDownWhenSelectionMovesAboveIt(t *testing.T) {
	rows := []string{"a\nA", "b\nB", "c\nC", "d\nD", "e\nE"}
	got, offset, total := windowListRows(rows, 3, 1, 4)
	want := []string{"b\nB", "c\nC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if offset != 2 || total != 10 {
		t.Errorf("got offset=%d total=%d, want 2, 10", offset, total)
	}
}

func TestWindowListRowsIsANoopWithoutABudget(t *testing.T) {
	rows := []string{"a", "b"}
	got, offset, total := windowListRows(rows, 0, 0, 0)
	if !reflect.DeepEqual(got, rows) {
		t.Errorf("got %v, want the unfiltered rows", got)
	}
	if offset != 0 || total != 2 {
		t.Errorf("got offset=%d total=%d, want 0, 2", offset, total)
	}
}

func TestEnsureListOffsetVisibleHoldsStillWhileSelectionStaysInWindow(t *testing.T) {
	var items []models.Item
	for i := 0; i < 20; i++ {
		items = append(items, mkItem(fmt.Sprintf("I%02d", i), models.StatusPendingUser))
	}
	// height=10 → mainH=8 → availH=6, and each short-titled row is 2 lines,
	// so 3 rows are visible per page.
	m := model{items: items, width: 100, height: 10}

	m.selected = 10
	m.listOffset = m.ensureListOffsetVisible()
	scrolledOffset := m.listOffset
	if scrolledOffset == 0 {
		t.Fatalf("selecting row 10 should have scrolled the window, offset=%d", scrolledOffset)
	}

	// Moving up by one row, while the selection is still inside the visible
	// window, must leave the window exactly where it was.
	m.selected = 9
	m.listOffset = m.ensureListOffsetVisible()
	if m.listOffset != scrolledOffset {
		t.Errorf("offset moved from %d to %d on an in-window up-arrow", scrolledOffset, m.listOffset)
	}
}

func TestEnsureListOffsetVisibleScrollsUpByOneAtTheTopEdge(t *testing.T) {
	var items []models.Item
	for i := 0; i < 20; i++ {
		items = append(items, mkItem(fmt.Sprintf("I%02d", i), models.StatusPendingUser))
	}
	m := model{items: items, width: 100, height: 10}
	m.selected = 10
	m.listOffset = m.ensureListOffsetVisible() // scroll down first

	before := m.listOffset
	m.selected = before - 1 // one row above the current window
	m.listOffset = m.ensureListOffsetVisible()
	if m.listOffset != before-1 {
		t.Errorf("got offset=%d, want %d (window scrolls up by exactly the one row needed)", m.listOffset, before-1)
	}
}

func TestTitleInputFitsTheDraftRow(t *testing.T) {
	// textinput renders Width+1 columns (a cell for the cursor past the end of
	// the text) and the draft row prefixes two more. Budgeting for only the
	// prefix overflowed the row by one column, which made lipgloss wrap every
	// draft row and the trailing cell flicker as the cursor blinked.
	const prefix = "› "
	for _, termW := range []int{80, 100, 120, 160} {
		m := model{width: termW}
		colW := m.listWidth() - 2

		ti := textinput.New()
		ti.Prompt = ""
		ti.Width = m.titleWidth()
		ti.Focus()

		for _, valueLen := range []int{0, 1, colW, colW * 3} {
			ti.SetValue(strings.Repeat("x", valueLen))
			ti.CursorEnd()
			if got := lipgloss.Width(prefix + ti.View()); got > colW {
				t.Errorf("term=%d len=%d: draft row is %d wide, column is %d",
					termW, valueLen, got, colW)
			}
		}
	}
}

func TestRenderDraftTitlePaintsTrailingCells(t *testing.T) {
	style := lipgloss.NewStyle().Background(selectedBg).Bold(true)
	// Simulate textinput's visible cursor, including the reset it emits after
	// the cursor cell. The fill must appear after that reset, not inside a
	// parent style that the reset can cancel.
	title := "draft\x1b[0m"
	got := renderDraftTitle(style, 20, title)
	if width := lipgloss.Width(got); width != 20 {
		t.Fatalf("draft title width = %d, want 20", width)
	}
	if want := title + strings.Repeat(" ", 13); got != "› "+want {
		t.Errorf("trailing fill did not follow the cursor reset: got %q, want %q", got, "› "+want)
	}
}

func TestRenderStyledANSIPreservesStyleAfterReset(t *testing.T) {
	style := lipgloss.NewStyle().Background(selectedBg).Bold(true)
	got := renderStyledANSI(style, "before\x1b[0mafter")
	if want := "before\x1b[0mafter"; got != want {
		t.Errorf("renderStyledANSI() = %q, want %q", got, want)
	}
}

// blankFrame stands in for a composed application frame: uniform rows, so any
// row the box did not touch is recognisable by its filler.
func blankFrame(width, height int, filler string) string {
	rows := make([]string, height)
	for i := range rows {
		rows[i] = strings.Repeat(filler, width)
	}
	return strings.Join(rows, "\n")
}

func TestQuitConfirmationIsCenteredOnTheWholeFrame(t *testing.T) {
	// Centred on the screen, not on the reading pane: the pane is offset by the
	// item list, so centring inside it lands visibly left of centre.
	const width, height = 100, 30
	box := lipgloss.NewStyle().Border(lipgloss.ThickBorder()).Render("stop?")
	boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)

	out := overlayCentered(blankFrame(width, height, "·"), box, width)

	rows := strings.Split(out, "\n")
	if len(rows) != height {
		t.Fatalf("overlay changed the frame height: %d, want %d", len(rows), height)
	}
	var first, last = -1, -1
	for i, row := range rows {
		if strings.Contains(row, "┏") || strings.Contains(row, "┃") || strings.Contains(row, "┗") {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		t.Fatal("box was not drawn")
	}
	if above, below := first, height-1-last; above-below > 1 || below-above > 1 {
		t.Errorf("not vertically centred: %d rows above, %d below", above, below)
	}
	// The filler either side of the box says where it sits horizontally. Count
	// columns rather than bytes: the filler and the border are both multi-byte.
	left := 0
	for _, r := range ansi.Strip(rows[first]) {
		if r == '┏' {
			break
		}
		left++
	}
	right := width - left - boxW
	if left-right > 1 || right-left > 1 {
		t.Errorf("not horizontally centred: %d columns left, %d right", left, right)
	}
	if boxH != last-first+1 {
		t.Errorf("box occupies %d rows, want %d", last-first+1, boxH)
	}
}

func TestQuitConfirmationLeavesTheFrameAroundItIntact(t *testing.T) {
	// A modal that blanked full-width bands through the list and the borders
	// would read as a rendering fault rather than as a box on top.
	const width, height = 100, 30
	box := lipgloss.NewStyle().Border(lipgloss.ThickBorder()).Render("stop?")

	out := overlayCentered(blankFrame(width, height, "·"), box, width)

	for i, row := range strings.Split(out, "\n") {
		if w := lipgloss.Width(row); w != width {
			t.Fatalf("row %d is %d columns wide, want %d", i, w, width)
		}
		if !strings.HasPrefix(row, "·") || !strings.HasSuffix(row, "·") {
			t.Errorf("row %d lost the frame beside the box: %q", i, row)
		}
	}
}

func TestQuitConfirmationShrinksRatherThanWrapping(t *testing.T) {
	// A frame wider than the screen wraps, and a wrapped border reads as broken.
	m := newModel(nil, nil, nil)
	m.width = 30

	if w := lipgloss.Width(m.quitConfirmBox()); w > m.width {
		t.Errorf("box is %d columns wide on a %d-column screen", w, m.width)
	}
}

func TestQuitConfirmationTakesOnlyY(t *testing.T) {
	// The key that lands here was pressed to leave, not to answer a question,
	// so anything but an explicit yes has to mean "stay".
	m := newModel(nil, nil, nil)
	m.mode = modeQuit

	if _, cmd := m.handleQuitKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd == nil {
		t.Error("y did not quit")
	}
	next, cmd := m.handleQuitKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if cmd != nil {
		t.Error("an unrelated key quit")
	}
	if next.(model).mode != modeNav {
		t.Error("an unrelated key left the confirmation up")
	}
}

func TestQuitWithoutASupervisorDoesNotAskFirst(t *testing.T) {
	// No supervisor means no turn to lose; q must still just quit.
	m := newModel(nil, nil, nil)
	next, cmd := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Error("q did not quit")
	}
	if next.(model).mode != modeNav {
		t.Error("q opened a confirmation with no dispatch running")
	}
}

func TestNewModelUsesALegibleDraftPlaceholder(t *testing.T) {
	m := newModel(nil, nil, nil)
	if got := m.title.Placeholder; got != "new item title…" {
		t.Errorf("draft placeholder = %q", got)
	}
	// The textinput default is ANSI colour 240, which is too close to the
	// selected-row background (237). The configured style uses the legible
	// metadata grey instead.
	if got := m.title.PlaceholderStyle.GetForeground(); got != dimFg {
		t.Errorf("placeholder foreground = %v, want %v", got, dimFg)
	}
	if m.input.KeyMap.Paste.Enabled() || m.title.KeyMap.Paste.Enabled() {
		t.Error("host clipboard shortcut must be disabled; terminal bracketed paste is supported instead")
	}
}

func TestFooterUsesThreeViewKeyRange(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width = 200
	got := m.renderFooter()
	if !strings.Contains(got, "1-3 view") {
		t.Errorf("footer does not advertise the three-view key range: %q", got)
	}
	if strings.Contains(got, "1-4") {
		t.Errorf("footer still advertises the removed fourth view: %q", got)
	}
}

func TestProjectContextUsesTheThirdViewKey(t *testing.T) {
	st, err := store.NewStore(filepath.Join(t.TempDir(), ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(st, nil, nil)
	m.conv.Width = 80

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	got := next.(model)
	if got.projectPane != 1 {
		t.Fatalf("key 3 opened project pane %d, want 1", got.projectPane)
	}

	next, _ = got.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if next.(model).projectPane != 1 {
		t.Error("key 3 left the project context while it was selected")
	}

	next, _ = got.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if next.(model).projectPane != 1 {
		t.Error("removed key 5 still changed the project context")
	}
}

func TestProjectContextHasOneActiveTab(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width = 120
	m.view = archiveView
	m.projectPane = 2

	header := ansi.Strip(m.renderHeader())
	if got := strings.Count(header, "["); got != 1 {
		t.Fatalf("project context header has %d active tabs, want 1: %q", got, header)
	}
	if !strings.Contains(header, "[Project Context]") {
		t.Fatalf("project context was not the active tab: %q", header)
	}
}

func TestProjectBriefContextListsCurrentAndPreviousVersions(t *testing.T) {
	st, err := store.NewStore(filepath.Join(t.TempDir(), ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceProjectBrief("first brief"); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceProjectBrief("current brief"); err != nil {
		t.Fatal(err)
	}

	m := newModel(st, nil, nil)
	m.projectPane = 2
	m.conv.Width = 80
	m.conv.Height = 20
	m.showProjectContext()
	if len(m.projectEntries) != 2 {
		t.Fatalf("project entries = %d, want current plus one previous version", len(m.projectEntries))
	}
	if m.projectEntries[0].content != "current brief" || !m.projectEntries[0].editable {
		t.Fatalf("current entry = %+v", m.projectEntries[0])
	}
	if m.projectEntries[1].content != "first brief" || m.projectEntries[1].editable {
		t.Fatalf("history entry = %+v", m.projectEntries[1])
	}

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(model)
	if got.selected != 1 || !strings.Contains(got.conv.View(), "first brief") {
		t.Fatalf("selecting history did not show it: selected=%d view=%q", got.selected, got.conv.View())
	}
}

func TestProjectBriefHistoryIsReadOnly(t *testing.T) {
	st, err := store.NewStore(filepath.Join(t.TempDir(), ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceProjectBrief("first brief"); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceProjectBrief("current brief"); err != nil {
		t.Fatal(err)
	}

	m := newModel(st, nil, nil)
	m.projectPane = 2
	m.showProjectContext()
	m.selected = 1
	next, cmd := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got := next.(model)
	if got.mode != modeNav || cmd != nil {
		t.Fatalf("history entry opened editor: mode=%v cmd=%v", got.mode, cmd)
	}
}

func TestProjectContextIgnoresQueuedItemReloads(t *testing.T) {
	m := model{
		view:        channelView(models.ChannelInbox),
		projectPane: 1,
		convItemID:  "project-instructions",
	}

	out, _ := m.Update(itemsLoadedMsg{
		items:       []models.Item{mkItem("item-1", models.StatusAgentAcknowledged)},
		view:        m.view,
		showBacklog: false,
	})
	got := out.(model)
	if got.projectPane != 1 {
		t.Fatalf("project pane = %d, want 1", got.projectPane)
	}
	if len(got.items) != 0 {
		t.Fatalf("queued item reload populated %d items while project context was open", len(got.items))
	}
	if got.convItemID != "project-instructions" {
		t.Fatalf("queued item reload replaced project content with %q", got.convItemID)
	}
}

func TestProjectContextWatcherOnlyRearms(t *testing.T) {
	watchCh := make(chan struct{}, 1)
	watchCh <- struct{}{}
	m := model{
		view:        channelView(models.ChannelInbox),
		projectPane: 2,
		watchCh:     watchCh,
	}

	_, cmd := m.Update(watchEventMsg{})
	if cmd == nil {
		t.Fatal("project context watcher was not re-armed")
	}
	if msg := cmd(); msg != (watchEventMsg{}) {
		t.Fatalf("watcher command returned %T, want watchEventMsg", msg)
	}
}

func TestPageKeysScrollConversationByHalfAPane(t *testing.T) {
	keyDown := tea.KeyMsg{Type: tea.KeyPgDown}
	keyUp := tea.KeyMsg{Type: tea.KeyPgUp}

	tests := []struct {
		name string
		page func(model, tea.KeyMsg) model
	}{
		{
			name: "item navigation",
			page: func(m model, key tea.KeyMsg) model {
				next, _ := m.handleNavKey(key)
				return next.(model)
			},
		},
		{
			name: "project navigation",
			page: func(m model, key tea.KeyMsg) model {
				m.projectPane = 1
				next, _ := m.handleNavKey(key)
				return next.(model)
			},
		},
		{
			name: "turn composer",
			page: func(m model, key tea.KeyMsg) model {
				m.mode = modeCompose
				next, _ := m.handleInputKey(key)
				return next.(model)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(nil, nil, nil)
			m.conv.Height = 20
			m.conv.SetContent(strings.Repeat("line\n", 100))
			m.conv.SetYOffset(30)

			m = tt.page(m, keyDown)
			if got := m.conv.YOffset; got != 40 {
				t.Fatalf("PgDn offset = %d, want 40", got)
			}
			m = tt.page(m, keyUp)
			if got := m.conv.YOffset; got != 30 {
				t.Errorf("PgUp offset = %d, want 30", got)
			}
		})
	}
}

func TestPageKeyStillScrollsAOneLineConversationPane(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.conv.Height = 1
	m.conv.SetContent("one\ntwo\nthree")

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyPgDown})
	if got := next.(model).conv.YOffset; got != 1 {
		t.Errorf("PgDn offset = %d, want 1", got)
	}
}

// mkItem builds a minimal inbox item for the selection tests below.
func mkItem(id string, st models.Status) models.Item {
	return models.Item{ID: id, Channel: models.ChannelInbox, Status: st, Created: t0, Title: id}
}

// loadInto runs the real itemsLoadedMsg path, which is where selection is
// re-established after any reload.
func loadInto(t *testing.T, m model, items []models.Item) model {
	t.Helper()
	out, _ := m.Update(itemsLoadedMsg{items: items, view: m.view, showBacklog: m.showBacklog})
	return out.(model)
}

func newSelectionModel(t *testing.T, items []models.Item, selected int) model {
	t.Helper()
	st, err := store.NewStore(filepath.Join(t.TempDir(), ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	m := model{
		store: st, view: channelView(models.ChannelInbox),
		items: items, selected: selected, width: 120, height: 40,
	}
	m.convItemID = m.selectedID()
	return m
}

func TestSelectionFollowsAnItemThatResorts(t *testing.T) {
	// A status change re-orders the list, so the row index is not stable
	// across a reload — the id is.
	before := []models.Item{
		mkItem("A", models.StatusPendingUser),
		mkItem("B", models.StatusPendingUser),
		mkItem("C", models.StatusPendingUser),
	}
	m := newSelectionModel(t, before, 2)

	// C is picked up by the agent and sorts to the top.
	after := []models.Item{
		mkItem("C", models.StatusAgentAcknowledged),
		mkItem("A", models.StatusPendingUser),
		mkItem("B", models.StatusPendingUser),
	}
	got := loadInto(t, m, after)

	if got.selectedID() != "C" {
		t.Errorf("selection landed on %q, want C", got.selectedID())
	}
	if got.selected != 0 {
		t.Errorf("selected index is %d, want 0", got.selected)
	}
}

func TestSelectionHoldsItsRowWhenTheItemLeavesTheView(t *testing.T) {
	// Archiving the selected item, or parking it in backlog with the filter
	// on, removes it from this view. Leaving selected untouched pointed it
	// past the end of the list and blanked the reading pane.
	before := []models.Item{
		mkItem("A", models.StatusPendingUser),
		mkItem("B", models.StatusPendingUser),
		mkItem("C", models.StatusPendingUser),
	}
	m := newSelectionModel(t, before, 2)

	got := loadInto(t, m, before[:2]) // C archived

	if got.selected >= len(got.items) {
		t.Fatalf("selected %d is past the end of %d items", got.selected, len(got.items))
	}
	if got.selectedID() != "B" {
		t.Errorf("selection landed on %q, want the last remaining row B", got.selectedID())
	}
}

func TestSelectionSurvivesAnEmptiedView(t *testing.T) {
	m := newSelectionModel(t, []models.Item{mkItem("A", models.StatusPendingUser)}, 0)

	got := loadInto(t, m, nil)

	if got.selected != 0 {
		t.Errorf("selected = %d on an empty list, want 0", got.selected)
	}
	if got.selectedID() != "" {
		t.Errorf("selectedID = %q on an empty list, want empty", got.selectedID())
	}
}

func TestDraftKeepsItsRowPastTheEnd(t *testing.T) {
	// A draft parks selected one past the last item on purpose; clamping it
	// would drop the cursor onto a real item and hide the draft row.
	items := []models.Item{mkItem("A", models.StatusPendingUser)}
	m := newSelectionModel(t, items, 1)
	m.draft = true

	got := loadInto(t, m, items)

	if got.selected != 1 {
		t.Errorf("draft selection moved to %d, want 1 (one past the end)", got.selected)
	}
}

func TestChildDraftStaysWithItsParentFamily(t *testing.T) {
	root := mkItem("root", models.StatusActive)
	root.Title = "root title"
	child := mkItem("child", models.StatusBacklog)
	child.Parent = root.ID
	child.Title = "existing child"
	other := mkItem("other", models.StatusActive)
	other.Title = "other root"

	m := model{
		items:       []models.Item{root, child, other},
		allItems:    []models.Item{root, child, other},
		selected:    3,
		draft:       true,
		draftParent: root.ID,
		width:       100,
		height:      40,
		view:        channelView(models.ChannelInbox),
		collapsed:   make(map[string]bool),
	}
	m.title.SetValue("new child")

	if got := m.draftRowIndex(); got != 2 {
		t.Fatalf("draft row = %d, want 2 after the parent family", got)
	}
	content, _ := m.renderList(20)
	plain := ansi.Strip(content)
	rootAt := strings.Index(plain, "root title")
	childAt := strings.Index(plain, "existing child")
	draftAt := strings.Index(plain, "new child")
	otherAt := strings.Index(plain, "other root")
	if rootAt < 0 || childAt < 0 || draftAt < 0 || otherAt < 0 {
		t.Fatalf("list is missing a row: %q", plain)
	}
	if !(rootAt < childAt && childAt < draftAt && draftAt < otherAt) {
		t.Fatalf("draft was not rendered after its parent family: %q", plain)
	}
	if !strings.Contains(plain, "├─ › new child") {
		t.Fatalf("child draft is not indented as a subthread: %q", plain)
	}
}

func TestCancelChildDraftRestoresParentSelection(t *testing.T) {
	root := mkItem("root", models.StatusActive)
	child := mkItem("child", models.StatusBacklog)
	child.Parent = root.ID
	other := mkItem("other", models.StatusActive)

	m := newSelectionModel(t, []models.Item{root, child, other}, 3)
	m.mode = modeTitle
	m.draft = true
	m.draftParent = root.ID

	got := m.cancelDraft()

	if got.selectedID() != root.ID {
		t.Errorf("selection landed on %q, want parent %q", got.selectedID(), root.ID)
	}
	if got.selected != 0 {
		t.Errorf("selected index = %d, want 0", got.selected)
	}
}
