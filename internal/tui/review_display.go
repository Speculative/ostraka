package tui

import (
	"fmt"
	"strings"

	"github.com/Speculative/ostraka/internal/reviewdiff"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m *model) followReviewCursorSide() {
	if m.reviewSplit || m.reviewAnchor >= 0 {
		return
	}
	if line := m.currentReviewLine(); line != nil {
		if line.Kind == '-' {
			m.reviewSide = "old"
		}
		if line.Kind == '+' {
			m.reviewSide = "new"
		}
	}
}

// Split navigation follows source lines on the explicitly chosen side.
func (m model) reviewNavigableLine(file reviewdiff.File, index int) bool {
	if index < 0 || index >= len(file.Lines) {
		return false
	}
	if !m.reviewSplit {
		return reviewLineHasCode(file, index)
	}
	if m.reviewSide == "old" {
		return file.Lines[index].OldLine > 0
	}
	return file.Lines[index].NewLine > 0
}

// Switching sides prefers the counterpart in the same replacement row.
// An empty cell falls forward, then backward; an empty side has no cursor.
func (m model) reviewIndexOnSide(file reviewdiff.File, index int) int {
	if m.reviewNavigableLine(file, index) {
		return index
	}
	if index >= 0 && index < len(file.Lines) && (file.Lines[index].Kind == '-' || file.Lines[index].Kind == '+') {
		start := index
		for start > 0 && (file.Lines[start-1].Kind == '-' || file.Lines[start-1].Kind == '+') {
			start--
		}
		middle := start
		for middle < len(file.Lines) && file.Lines[middle].Kind == '-' {
			middle++
		}
		end := middle
		for end < len(file.Lines) && file.Lines[end].Kind == '+' {
			end++
		}
		paired := middle + index - start
		if index >= middle {
			paired = start + index - middle
		}
		if paired >= start && paired < end && m.reviewNavigableLine(file, paired) {
			return paired
		}
	}
	for i := max(0, index); i < len(file.Lines); i++ {
		if m.reviewNavigableLine(file, i) {
			return i
		}
	}
	for i := min(index, len(file.Lines)-1); i >= 0; i-- {
		if m.reviewNavigableLine(file, i) {
			return i
		}
	}
	return -1
}

// Context changes preserve the source location, rather than a display index.
func (m *model) toggleReviewContext() {
	before, ok := m.selectedReviewFile()
	if !ok {
		return
	}
	m.reviewFullContext = !m.reviewFullContext
	after, _ := m.selectedReviewFile()
	mapIndex := func(index int) int {
		if index < 0 || index >= len(before.Lines) {
			return -1
		}
		line := before.Lines[index]
		for i, l := range after.Lines {
			if line.NewLine > 0 && l.NewLine == line.NewLine || line.NewLine == 0 && line.OldLine > 0 && l.OldLine == line.OldLine {
				return i
			}
		}
		return -1
	}
	m.reviewCursor = mapIndex(m.reviewCursor)
	m.reviewAnchor = mapIndex(m.reviewAnchor)
	if m.reviewCursor < 0 {
		m.reviewCursor = firstReviewCodeLine(after)
	}
	if m.reviewAnchor < 0 {
		m.reviewSelecting = false
	}
	m.updateConv()
	m.revealReviewCursor()
}

func (m model) handleReviewSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.reviewSearching = false
		m.filterInput.Blur()
		return m, nil
	case "enter":
		m.reviewSearchQuery = m.filterInput.Value()
		m.reviewSearching = false
		m.filterInput.Blur()
		m.findReviewMatch(1)
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	return m, cmd
}

func (m *model) findReviewMatch(direction int) {
	query := strings.ToLower(m.reviewSearchQuery)
	if query == "" {
		return
	}
	if m.reviewSearchFiles {
		count := len(m.reviewDiff.Files)
		for step := 1; step <= count; step++ {
			i := (m.selected + direction*step + count) % count
			if strings.Contains(strings.ToLower(m.reviewDiff.Files[i].Path), query) {
				m.selected = i
				file, _ := m.selectedReviewFile()
				m.reviewCursor = firstReviewCodeLine(file)
				m.reviewAnchor = -1
				m.updateConv()
				return
			}
		}
	} else if file, ok := m.selectedReviewFile(); ok {
		count := len(file.Lines)
		for step := 1; step <= count; step++ {
			i := (m.reviewCursor + direction*step + count) % count
			if (!m.reviewSplit || m.reviewNavigableLine(file, i)) && strings.Contains(strings.ToLower(file.Lines[i].Text), query) {
				m.reviewCursor = i
				m.followReviewCursorSide()
				m.updateConv()
				m.revealReviewCursor()
				return
			}
		}
	}
	m.err = fmt.Errorf("no match for %q", m.reviewSearchQuery)
}

// Pair each replacement block; context lines appear on both sides. Offsets
// map both cells back to the original diff lines used by source selections.
func (m *model) renderReviewSplit(file reviewdiff.File, content *[]string, offset, width int) {
	leftWidth := max(1, (width-3)/2)
	rightWidth := max(1, width-3-leftWidth)
	cell := func(index, cellWidth int, side string) string {
		if index < 0 {
			return strings.Repeat(" ", cellWidth)
		}
		line := file.Lines[index]
		number := line.OldLine
		if side == "new" {
			number = line.NewLine
		}
		style := renderReviewDiffLine(line)
		if m.reviewLineHasThread(line, side) {
			style = style.Background(reviewThreadBg)
		}
		if m.reviewRangeContains(index) && m.reviewSide == side || index == m.reviewCursor && m.focus == focusReadingPane && m.reviewSide == side {
			style = style.Background(selectedBg).Bold(true)
		}
		text := style.Render(fmt.Sprintf("%4s ", reviewLineNumber(number))) + m.reviewThreadBadge(line, side, style) + m.renderReviewSource(file, line, side, style)
		return style.Width(cellWidth).Render(ansi.Wrap(text, cellWidth, " "))
	}
	row := func(left, right int) {
		if left >= 0 {
			m.reviewLineOffsets[left] = offset
		}
		if right >= 0 {
			m.reviewLineOffsets[right] = offset
		}
		rendered := lipgloss.JoinHorizontal(lipgloss.Top, cell(left, leftWidth, "old"), " │ ", cell(right, rightWidth, "new"))
		*content = append(*content, rendered)
		offset += strings.Count(rendered, "\n") + 1
	}
	for i := 0; i < len(file.Lines); {
		line := file.Lines[i]
		switch line.Kind {
		case '@', '\\':
			m.reviewLineOffsets[i] = offset
			rendered := ansi.Wrap(renderReviewDiffLine(line).Render(line.Text), width, " ")
			*content = append(*content, rendered)
			offset += strings.Count(rendered, "\n") + 1
			i++
		case '-', '+':
			var removed, added []int
			for i < len(file.Lines) && file.Lines[i].Kind == '-' {
				removed = append(removed, i)
				i++
			}
			for i < len(file.Lines) && file.Lines[i].Kind == '+' {
				added = append(added, i)
				i++
			}
			for j := 0; j < max(len(removed), len(added)); j++ {
				left, right := -1, -1
				if j < len(removed) {
					left = removed[j]
				}
				if j < len(added) {
					right = added[j]
				}
				row(left, right)
			}
		default:
			row(i, i)
			i++
		}
	}
}
