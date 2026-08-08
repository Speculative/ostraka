package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ostraka/internal/models"
	"ostraka/internal/store"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
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

func TestChannelViewExcludesOtherChannels(t *testing.T) {
	v := channelView(models.ChannelInbox)
	if v.includes(item(models.ChannelAsks, models.StatusActive, t0), true) {
		t.Error("an asks item appeared in the inbox view")
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

func TestArchiveViewTakesTerminalItemsFromEveryChannel(t *testing.T) {
	for _, ch := range []models.Channel{models.ChannelInbox, models.ChannelAsks, models.ChannelHandoff} {
		if !archiveView.includes(item(ch, models.StatusArchived, t0), false) {
			t.Errorf("archived %s item missing from the archive", ch)
		}
		if archiveView.includes(item(ch, models.StatusActive, t0), true) {
			t.Errorf("live %s item leaked into the archive", ch)
		}
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
		item(models.ChannelAsks, models.StatusActive, t0),                              // wrong channel
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
		item(models.ChannelAsks, models.StatusBacklog, t0),   // other channel
		item(models.ChannelInbox, models.StatusArchived, t0), // terminal
		item(models.ChannelInbox, models.StatusBacklog, t0),  // the only one
	}
	if _, hidden := channelView(models.ChannelInbox).prepare(all, false); hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	if _, hidden := channelView(models.ChannelInbox).prepare(all, true); hidden != 0 {
		t.Errorf("hidden = %d with the toggle on, want 0", hidden)
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
		{channelView(models.ChannelAsks), "Asks"},
		{channelView(models.ChannelHandoff), "Handoff"},
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

// mkItem builds a minimal inbox item for the selection tests below.
func mkItem(id string, st models.Status) models.Item {
	return models.Item{ID: id, Channel: models.ChannelInbox, Status: st, Created: t0, Title: id}
}

// loadInto runs the real itemsLoadedMsg path, which is where selection is
// re-established after any reload.
func loadInto(t *testing.T, m model, items []models.Item) model {
	t.Helper()
	out, _ := m.Update(itemsLoadedMsg{items: items})
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
