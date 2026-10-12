package tui

import (
	"fmt"
	"github.com/Speculative/ostraka/internal/models"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/charmbracelet/lipgloss"
)

var reviewThreadBg = lipgloss.Color("17")

type reviewCodeLocation struct {
	side string
	line int
}
type reviewThreadRef struct {
	reviewID string
	number   int
	itemID   string
}

// Verify the whole attachment before painting any of its lines. Stale and
// detached attachments remain in the thread reader, never on unrelated code.
func (m *model) indexReviewCodeThreads(file reviewdiff.File) {
	m.reviewCodeMarkers = make(map[reviewCodeLocation][]reviewThreadRef)
	for _, review := range m.reviewReviews {
		for _, thread := range review.Threads {
			anchor := thread.Anchor
			if anchor == nil {
				continue
			}
			path := file.Path
			if anchor.Side == "old" && file.OldPath != "" {
				path = file.OldPath
			}
			if anchor.File != path || !reviewAnchorMatchesDiff(m.reviewDiff, anchor) {
				continue
			}
			ref := reviewThreadRef{review.ID, thread.Number, review.ItemID}
			for _, line := range file.Lines {
				n := line.NewLine
				if anchor.Side == "old" {
					n = line.OldLine
				}
				if n > 0 && n >= anchor.StartLine && n <= anchor.EndLine {
					key := reviewCodeLocation{anchor.Side, n}
					m.reviewCodeMarkers[key] = append(m.reviewCodeMarkers[key], ref)
				}
			}
		}
	}
}

func (m model) reviewLineThreads(line reviewdiff.Line, side string) []reviewThreadRef {
	var refs []reviewThreadRef
	if side == "" || side == "old" {
		refs = append(refs, m.reviewCodeMarkers[reviewCodeLocation{"old", line.OldLine}]...)
	}
	if side == "" || side == "new" {
		refs = append(refs, m.reviewCodeMarkers[reviewCodeLocation{"new", line.NewLine}]...)
	}
	return refs
}
func (m model) reviewLineHasThread(line reviewdiff.Line, side string) bool {
	return len(m.reviewLineThreads(line, side)) > 0
}
func (m model) reviewThreadBadge(line reviewdiff.Line, side string, style lipgloss.Style) string {
	refs := m.reviewLineThreads(line, side)
	if len(refs) == 0 {
		return ""
	}
	var labels []string
	for _, ref := range refs {
		label := fmt.Sprintf("#%d", ref.number)
		// Thread numbers are review-scoped. Disambiguate overlapping reviews.
		if len(refs) > 1 {
			label += "@" + ref.itemID
		}
		labels = append(labels, label)
	}
	return style.Foreground(lipgloss.Color("81")).Render("[" + strings.Join(labels, ",") + "] ")
}

func (m *model) openReviewThreadsAtCursor() bool {
	file, ok := m.selectedReviewFile()
	if !ok {
		return false
	}
	m.indexReviewCodeThreads(file)
	line := m.currentReviewLine()
	if line == nil {
		return false
	}
	side := ""
	if m.reviewSplit {
		side = m.reviewSide
	}
	refs := m.reviewLineThreads(*line, side)
	if len(refs) == 0 {
		return false
	}
	target := refs[0]
	m.openReviewThreads()
	index := reviewIndexByID(m.reviewReviews, target.reviewID)
	if index < 0 {
		return false
	}
	m.selected = index
	m.selectReviewEntry()
	m.reviewThreadCursor = threadIndexByNumber(m.reviewReviews, target.reviewID, target.number)
	m.focus = focusReadingPane
	m.updateConv()
	// Opening from code always brings the target's beginning into view.
	if m.reviewThreadCursor < len(m.reviewThreadOffsets) {
		m.conv.SetYOffset(m.reviewThreadOffsets[m.reviewThreadCursor])
	}
	return true
}

func (m *model) focusFirstReviewChange() {
	file, ok := m.selectedReviewFile()
	if !ok {
		return
	}
	m.reviewAnchor = -1
	m.reviewSelecting = false
	for i, line := range file.Lines {
		if (line.Kind == '+' || line.Kind == '-') && m.reviewNavigableLine(file, i) {
			m.reviewCursor = i
			return
		}
	}
	// A hunk can contain changes only on the other side of a split view.
	m.reviewCursor = m.reviewIndexOnSide(file, firstReviewCodeLine(file))
}

// Return to the selected thread, rather than the location that opened the
// reader. Validate the excerpt before placing a cursor on potentially stale code.
func (m *model) returnToReviewThreadCode() {
	_, thread, ok := m.currentReviewThread()
	anchor := thread.Anchor
	m.closeReviewThreads()
	m.focus = focusReadingPane
	m.reviewAnchor = -1
	m.reviewSelecting = false
	if !ok || anchor == nil {
		m.updateConv()
		return
	}
	found := false
	for i, file := range m.reviewDiff.Files {
		path := file.Path
		if anchor.Side == "old" && file.OldPath != "" {
			path = file.OldPath
		}
		if path == anchor.File {
			m.selected = i
			found = true
			break
		}
	}
	if !found || !reviewAnchorMatchesDiff(m.reviewDiff, anchor) {
		m.err = fmt.Errorf("thread attachment unavailable or changed: %s:%d; refresh or ask the agent to relocate it", anchor.File, anchor.StartLine)
		if found {
			m.focusFirstReviewChange()
		}
		m.updateConv()
		return
	}
	m.reviewSide = anchor.Side
	locate := func() int {
		file, _ := m.selectedReviewFile()
		for i, line := range file.Lines {
			n := line.NewLine
			if anchor.Side == "old" {
				n = line.OldLine
			}
			if n == anchor.StartLine {
				return i
			}
		}
		return -1
	}
	index := locate()
	if index < 0 {
		m.reviewFullContext = true
		index = locate()
	}
	m.reviewCursor = index
	m.err = nil
	m.updateConv()
	m.revealReviewCursor()
}

func (m model) reviewFileEntries() []projectEntry {
	entries := reviewFileEntries(m.reviewDiff, m.reviewLoading)
	for i, file := range m.reviewDiff.Files {
		count, fresh := 0, false
		for _, review := range m.reviewReviews {
			for _, thread := range review.Threads {
				anchor := thread.Anchor
				if anchor == nil {
					continue
				}
				path := file.Path
				if anchor.Side == "old" && file.OldPath != "" {
					path = file.OldPath
				}
				if anchor.File != path {
					continue
				}
				count++
				fresh = fresh || reviewThreadHasNewComments(thread)
			}
		}
		if count > 0 {
			sigil := ""
			if fresh {
				sigil = "+"
			}
			entries[i].threadBadge = fmt.Sprintf("[%d%s] ", count, sigil)
			entries[i].threadNew = fresh
		}
	}
	return entries
}

// Saved drafts are not user turns and do not acknowledge agent comments.
func reviewThreadHasNewComments(thread models.ReviewThread) bool {
	var lastUser time.Time
	for _, message := range thread.Messages {
		if message.Actor == models.ActorUser && message.Submitted {
			at := message.SubmittedAt
			if at.IsZero() {
				at = message.CreatedAt
			}
			if at.After(lastUser) {
				lastUser = at
			}
		}
	}
	for _, message := range thread.Messages {
		if message.Actor == models.ActorAgent && message.Kind != "attachment" && message.CreatedAt.After(lastUser) {
			return true
		}
	}
	return false
}
