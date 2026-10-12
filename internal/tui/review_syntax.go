package tui

import (
	"strings"

	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

type reviewSyntax struct {
	old  map[int][]chroma.Token
	next map[int][]chroma.Token
}

// Tokenise each version separately so deleted text cannot affect the lexical
// state of the working-tree version. Cache tokens until the next diff capture.
func (m *model) reviewFileSyntax(file reviewdiff.File) reviewSyntax {
	if syntax, ok := m.reviewSyntax[file.Path]; ok {
		return syntax
	}
	syntax := reviewSyntax{}
	lexer := lexers.Match(file.Path)
	if lexer != nil {
		lines := file.FullLines
		if lines == nil {
			lines = file.Lines
		}
		syntax.old = reviewSourceTokens(lexer, lines, true)
		syntax.next = reviewSourceTokens(lexer, lines, false)
	}
	if m.reviewSyntax == nil {
		m.reviewSyntax = make(map[string]reviewSyntax)
	}
	m.reviewSyntax[file.Path] = syntax
	return syntax
}

func reviewSourceTokens(lexer chroma.Lexer, lines []reviewdiff.Line, old bool) map[int][]chroma.Token {
	var source []string
	var numbers []int
	for _, line := range lines {
		n := line.NewLine
		if old {
			n = line.OldLine
		}
		if n > 0 {
			source = append(source, line.Text)
			numbers = append(numbers, n)
		}
	}
	iterator, err := lexer.Tokenise(nil, strings.Join(source, "\n"))
	if err != nil {
		return nil
	}
	result := make(map[int][]chroma.Token)
	row := 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		for i, part := range strings.Split(token.Value, "\n") {
			if i > 0 {
				row++
			}
			if row < len(numbers) && part != "" {
				result[numbers[row]] = append(result[numbers[row]], chroma.Token{Type: token.Type, Value: part})
			}
		}
	}
	return result
}

func (m *model) renderReviewSource(file reviewdiff.File, line reviewdiff.Line, side string, base lipgloss.Style) string {
	syntax := m.reviewFileSyntax(file)
	tokens := syntax.next[line.NewLine]
	if side == "old" {
		tokens = syntax.old[line.OldLine]
	}
	var original strings.Builder
	for _, token := range tokens {
		original.WriteString(token.Value)
	}
	// Unknown languages, lexer transformations, and incomplete context retain
	// the exact source text. Highlighting never changes a question's excerpt.
	if len(tokens) == 0 || original.String() != line.Text {
		return base.Render(line.Text)
	}
	palette := styles.Get("dracula")
	var output strings.Builder
	for _, token := range tokens {
		style := base
		entry := palette.Get(token.Type)
		if entry.Colour.IsSet() {
			style = style.Foreground(lipgloss.Color(entry.Colour.String()))
		}
		if entry.Italic == chroma.Yes {
			style = style.Italic(true)
		}
		output.WriteString(style.Render(token.Value))
	}
	return output.String()
}
