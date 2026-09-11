package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// protectedHyphen is an internal, one-cell sentinel. Glamour's two paragraph
// wrapping passes treat ASCII hyphens as breakpoints even when the whole
// hyphenated token fits on a line. That can leave a short suffix such as the
// last six digits of an item ID on its own row. Protecting content hyphens
// during rendering keeps those tokens intact; the sentinel is restored before
// the rendered text is returned.
const protectedHyphen = '\ue000'

// renderMarkdown turns conversation content into terminal-native output. The
// renderer owns wrapping because ANSI escape sequences and wide Unicode cells
// must not be counted as ordinary bytes by the viewport.
func renderMarkdown(markdown string, width int) string {
	if markdown == "" || width <= 0 {
		return markdown
	}

	style := glamourstyles.DarkStyleConfig
	// The conversation pane already supplies its own horizontal boundary. The
	// default document margin consumes two cells on each side and makes the
	// renderer wrap prose to a narrower width than the pane actually has.
	style.Document.Margin = nil
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

	rendered, err := renderer.Render(protectMarkdownHyphens(markdown, width))
	if err != nil {
		return wrapText(markdown, width)
	}
	// Glamour's document style intentionally adds margin newlines around the
	// whole document. Conversation sections already provide their own spacing,
	// so retain only the content's internal blank lines here. Code blocks retain
	// their own indentation, and code lines are otherwise treated as
	// preformatted. Run one ANSI-aware final pass so those decorations cannot
	// make a viewport row wider than the pane, including for wide Unicode
	// graphemes.
	rendered = ansi.Wrap(rendered, width, " ,.;-+|")
	return strings.Trim(strings.ReplaceAll(rendered, string(protectedHyphen), "-"), "\n")
}

// protectMarkdownHyphens makes short content tokens indivisible while
// Glamour renders them. Markdown syntax that uses hyphens structurally—list
// markers, thematic breaks, and table separators—is left untouched. Fenced
// code and link destinations are also left alone because changing their
// source before parsing could change Markdown semantics or syntax highlighting.
func protectMarkdownHyphens(markdown string, width int) string {
	if width <= 0 || !strings.Contains(markdown, "-") {
		return markdown
	}

	var out strings.Builder
	out.Grow(len(markdown))
	inFence := byte(0)
	for _, line := range strings.SplitAfter(markdown, "\n") {
		body := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		trimmed := strings.TrimLeft(body, " \t")
		if marker := fenceMarker(trimmed); marker != 0 {
			out.WriteString(line)
			if inFence == 0 {
				inFence = marker
			} else if inFence == marker {
				inFence = 0
			}
			continue
		}
		if inFence != 0 || hyphenSyntaxLine(body) {
			out.WriteString(line)
			continue
		}
		out.WriteString(protectMarkdownLine(line, width))
	}
	return out.String()
}

func fenceMarker(line string) byte {
	line = strings.TrimLeft(line, " \t")
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0
	}
	if line[1] != line[0] || line[2] != line[0] {
		return 0
	}
	return line[0]
}

func hyphenSyntaxLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.ContainsRune(trimmed, '-') {
		return false
	}
	for _, r := range trimmed {
		if r != '|' && r != ':' && r != '-' && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func protectMarkdownLine(line string, width int) string {
	var out strings.Builder
	out.Grow(len(line))
	codeTicks := 0
	linkCloser := byte(0)
	linkDepth := 0
	for i := 0; i < len(line); {
		c := line[i]

		if linkCloser != 0 {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(line) {
				out.WriteByte(line[i+1])
				i += 2
				continue
			}
			if linkCloser == '(' {
				switch c {
				case '(':
					linkDepth++
				case ')':
					linkDepth--
					if linkDepth == 0 {
						linkCloser = 0
					}
				}
			} else if c == ']' {
				linkCloser = 0
			}
			i++
			continue
		}

		if c == '`' {
			run := 1
			for i+run < len(line) && line[i+run] == '`' {
				run++
			}
			out.WriteString(line[i : i+run])
			if codeTicks == 0 {
				codeTicks = run
			} else if codeTicks == run {
				codeTicks = 0
			}
			i += run
			continue
		}

		if codeTicks == 0 && c == ']' && i+1 < len(line) && (line[i+1] == '(' || line[i+1] == '[') {
			out.WriteByte(c)
			out.WriteByte(line[i+1])
			linkCloser = line[i+1]
			linkDepth = 1
			i += 2
			continue
		}

		if codeTicks == 0 && c == '<' {
			if end := strings.IndexByte(line[i+1:], '>'); end >= 0 {
				end += i + 1
				out.WriteString(line[i : end+1])
				i = end + 1
				continue
			}
		}

		if c == '-' && !escapedAt(line, i) && !listMarkerAt(line, i) && tokenFits(line, i, width) {
			out.WriteRune(protectedHyphen)
		} else {
			out.WriteByte(c)
		}
		i++
	}
	return out.String()
}

func escapedAt(s string, i int) bool {
	backslashes := 0
	for i > 0 && s[i-1] == '\\' {
		backslashes++
		i--
	}
	return backslashes%2 == 1
}

func listMarkerAt(line string, i int) bool {
	if line[i] != '-' || i > 3 {
		return false
	}
	for _, r := range line[:i] {
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return i+1 == len(line) || unicode.IsSpace(rune(line[i+1]))
}

func tokenFits(s string, i, width int) bool {
	start := i
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:start])
		if unicode.IsSpace(r) {
			break
		}
		start -= size
	}
	end := i
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if unicode.IsSpace(r) {
			break
		}
		end += size
	}
	return lipgloss.Width(s[start:end]) <= width
}
