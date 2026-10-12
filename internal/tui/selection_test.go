package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestSelectedBackgroundPreservesFormattingAndOverridesNestedBackgrounds(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	input := "\x1b[1;38;5;81mTitle\x1b[0m\n\x1b[3mitalic\x1b[m \x1b[38;2;1;2;3;48;5;236mcode\x1b[49m end"
	got := selectedBackground(input, 24)
	background := "\x1b[48;5;237m"
	for _, sequence := range []string{"\x1b[1;38;5;81m", "\x1b[3m", "\x1b[m", "\x1b[38;2;1;2;3;48;5;236m", "\x1b[49m"} {
		if !strings.Contains(got, sequence+background) {
			t.Errorf("style %q was lost or failed to restore selection background: %q", sequence, got)
		}
	}
	for _, row := range strings.Split(got, "\n") {
		if ansi.StringWidth(row) != 24 || !strings.HasPrefix(row, background) || !strings.HasSuffix(row, "\x1b[0m") {
			t.Errorf("selection row has incorrect padding or leaks style: %q", row)
		}
	}
}

func TestSelectedMarkdownRetainsSyntaxStyles(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	input := renderMarkdown("**bold** and *italic*\n\n```go\nfunc main() { println(42) }\n```", 80)
	selected := renderConversationParts([]conversationPart{{content: input}}, true, 80)
	// Every renderer style, including token colours, must survive selection.
	for _, sequence := range sgrSequence.FindAllString(input, -1) {
		if !strings.Contains(selected, sequence+"\x1b[48;5;237m") {
			t.Fatalf("selection lost renderer style %q", sequence)
		}
	}
}

func TestSelectedBackgroundWithoutColorDoesNotAddANSI(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	if got := selectedBackground("text", 6); got != "text  " {
		t.Fatalf("colorless selection = %q", got)
	}
}

func TestSelectedIndentedCodeDoesNotGainRowsInViewport(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	const width = 48
	input := renderMarkdown("```go\nfunc main() {\n\tprintln(42)\n\t\tprintln(43)\n}\n```", width)
	selected := selectedBackground(input, width)
	// This is the same width/padding pass applied by the viewport's View.
	view := lipgloss.NewStyle().Width(width).Render(selected)
	if strings.Count(view, "\n") != strings.Count(input, "\n") {
		t.Fatalf("viewport inserted rows after selecting indented code: before=%d after=%d", strings.Count(input, "\n"), strings.Count(view, "\n"))
	}
	for _, row := range strings.Split(view, "\n") {
		if ansi.StringWidth(row) != width {
			t.Errorf("selected viewport row width = %d, want %d: %q", ansi.StringWidth(row), width, row)
		}
		if !strings.Contains(row, "48;5;237") {
			t.Errorf("selected viewport row has no background: %q", row)
		}
	}
}

func TestSelectedRawTabsAreMeasuredBeforePadding(t *testing.T) {
	input := "\tcode\n\t\tmore"
	selected := selectedBackground(input, 24)
	view := lipgloss.NewStyle().Width(24).Render(selected)
	if strings.Count(view, "\n") != 1 || strings.Contains(selected, "\t") {
		t.Fatalf("tab expansion added rows: %q", view)
	}
	for _, row := range strings.Split(view, "\n") {
		if ansi.StringWidth(row) != 24 {
			t.Errorf("tabbed row width = %d, want 24", ansi.StringWidth(row))
		}
	}
}
