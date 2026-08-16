package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"ostraka/internal/models"
)

// listView is what the item list is showing. The inbox is one kind of view;
// the archive is another. Archive is a lifecycle state rather than a channel.
type listView struct {
	channel models.Channel
	archive bool
}

func channelView(ch models.Channel) listView { return listView{channel: ch} }

var archiveView = listView{archive: true}

func (v listView) label() string {
	if v.archive {
		return "Archive"
	}
	ch := string(v.channel)
	return string(ch[0]-32) + ch[1:] // channel names are ASCII lowercase
}

// includes reports whether an item belongs in this view.
//
// Terminal items live in the archive and nowhere else: the store already moves
// them to ARCHIVE/ on disk, and leaving them in their original channel made
// that move invisible — the channel list grew forever and mostly held things
// nobody was going to act on.
func (v listView) includes(item models.Item, showBacklog bool) bool {
	terminal := models.TerminalStatuses[item.Status]
	if v.archive {
		return terminal
	}
	if terminal || item.Channel != v.channel {
		return false
	}
	return showBacklog || item.Status != models.StatusBacklog
}

// statusRank orders the list by who owes the next move: work the agent is
// running now, then work queued for it, then work waiting on the user, then
// everything at rest. The top of the list is where attention should go.
var statusRank = map[models.Status]int{
	models.StatusAgentAcknowledged: 0,
	models.StatusPendingAgent:      1,
	models.StatusPendingUser:       2,
	models.StatusActive:            3,
	models.StatusBacklog:           4,
	models.StatusDone:              5,
	models.StatusArchived:          6,
}

// rankOf places statuses this build does not know about at the end rather than
// the beginning — an item written by a newer CLI should not silently claim the
// most urgent row in the list.
func rankOf(s models.Status) int {
	if r, ok := statusRank[s]; ok {
		return r
	}
	return len(statusRank)
}

// lastActivity is when the item last changed hands. The body is the opening
// statement, so an item with no turns yet is as old as its creation.
func lastActivity(item models.Item) time.Time {
	if n := len(item.Turns); n > 0 {
		return item.Turns[n-1].Timestamp
	}
	return item.Created
}

// sortForDisplay orders by status, then most recent activity first within a
// status. Stable so that items sharing a rank and a timestamp keep whatever
// order the store gave them rather than shuffling between reloads.
func sortForDisplay(items []models.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := rankOf(items[i].Status), rankOf(items[j].Status)
		if ri != rj {
			return ri < rj
		}
		return lastActivity(items[i]).After(lastActivity(items[j]))
	})
}

// prepare filters every item in the store down to this view and orders it,
// also reporting how many backlog items it suppressed. The list says so rather
// than omitting them silently: a row you cannot see and were not told about is
// indistinguishable from one that was lost.
func (v listView) prepare(all []models.Item, showBacklog bool) ([]models.Item, int) {
	out := make([]models.Item, 0, len(all))
	hidden := 0
	for _, item := range all {
		switch {
		case v.includes(item, showBacklog):
			out = append(out, item)
		case !showBacklog && v.includes(item, true):
			// Excluded only by the backlog filter, not by channel or status.
			hidden++
		}
	}
	sortForDisplay(out)
	return out, hidden
}

// listMinContentWidth is the narrowest the list column ever gets: listWidth
// floors at 24 and the panel spends 2 on padding. Anything wider than this
// wraps, and a marker broken across two lines reads as a typo rather than a
// sentence — which is exactly how the first version of this label looked.
const listMinContentWidth = 22

// hiddenBacklogLabel is the marker the list shows in place of the rows it is
// suppressing. It names the key that reveals them, because the count alone
// tells you something is missing without telling you how to get it back.
//
// Kept short enough to fit listMinContentWidth unwrapped, which rules out the
// longer "— b to show" phrasing at an 80-column terminal.
func hiddenBacklogLabel(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 backlog item (b)"
	default:
		return fmt.Sprintf("%d backlog items (b)", n)
	}
}

// hiddenBacklogRow centres the label between horizontal rules, filling the
// column exactly. When the column is too narrow for a rule on each side the
// label is rendered alone: a one-sided or single-dash rule reads as damage
// rather than decoration, and the label is the part that carries meaning.
func hiddenBacklogRow(label string, width int) string {
	if label == "" {
		return ""
	}
	// One space of breathing room either side of the text.
	pad := width - len([]rune(label)) - 2
	if pad < 2 || width <= 0 {
		return label
	}
	left := pad / 2
	return strings.Repeat("─", left) + " " + label + " " + strings.Repeat("─", pad-left)
}
