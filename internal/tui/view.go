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
	models.StatusProposed:          4,
	models.StatusBacklog:           5,
	models.StatusDone:              6,
	models.StatusArchived:          7,
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

// rootID identifies the shallow tree owner used by the inbox renderer. A
// malformed grandchild is still displayed under its recorded parent; the
// store rejects new ones, while the UI remains tolerant of older files.
func rootID(item models.Item) string {
	if item.Parent != "" {
		return item.Parent
	}
	return item.ID
}

// prepareGrouped filters and sorts roots as families, then expands each root
// into its direct children unless that root is folded. The returned slice is
// still a flat selection model, which keeps keyboard navigation and existing
// conversation code simple while rendering a one-level tree.
func (v listView) prepareGrouped(all []models.Item, showBacklog bool, collapsed map[string]bool) ([]models.Item, int) {
	// Unit-level view tests and a partially written legacy file may have no
	// stable ID. Grouping such rows would merge unrelated roots under the empty
	// map key, so retain the old flat behavior until IDs are available.
	for _, item := range all {
		if item.ID == "" {
			return v.prepare(all, showBacklog)
		}
	}
	visible := make(map[string]models.Item)
	hidden := 0
	for _, item := range all {
		if v.includes(item, showBacklog) {
			visible[item.ID] = item
		} else if !showBacklog && v.includes(item, true) {
			hidden++
		}
	}

	groups := make(map[string][]models.Item)
	for _, item := range visible {
		groups[rootID(item)] = append(groups[rootID(item)], item)
	}
	type itemFamily struct {
		key     string
		root    models.Item
		members []models.Item
		hasRoot bool
	}
	families := make([]itemFamily, 0, len(groups))
	for id, family := range groups {
		root, hasRoot := visible[id]
		if !hasRoot {
			// A terminal child can be visible in the archive while its live
			// parent remains in the inbox. There is no parent row to render in
			// this view, so keep every child as a detached row. Using the first
			// child as a fake root loses its siblings and makes folding use the
			// wrong ID.
			root = family[0]
		}
		families = append(families, itemFamily{key: id, root: root, members: family, hasRoot: hasRoot})
	}
	sort.SliceStable(families, func(i, j int) bool {
		ri, rj := familyRank(families[i].key, families[i].root, all), familyRank(families[j].key, families[j].root, all)
		if ri != rj {
			return ri < rj
		}
		ai, aj := familyActivity(families[i].key, families[i].root, all), familyActivity(families[j].key, families[j].root, all)
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return families[i].key < families[j].key
	})

	out := make([]models.Item, 0, len(visible))
	for _, family := range families {
		members := append([]models.Item(nil), family.members...)
		sort.SliceStable(members, func(i, j int) bool {
			if !members[i].Created.Equal(members[j].Created) {
				return members[i].Created.Before(members[j].Created)
			}
			return members[i].ID < members[j].ID
		})
		if !family.hasRoot {
			// The parent is outside this view. Do not imply that one child is
			// the parent of its siblings; all terminal members remain directly
			// selectable in the archive.
			for _, member := range members {
				member.Parent = ""
				out = append(out, member)
			}
			continue
		}
		out = append(out, visible[family.root.ID])
		if collapsed[family.key] {
			continue
		}
		for _, child := range members {
			if child.ID != family.root.ID {
				out = append(out, child)
			}
		}
	}
	return out, hidden
}

func familyRank(key string, root models.Item, all []models.Item) int {
	rank := rankOf(root.Status)
	for _, item := range all {
		if rootID(item) == key && rankOf(item.Status) < rank {
			rank = rankOf(item.Status)
		}
	}
	return rank
}

func familyActivity(key string, root models.Item, all []models.Item) time.Time {
	latest := lastActivity(root)
	for _, item := range all {
		if rootID(item) == key && lastActivity(item).After(latest) {
			latest = lastActivity(item)
		}
	}
	return latest
}

func familyCounts(root models.Item, all []models.Item) (open, done int) {
	for _, item := range all {
		if rootID(item) != root.ID {
			continue
		}
		if models.TerminalStatuses[item.Status] {
			done++
		} else if item.ID != root.ID {
			open++
		}
	}
	return open, done
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
