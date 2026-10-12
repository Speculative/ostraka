package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func markerReviewModel(t *testing.T) model {
	t.Helper()
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "marker target", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	// First thread is unrelated; the code marker must open thread #2.
	if _, err = s.CreateReviewThread(review.ID, nil, models.ActorUser, "item question"); err != nil {
		t.Fatal(err)
	}
	anchor := &models.ReviewAnchor{File: "file.go", Side: "new", StartLine: 2, EndLine: 2, Excerpt: "added one"}
	if _, err = s.CreateReviewThread(review.ID, anchor, models.ActorUser, "code question"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateReviewThread(review.ID, nil, models.ActorUser, strings.Repeat("Long third thread.\n\n", 12)); err != nil {
		t.Fatal(err)
	}
	m := reviewNavigationModel()
	m.store = s
	m.reviewDiff.Files[0].FullLines = nil
	m.focus = focusItemList
	m.refreshReviewList()
	m.conv.Height = 10
	m.updateConv()
	return m
}

func TestReviewMarkerOpensTargetThreadAndReturnsToCode(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := markerReviewModel(t)
	if !strings.Contains(m.conv.View(), "48;5;17") {
		t.Fatal("unfocused thread line lacks marker background")
	}
	m = reviewKey(m, "l")
	if m.reviewCursor != 4 || m.focus != focusReadingPane {
		t.Fatalf("focus started at context: %d", m.reviewCursor)
	}
	view := m.conv.View()
	if !strings.Contains(ansi.Strip(view), "[#2]") {
		t.Fatalf("code marker missing: %q", view)
	}
	m = reviewKey(m, "l")
	if !m.reviewThreadsMode || m.reviewThreadCursor != 1 || m.focus != focusReadingPane {
		t.Fatal("marker did not open matching thread")
	}
	if len(m.reviewThreadOffsets) != 3 {
		t.Fatal("reader did not render all threads")
	}
	top := m.reviewThreadOffsets[1]
	if m.conv.YOffset > top || top >= m.conv.YOffset+m.conv.Height {
		t.Fatal("target thread is outside viewport")
	}
	if !strings.Contains(ansi.Strip(m.conv.View()), "Thread #2") {
		t.Fatal("target thread heading is not visible")
	}
	m = reviewKey(m, "h")
	if m.reviewThreadsMode || m.selectedReviewPath() != "file.go" || m.reviewCursor != 4 {
		t.Fatal("h did not return to same code location")
	}
}

func TestReviewThreadSelectionAndReplyComposer(t *testing.T) {
	m := markerReviewModel(t)
	next, _ := m.handleReviewKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if !m.reviewThreadsMode || m.focus != focusReadingPane {
		t.Fatal("Tab did not focus review threads")
	}
	m = reviewKey(m, "l")
	if m.reviewThreadCursor != 0 || m.focus != focusReadingPane {
		t.Fatal("l did not select first thread")
	}
	m = reviewKey(m, "j")
	m = reviewKey(m, "j")
	if m.reviewThreadCursor != 2 {
		t.Fatal("j did not select next whole thread")
	}
	m = reviewKey(m, "k")
	if m.reviewThreadCursor != 1 {
		t.Fatal("k did not select previous whole thread")
	}
	m = reviewKey(m, "t")
	if m.mode != modeCompose || m.reviewComposer.threadNumber != 2 || m.reviewComposer.reviewID == "" {
		t.Fatal("t did not compose inside selected thread")
	}
	m.input.SetValue("Follow-up without hand-written thread markdown")
	next, _ = m.saveReviewComposer()
	m = next.(model)
	if !m.reviewThreadsMode || m.mode != modeNav {
		t.Fatal("saving left thread reader")
	}
	review, thread, ok := m.currentReviewThread()
	if !ok || thread.Number != 2 || len(thread.Messages) != 2 || thread.Messages[1].Submitted {
		t.Fatalf("follow-up did not stay as unsent thread message: %#v", review)
	}
	m = reviewKey(m, "e")
	if m.mode != modeNav {
		t.Fatal("e still edits a draft")
	}
}

func TestReviewStaleAndDetachedAnchorsDoNotMarkCode(t *testing.T) {
	m := markerReviewModel(t)
	m.reviewReviews[0].Threads[1].Anchor.Excerpt = "different code"
	m.updateConv()
	file, _ := m.selectedReviewFile()
	if m.reviewLineHasThread(file.Lines[4], "new") {
		t.Fatal("stale anchor marked unrelated code")
	}
	m.reviewReviews[0].Threads[1].Anchor = nil
	m.updateConv()
	if m.reviewLineHasThread(file.Lines[4], "new") {
		t.Fatal("detached thread marked code")
	}
}

func TestReviewWholeThreadHighlightIncludesAllRows(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := markerReviewModel(t)
	m.openReviewThreads()
	m.focus = focusReadingPane
	m.reviewThreadCursor = 1
	m.conv.Height = 200
	m.updateConv()
	rows := strings.Split(m.conv.View(), "\n")
	for i := m.reviewThreadOffsets[1]; i < m.reviewThreadEnds[1]; i++ {
		if i >= len(rows) || !strings.Contains(rows[i], "48;5;237") {
			t.Fatalf("thread row %d lacks highlight", i)
		}
	}
}

func TestReviewFirstChangeFocusRespectsSplitSide(t *testing.T) {
	m := reviewNavigationModel()
	m.focus = focusItemList
	m = reviewKey(m, "l")
	if m.reviewCursor != 4 || m.reviewSide != "new" {
		t.Fatal("new side did not focus first addition")
	}
	m = reviewKey(m, "h")
	m.reviewSide = "old"
	m = reviewKey(m, "l")
	if m.reviewCursor != 2 || m.reviewSide != "old" {
		t.Fatal("old side did not focus first removal")
	}
	m.reviewSplit = false
	m.focus = focusItemList
	m.reviewCursor = 0
	m = reviewKey(m, "l")
	if m.reviewCursor != 2 {
		t.Fatal("unified focus did not skip context")
	}
}

func TestReviewThreadMarkerAndNavigationRespectSourceSide(t *testing.T) {
	m := markerReviewModel(t)
	review, err := m.store.CreateReview(m.reviewReviews[0].ItemID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := &models.ReviewAnchor{File: "file.go", Side: "old", StartLine: 2, EndLine: 3, Excerpt: "removed one\nremoved two"}
	if _, err = m.store.CreateReviewThread(review.ID, anchor, models.ActorUser, "old-side question"); err != nil {
		t.Fatal(err)
	}
	m.refreshReviewList()
	m.updateConv()
	line := m.reviewDiff.Files[0].Lines[2]
	if !m.reviewLineHasThread(line, "old") || m.reviewLineHasThread(line, "new") {
		t.Fatal("old thread leaked into new-side code")
	}
	m.focus = focusItemList
	m.reviewSide = "old"
	m = reviewKey(m, "l")
	m = reviewKey(m, "l")
	if !m.reviewThreadsMode || m.reviewID != review.ID || m.reviewThreadCursor != 0 {
		t.Fatal("old-side marker did not select its review/thread")
	}
}

func TestReviewThreadRefreshPreservesReaderScroll(t *testing.T) {
	m := markerReviewModel(t)
	m.openReviewThreads()
	m.focus = focusReadingPane
	m.reviewThreadCursor = 2
	m.updateConv()
	m.revealReviewThread()
	m.conv.ScrollDown(3)
	offset := m.conv.YOffset
	m.refreshReviewList()
	m.updateConv()
	if m.conv.YOffset != offset {
		t.Fatalf("refresh moved reader from %d to %d", offset, m.conv.YOffset)
	}
}

func TestReturnFromChangedThreadFollowsAttachment(t *testing.T) {
	m := markerReviewModel(t)
	other := m.reviewDiff.Files[0]
	other.Path = "other.go"
	m.reviewDiff.Files = append(m.reviewDiff.Files, other)
	m = reviewKey(m, "l")
	m = reviewKey(m, "l")
	m.reviewReviews[0].Threads[2].Anchor = &models.ReviewAnchor{File: "other.go", Side: "old", StartLine: 2, EndLine: 2, Excerpt: "removed one"}
	m = reviewKey(m, "j")
	m = reviewKey(m, "h")
	file, _ := m.selectedReviewFile()
	if m.reviewThreadsMode || file.Path != "other.go" || m.reviewSide != "old" || file.Lines[m.reviewCursor].OldLine != 2 {
		t.Fatalf("h did not follow selected attachment: path=%s side=%s cursor=%d", file.Path, m.reviewSide, m.reviewCursor)
	}
}

func TestReviewFileThreadBadgeUsesSubmittedUserTurn(t *testing.T) {
	m := markerReviewModel(t)
	thread := &m.reviewReviews[0].Threads[1]
	now := time.Now().UTC()
	thread.Messages = []models.ReviewMessage{
		{Actor: models.ActorUser, Submitted: true, CreatedAt: now.Add(-time.Hour), SubmittedAt: now.Add(-time.Minute)},
		{Actor: models.ActorAgent, CreatedAt: now},
		{Actor: models.ActorUser, CreatedAt: now.Add(time.Minute)}, // saved draft
	}
	entries := m.reviewFileEntries()
	if entries[0].threadBadge != "[1+] " || !entries[0].threadNew {
		t.Fatalf("new reply missing: %+v", entries[0])
	}
	thread.Messages[2].Submitted = true
	thread.Messages[2].SubmittedAt = now.Add(2 * time.Minute)
	entries = m.reviewFileEntries()
	if entries[0].threadBadge != "[1] " || entries[0].threadNew {
		t.Fatalf("submitted follow-up did not clear indicator: %+v", entries[0])
	}
	thread.Anchor = nil
	if entries = m.reviewFileEntries(); entries[0].threadBadge != "" {
		t.Fatal("detached thread counted against file")
	}
}

func TestThreadReturnOpensFullContextAndRejectsStaleCode(t *testing.T) {
	m := markerReviewModel(t)
	m.reviewDiff.Files[0].FullLines = append(m.reviewDiff.Files[0].Lines, reviewdiff.Line{Text: "outside hunk", Kind: ' ', OldLine: 99, NewLine: 99})
	m = reviewKey(m, "l")
	m = reviewKey(m, "l")
	m.reviewReviews[0].Threads[1].Anchor = &models.ReviewAnchor{File: "file.go", Side: "new", StartLine: 99, EndLine: 99, Excerpt: "outside hunk"}
	m = reviewKey(m, "h")
	line := m.currentReviewLine()
	if !m.reviewFullContext || line == nil || line.NewLine != 99 {
		t.Fatal("thread outside hunk was not revealed")
	}
	m = reviewKey(m, "l")
	m.reviewReviews[0].Threads[1].Anchor.Excerpt = "obsolete"
	m = reviewKey(m, "h")
	if m.err == nil || m.reviewCursor == len(m.reviewDiff.Files[0].FullLines)-1 {
		t.Fatal("stale attachment silently selected unrelated code")
	}
}

func TestTabThreadSelectionRendersImmediatelyAndAfterMovement(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := markerReviewModel(t)
	m = reviewKey(m, "tab")
	if !m.reviewThreadsMode || m.focus != focusReadingPane {
		t.Fatal("Tab did not focus the thread reader")
	}
	for _, key := range []string{"", "j", "k"} {
		if key != "" {
			m = reviewKey(m, key)
		}
		rows := strings.Split(m.conv.View(), "\n")
		index := m.reviewThreadCursor
		visibleStart := max(m.reviewThreadOffsets[index], m.conv.YOffset)
		visibleEnd := min(m.reviewThreadEnds[index], m.conv.YOffset+m.conv.Height)
		if visibleStart >= visibleEnd {
			t.Fatalf("selected thread is offscreen after %q", key)
		}
		for row := visibleStart; row < visibleEnd; row++ {
			if !strings.Contains(rows[row-m.conv.YOffset], "48;5;237") {
				t.Fatalf("selected thread row %d lacks highlight after %q", row, key)
			}
		}
	}
	// The current thread stays marked even when focus changes.
	m.focus = focusItemList
	m.updateConv()
	if !strings.Contains(m.conv.View(), "48;5;237") {
		t.Fatal("thread highlight disappeared when focus changed")
	}
}
