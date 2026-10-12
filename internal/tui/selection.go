package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// selectedBackground keeps the rendered foreground and text attributes intact.
// Reapply only the background after every SGR sequence: nested Markdown and
// syntax styles can reset it or set a code-block background of their own.
func selectedBackground(content string, width int) string {
	color := lipgloss.ColorProfile().Color(string(selectedBg))
	background := ""
	if color != nil && color.Sequence(true) != "" {
		background = "\x1b[" + color.Sequence(true) + "m"
	}
	rows := strings.Split(content, "\n")
	for i, row := range rows {
		// Match the viewport's tab expansion before measuring padding. Tabs
		// otherwise measure as zero cells and overflow when Lip Gloss renders.
		row = strings.ReplaceAll(row, "\t", strings.Repeat(" ", lipgloss.NewStyle().GetTabWidth()))
		padding := strings.Repeat(" ", max(0, width-ansi.StringWidth(row)))
		if background == "" {
			rows[i] = row + padding
			continue
		}
		row = sgrSequence.ReplaceAllStringFunc(row, func(sequence string) string {
			return sequence + background
		})
		rows[i] = background + row + padding + "\x1b[0m"
	}
	return strings.Join(rows, "\n")
}
