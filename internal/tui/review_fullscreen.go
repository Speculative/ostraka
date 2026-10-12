package tui

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
)

func (m model) renderFullScreenReview() string {
	panel := m.conv.View()
	if m.reviewComposer.active {
		separator := composerBorderStyle.Render(strings.Repeat("─", max(1, m.width)))
		input := lipgloss.NewStyle().Padding(0, 0, 1, 1).Render(m.input.View())
		panel = lipgloss.JoinVertical(lipgloss.Left, panel, separator, input)
		if m.mentionPicker.open {
			panel = m.overlayMentionPickerAtCursor(panel)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, panel, m.renderFooter())
}
