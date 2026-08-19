package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// renderMarkdown turns conversation content into terminal-native output. The
// renderer owns wrapping because ANSI escape sequences and wide Unicode cells
// must not be counted as ordinary bytes by the viewport.
func renderMarkdown(markdown string, width int) string {
	if markdown == "" || width <= 0 {
		return markdown
	}

	style := glamourstyles.DarkStyleConfig
	// Keep ordinary prose contiguous. The default document colour wraps every
	// text node separately, which makes a plain phrase such as "first brief"
	// impossible to search for in the rendered conversation. Markdown-specific
	// elements and fenced code retain their own styles below.
	style.Document.Color = nil
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithColorProfile(termenv.ANSI256),
		glamour.WithWordWrap(width),
		glamour.WithTableWrap(true),
		glamour.WithInlineTableLinks(true),
		glamour.WithPreservedNewLines(),
		glamour.WithEmoji(),
	)
	if err != nil {
		return wrapText(markdown, width)
	}

	rendered, err := renderer.Render(markdown)
	if err != nil {
		return wrapText(markdown, width)
	}
	// Glamour's document style intentionally adds a margin newline around the
	// whole document. Conversation sections already provide their own spacing,
	// so retain only the content's internal blank lines here.
	// Glamour deliberately gives code blocks and document margins their own
	// indentation, and code lines are otherwise treated as preformatted. Run
	// one ANSI-aware final pass so those decorations cannot make a viewport row
	// wider than the pane, including for wide Unicode graphemes.
	rendered = ansi.Wrap(rendered, width, " ,.;-+|")
	return strings.Trim(rendered, "\n")
}
