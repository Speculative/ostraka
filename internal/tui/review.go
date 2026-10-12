package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewdiff"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const reviewCaptureTimeout = 20 * time.Second

var (
	reviewAddedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	reviewDeletedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	reviewHunkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	reviewMetaStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

type reviewComposerState struct {
	active               bool
	selectingDestination bool
	itemID               string
	reviewID             string
	threadNumber         int
	messageID            string
	anchor               *models.ReviewAnchor
}

func loadReviewDiffCmd(directory string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), reviewCaptureTimeout)
		defer cancel()
		d, err := reviewdiff.Capture(ctx, directory)
		return reviewDiffLoadedMsg{diff: d, generation: generation, err: err}
	}
}

func (m model) openReviewChanges() (model, tea.Cmd) {
	m.projectPane = reviewProjectPane
	m.projectEntries = nil
	m.editingProject = false
	m.projectPreview = false
	m.items = nil
	m.selected = 0
	m.listOffset = 0
	m.focus = focusItemList
	m.reviewDiff = reviewdiff.Diff{}
	m.reviewSyntax = nil
	m.reviewCursor = 0
	m.reviewAnchor = -1
	m.reviewSelecting = false
	m.reviewSide = "new"
	m.reviewLoading = false
	m.reviewThreadsMode = false
	m.reviewReviews = nil
	m.reviewSelectedID = ""
	m.reviewThreadCursor = 0
	m.err = nil
	m = m.recalcLayout()
	m.refreshReviewList()
	return m.requestReviewDiff()
}

func (m model) requestReviewDiff() (model, tea.Cmd) {
	if m.store == nil {
		m.err = fmt.Errorf("project store is unavailable")
		m.reviewLoading = false
		return m, nil
	}
	m.reviewDiffGeneration++
	m.reviewLoading = true
	m.err = nil
	if len(m.reviewDiff.Files) == 0 && !m.reviewThreadsMode {
		m.projectEntries = m.reviewFileEntries()
		m.selected = 0
	}
	m.updateConv()
	directory := filepath.Dir(m.store.Root)
	return m, loadReviewDiffCmd(directory, m.reviewDiffGeneration)
}

func (m *model) acceptReviewDiff(msg reviewDiffLoadedMsg) {
	if m.projectPane != reviewProjectPane || msg.generation != m.reviewDiffGeneration {
		return
	}
	m.reviewLoading = false
	if msg.err != nil {
		m.err = fmt.Errorf("read working-tree diff: %w", msg.err)
		if len(m.reviewDiff.Files) == 0 && !m.reviewThreadsMode {
			m.projectEntries = []projectEntry{{title: "Diff unavailable", meta: "refresh failed"}}
			m.selected = 0
		}
		m.updateConv()
		return
	}
	// A capture may finish after the user has opened the thread reader. Keep
	// its list and cursor intact; the new diff is ready when they return.
	if m.reviewThreadsMode {
		m.reviewDiff = msg.diff
		m.reviewSyntax = nil
		m.reviewAnchor = -1
		m.reviewSelecting = false
		m.err = nil
		m.updateConv()
		return
	}
	previousPath := m.selectedReviewPath()
	previousCursor := m.reviewCursor
	m.reviewDiff = msg.diff
	m.reviewSyntax = nil
	m.projectEntries = m.reviewFileEntries()
	m.selected = indexReviewPath(msg.diff.Files, previousPath)
	if len(msg.diff.Files) == 0 {
		m.selected = 0
		m.reviewCursor = 0
	} else {
		file, _ := m.selectedReviewFile()
		m.reviewCursor = min(previousCursor, len(file.Lines)-1)
		if m.reviewCursor < 0 || !reviewLineHasCode(file, m.reviewCursor) {
			m.reviewCursor = firstReviewCodeLine(file)
		}
	}
	m.reviewAnchor = -1
	m.reviewSelecting = false
	m.listOffset = 0
	m.err = nil
	m.updateConv()
}

func reviewFileEntries(d reviewdiff.Diff, loading bool) []projectEntry {
	entries := make([]projectEntry, 0, len(d.Files))
	for _, file := range d.Files {
		meta := file.Status
		if file.Binary {
			meta += " · binary"
		}
		if file.Notice != "" {
			meta += " · " + strings.TrimSpace(file.Notice)
		}
		color := lipgloss.Color("226")
		if file.Status == "untracked" || file.Status == "added" {
			color = "42"
		}
		if file.Status == "deleted" {
			color = "203"
		}
		entries = append(entries, projectEntry{title: file.Path, meta: meta, statusColor: color})
	}
	if len(entries) == 0 {
		label, meta := "No working-tree changes", "working tree clean"
		if loading {
			label, meta = "Reading working-tree diff…", "refreshing"
		}
		entries = append(entries, projectEntry{title: label, meta: meta})
	}
	return entries
}

func indexReviewPath(files []reviewdiff.File, path string) int {
	if path != "" {
		for i, file := range files {
			if file.Path == path {
				return i
			}
		}
	}
	return 0
}

func (m model) selectedReviewPath() string {
	if m.selected >= 0 && m.selected < len(m.reviewDiff.Files) {
		return m.reviewDiff.Files[m.selected].Path
	}
	return ""
}

func (m model) selectedReviewFile() (reviewdiff.File, bool) {
	if m.projectPane != reviewProjectPane || m.selected < 0 || m.selected >= len(m.reviewDiff.Files) {
		return reviewdiff.File{}, false
	}
	file := m.reviewDiff.Files[m.selected]
	if m.reviewFullContext && file.FullLines != nil {
		file.Lines = file.FullLines
	}
	return file, true
}

func firstReviewCodeLine(file reviewdiff.File) int {
	for i, line := range file.Lines {
		if line.OldLine > 0 || line.NewLine > 0 {
			return i
		}
	}
	return 0
}

func reviewLineHasCode(file reviewdiff.File, index int) bool {
	return index >= 0 && index < len(file.Lines) &&
		(file.Lines[index].OldLine > 0 || file.Lines[index].NewLine > 0)
}

// ReviewViewState reports the live browser cursor for sibling review UI.
func (m model) ReviewViewState() ReviewViewState {
	state := ReviewViewState{
		Active:    m.projectPane == reviewProjectPane,
		Loading:   m.reviewLoading,
		Root:      m.reviewDiff.Root,
		Head:      m.reviewDiff.Head,
		FileIndex: m.selected,
		LineIndex: m.reviewCursor,
		Side:      m.reviewSide,
		Selecting: m.reviewSelecting,
	}
	if file, ok := m.selectedReviewFile(); ok {
		state.FilePath = file.Path
	}
	_, marked, err := m.ReviewSelection()
	state.HasRange = marked && err == nil
	return state
}

// ReviewSelection returns the currently marked source range. The bool is true
// when a range is marked; err explains when the display range cannot map to a
// contiguous source excerpt on the selected side.
func (m model) ReviewSelection() (selection reviewdiff.Selection, marked bool, err error) {
	file, ok := m.selectedReviewFile()
	if !ok || m.reviewAnchor < 0 {
		return reviewdiff.Selection{}, false, nil
	}
	selection, err = reviewdiff.Select(file, m.reviewAnchor, m.reviewCursor, m.reviewSide)
	return selection, true, err
}

func (m model) handleReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.reviewSearching {
		return m.handleReviewSearchKey(msg)
	}
	if m.reviewThreadsMode {
		return m.handleReviewThreadsKey(msg)
	}
	switch msg.String() {
	case "/":
		m.reviewSearching = true
		m.reviewSearchFiles = m.focus == focusItemList
		m.reviewSearchQuery = ""
		m.filterInput.SetValue("")
		return m, m.filterInput.Focus()
	case "n", "N":
		direction := 1
		if msg.String() == "N" {
			direction = -1
		}
		m.findReviewMatch(direction)
		return m, nil
	case "c":
		m.toggleReviewContext()
		return m, nil
	case "d":
		m.reviewSplit = !m.reviewSplit
		if m.reviewSplit && m.reviewAnchor < 0 {
			m.reviewSide = "new"
		}
		m.updateConv()
		return m, nil
	case "P":
		if controller, ok := m.sup.(dispatchPauseController); ok {
			controller.ToggleDispatchPause()
		}
		return m, nil
	case "ctrl+c":
		if _, busy := m.busyDispatch(); busy {
			_ = m.sup.Interrupt()
			return m, nil
		}
		return m, tea.Quit
	case "q":
		if _, busy := m.busyDispatch(); busy {
			m.mode = modeQuit
			return m, nil
		}
		return m, tea.Quit
	case "1":
		return m.switchView(channelView(models.ChannelInbox))
	case "2":
		return m.switchView(archiveView)
	case "3":
		m.projectPane = 1
		m.showProjectContext()
		return m, nil
	case "4", "r":
		return m.requestReviewDiff()
	case "tab":
		m.openReviewThreads()
		m.revealReviewThread()
		return m, nil
	case "t":
		if !m.openReviewThreadsAtCursor() {
			return m, nil
		}
		return m.replyToReviewThread()
	case "a":
		return m.beginReviewQuestion(false)
	case "@":
		return m.beginReviewQuestion(true)
	case "b":
		return m.submitReviewBatch()
	case "F":
		return m.finishCurrentReview()
	case "left", "shift+left", "h":
		m.focus = focusItemList
		m.updateConv()
		return m, nil
	case "right", "shift+right", "l":
		if m.focus == focusReadingPane {
			m.openReviewThreadsAtCursor()
			return m, nil
		}
		m.focus = focusReadingPane
		m.focusFirstReviewChange()
		m.followReviewCursorSide()
		m.updateConv()
		m.revealReviewCursor()
		return m, nil
	case "j", "down", "shift+down":
		if m.focus == focusReadingPane {
			m.moveReviewCursor(1)
		} else if m.selected < len(m.projectEntries)-1 {
			m.selected++
			m.reviewCursor = 0
			m.reviewAnchor = -1
			if file, ok := m.selectedReviewFile(); ok {
				m.reviewCursor = firstReviewCodeLine(file)
			}
			m.showSelectedProjectEntry()
		}
		return m, nil
	case "k", "up", "shift+up":
		if m.focus == focusReadingPane {
			m.moveReviewCursor(-1)
		} else if m.selected > 0 {
			m.selected--
			m.reviewCursor = 0
			m.reviewAnchor = -1
			if file, ok := m.selectedReviewFile(); ok {
				m.reviewCursor = firstReviewCodeLine(file)
			}
			m.showSelectedProjectEntry()
		}
		return m, nil
	case "]":
		m.moveReviewHunk(1)
		return m, nil
	case "[":
		m.moveReviewHunk(-1)
		return m, nil
	case "v":
		if file, ok := m.selectedReviewFile(); !ok || (m.reviewSplit && !m.reviewNavigableLine(file, m.reviewCursor)) {
			return m, nil
		}
		m.reviewAnchor = m.reviewCursor
		m.reviewSelecting = true
		if !m.reviewSplit {
			if line := m.currentReviewLine(); line != nil && line.Kind == '-' {
				m.reviewSide = "old"
			} else {
				m.reviewSide = "new"
			}
		}
		m.updateConv()
		m.revealReviewCursor()
		return m, nil
	case "enter":
		if m.reviewSelecting {
			m.reviewSelecting = false
			m.updateConv()
		}
		return m, nil
	case "s":
		if m.reviewSide == "old" {
			m.reviewSide = "new"
		} else {
			m.reviewSide = "old"
		}
		m.updateConv()
		return m, nil
	case "esc":
		m.reviewAnchor = -1
		m.reviewSelecting = false
		m.updateConv()
		return m, nil
	case "pgup":
		return m, m.pageConversation(-1)
	case "pgdown":
		return m, m.pageConversation(1)
	case "f":
		return m.enterCopyMode(), nil
	default:
		return m, nil
	}
}

func (m *model) currentReviewLine() *reviewdiff.Line {
	file, ok := m.selectedReviewFile()
	if !ok || m.reviewCursor < 0 || m.reviewCursor >= len(file.Lines) {
		return nil
	}
	return &file.Lines[m.reviewCursor]
}

func (m *model) moveReviewCursor(delta int) {
	file, ok := m.selectedReviewFile()
	if !ok || len(file.Lines) == 0 || delta == 0 {
		return
	}
	if m.reviewSplit {
		for i := m.reviewCursor + delta; i >= 0 && i < len(file.Lines); i += delta {
			if m.reviewNavigableLine(file, i) {
				m.reviewCursor = i
				break
			}
		}
	} else {
		m.reviewCursor = max(0, min(len(file.Lines)-1, m.reviewCursor+delta))
	}
	m.followReviewCursorSide()
	m.updateConv()
	m.revealReviewCursor()
}

func (m *model) moveReviewHunk(direction int) {
	file, ok := m.selectedReviewFile()
	if !ok || direction == 0 {
		return
	}
	if direction > 0 {
		for i := m.reviewCursor + 1; i < len(file.Lines); i++ {
			if file.Lines[i].Kind == '@' {
				for j := i + 1; j < len(file.Lines) && file.Lines[j].Kind != '@'; j++ {
					if m.reviewNavigableLine(file, j) {
						m.reviewCursor = j
						m.followReviewCursorSide()
						m.updateConv()
						m.revealReviewCursor()
						return
					}
				}
			}
		}
		return
	}
	currentHunk := -1
	for i := m.reviewCursor; i >= 0; i-- {
		if file.Lines[i].Kind == '@' {
			currentHunk = i
			break
		}
	}
	for i := currentHunk - 1; i >= 0; i-- {
		if file.Lines[i].Kind != '@' {
			continue
		}
		for j := i + 1; j < len(file.Lines) && file.Lines[j].Kind != '@'; j++ {
			if m.reviewNavigableLine(file, j) {
				m.reviewCursor = j
				m.updateConv()
				m.revealReviewCursor()
				return
			}
		}
	}
}

func (m *model) updateReviewConv() {
	if m.reviewThreadsMode {
		m.updateReviewThreadsConv()
		return
	}
	file, ok := m.selectedReviewFile()
	if !ok {
		m.reviewLineOffsets = nil
		if m.reviewLoading {
			m.conv.SetContent("Reading current working-tree changes…")
		} else if m.err != nil {
			m.conv.SetContent("Could not read the current working-tree diff.")
		} else if len(m.reviewDiff.Files) == 0 {
			m.conv.SetContent("No working-tree changes against HEAD.")
		} else {
			m.conv.SetContent("Select a file to view its diff.")
		}
		m.resetProjectConvSelection()
		return
	}

	if m.reviewSplit {
		m.reviewCursor = m.reviewIndexOnSide(file, m.reviewCursor)
		if m.reviewAnchor >= 0 {
			m.reviewAnchor = m.reviewIndexOnSide(file, m.reviewAnchor)
			if m.reviewAnchor < 0 {
				m.reviewSelecting = false
			}
		}
	}
	m.indexReviewCodeThreads(file)
	width := max(1, m.conv.Width)
	contextLabel, layoutLabel := "diff context", "unified"
	if m.reviewFullContext {
		contextLabel = "full file"
	}
	if m.reviewSplit {
		layoutLabel = "side-by-side (old │ new)"
	}
	labels := []string{file.Path + " · " + file.Status, contextLabel + " · " + layoutLabel + " · selection side: " + m.reviewSide}
	if m.reviewDiff.Head == "" {
		labels = append(labels, "comparison: unborn branch")
	} else {
		head := m.reviewDiff.Head
		if len(head) > 12 {
			head = head[:12]
		}
		labels = append(labels, "comparison: working tree against "+head)
	}
	if file.Binary {
		labels = append(labels, "Binary file; source lines are not selectable.")
	}
	if file.Notice != "" {
		labels = append(labels, strings.TrimSpace(file.Notice))
	}
	content := make([]string, 0, len(file.Lines)+len(labels)+1)
	for _, label := range labels {
		content = append(content, ansi.Wrap(reviewMetaStyle.Render(label), width, " "))
	}
	content = append(content, ansi.Wrap(reviewHunkStyle.Render(strings.Repeat("─", clampRule(width, 40))), width, " "))
	m.reviewLineOffsets = make([]int, len(file.Lines))
	lineOffset := strings.Count(strings.Join(content, "\n"), "\n") + 1
	if m.reviewSplit {
		m.renderReviewSplit(file, &content, lineOffset, width)
	} else {
		for i, line := range file.Lines {
			m.reviewLineOffsets[i] = lineOffset
			rendered := renderReviewDiffLine(line)
			if m.reviewLineHasThread(line, "") {
				rendered = rendered.Background(reviewThreadBg)
			}
			if m.reviewRangeContains(i) {
				rendered = rendered.Background(selectedBg)
			} else if i == m.reviewCursor && m.focus == focusReadingPane {
				rendered = rendered.Background(selectedBg).Bold(true)
			}
			text := rendered.Render(formatReviewDiffLine(line))
			if line.OldLine > 0 || line.NewLine > 0 {
				prefix := fmt.Sprintf("%4s %4s %c ", reviewLineNumber(line.OldLine), reviewLineNumber(line.NewLine), line.Kind)
				side := "new"
				if line.NewLine == 0 {
					side = "old"
				}
				text = rendered.Render(prefix) + m.reviewThreadBadge(line, "", rendered) + m.renderReviewSource(file, line, side, rendered)
			}
			wrapped := ansi.Wrap(text, width, " ")
			content = append(content, wrapped)
			lineOffset += strings.Count(wrapped, "\n") + 1
		}
	}
	m.conv.SetContent(strings.Join(content, "\n"))
	m.resetProjectConvSelection()
	if m.focus == focusReadingPane {
		m.revealReviewCursor()
	}
}

func (m *model) resetProjectConvSelection() {
	m.convTurns = 0
	m.convActivities = 0
	m.convLive = 0
	m.convPartials = 0
	m.convSelection = -1
	m.convSelectable = nil
	m.convSelectTop = 0
	m.convSelectBottom = 0
	m.convFailure = 0
	m.convItemID = "review-changes"
}

func renderReviewDiffLine(line reviewdiff.Line) lipgloss.Style {
	switch line.Kind {
	case '+':
		return reviewAddedStyle
	case '-':
		return reviewDeletedStyle
	case '@':
		return reviewHunkStyle
	case '\\':
		return reviewMetaStyle
	default:
		return lipgloss.NewStyle()
	}
}

func formatReviewDiffLine(line reviewdiff.Line) string {
	if line.Kind == '@' || line.Kind == '\\' {
		return "        " + line.Text
	}
	return fmt.Sprintf("%4s %4s %c %s", reviewLineNumber(line.OldLine), reviewLineNumber(line.NewLine), line.Kind, line.Text)
}

func reviewLineNumber(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func (m model) reviewRangeContains(index int) bool {
	if m.reviewAnchor < 0 {
		return false
	}
	if index == m.reviewCursor {
		return true
	}
	first, last := m.reviewAnchor, m.reviewCursor
	if first > last {
		first, last = last, first
	}
	if index < first || index > last {
		return false
	}
	file, ok := m.selectedReviewFile()
	if !ok || index >= len(file.Lines) {
		return false
	}
	line := file.Lines[index]
	if m.reviewSide == "old" {
		return line.OldLine > 0
	}
	return line.NewLine > 0
}

func (m *model) revealReviewCursor() {
	if m.reviewCursor < 0 || m.reviewCursor >= len(m.reviewLineOffsets) {
		return
	}
	line := m.reviewLineOffsets[m.reviewCursor]
	if line < m.conv.YOffset {
		m.conv.SetYOffset(line)
	} else if line >= m.conv.YOffset+m.conv.Height {
		m.conv.SetYOffset(line - m.conv.Height + 1)
	}
}
