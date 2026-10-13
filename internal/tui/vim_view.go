package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Bubble Tea v1 hides the terminal's hardware cursor. Bubbles draws a reverse
// video block and does not expose cursor shapes. Render a text-preserving
// underline for insert mode instead of writing terminal cursor controls that
// would interfere with the renderer's own screen positioning.
func (m model) turnTextareaView() string {
	if !m.vimTurnEditor() {
		return m.input.View()
	}
	if m.vim.normal {
		view := m.input.View()
		if m.vim.visual {
			return m.vimSelectionView(view)
		}
		return view
	}
	ta := m.input
	visible := !ta.Cursor.Blink
	ta.Cursor.SetMode(cursor.CursorHide)
	view := ta.View()
	if !visible {
		return view
	}
	row, column := textareaVisualPosition(ta, textareaCursorOffset(ta))
	row -= textareaViewport(&ta).YOffset
	column += textareaGutterWidth(ta)
	lines := strings.Split(view, "\n")
	if row < 0 || row >= len(lines) || column >= lipgloss.Width(lines[row]) {
		return view
	}
	// Cut by display cells so wide glyphs and their combining marks stay intact.
	cell := ansi.Strip(ansi.Cut(lines[row], column, column+1))
	width := 1
	if cell == "" || cell == " " {
		r := []rune(ta.Value())
		p := textareaCursorOffset(ta)
		if p < len(r) && ansi.StringWidth(string(r[p])) > 1 {
			width = ansi.StringWidth(string(r[p]))
			cell = ansi.Strip(ansi.Cut(lines[row], column, column+width))
		}
	}
	if cell == "" {
		cell = " "
	}
	mark := ta.Cursor.Style.Reverse(false).Underline(true).Render(cell)
	lines[row] = ansi.Cut(lines[row], 0, column) + ansiReset + mark + ansiReset + ansi.Cut(lines[row], column+width, lipgloss.Width(lines[row]))
	return strings.Join(lines, "\n")
}

func (m model) vimSelectionView(view string) string {
	r := []rune(m.input.Value())
	a, b := m.vimSelection(r, textareaCursorOffset(m.input))
	firstRow, firstCol := textareaVisualPosition(m.input, a)
	last := max(a, b-1)
	lastRow, lastCol := textareaVisualPosition(m.input, last)
	lastWidth := 1
	if last < len(r) {
		lastWidth = max(1, ansi.StringWidth(string(r[last])))
	}
	lastCol += lastWidth
	gutter := textareaGutterWidth(m.input)
	offset := textareaViewport(&m.input).YOffset
	lines := strings.Split(view, "\n")
	for i := max(0, firstRow-offset); i < len(lines) && i <= lastRow-offset; i++ {
		left, right := gutter, lipgloss.Width(lines[i])
		if i+offset == firstRow {
			left += firstCol
		}
		if i+offset == lastRow && !m.vim.visualLine {
			right = min(right, gutter+lastCol)
		}
		if right <= left {
			continue
		}
		span := selectedBackground(ansi.Cut(lines[i], left, right), right-left)
		lines[i] = ansi.Cut(lines[i], 0, left) + ansiReset + span + ansiReset + ansi.Cut(lines[i], right, lipgloss.Width(lines[i]))
	}
	return strings.Join(lines, "\n")
}
