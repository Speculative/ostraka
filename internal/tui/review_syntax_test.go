package tui

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestReviewSyntaxUsesIndependentFullFileVersions(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	full := []reviewdiff.Line{
		{Text: "package main", Kind: ' ', OldLine: 1, NewLine: 1},
		{Text: "/*", Kind: '-', OldLine: 2},
		{Text: "var s = `", Kind: '+', NewLine: 2},
		{Text: "multiline text", Kind: ' ', OldLine: 3, NewLine: 3},
		{Text: "*/", Kind: '-', OldLine: 4},
		{Text: "`", Kind: '+', NewLine: 4},
		{Text: "const answer = 42", Kind: ' ', OldLine: 5, NewLine: 5},
	}
	file := reviewdiff.File{Path: "source.go", FullLines: full, Lines: full[3:4]}
	m := newModel(nil, nil, nil)
	syntax := m.reviewFileSyntax(file)
	if len(syntax.old[3]) == 0 || syntax.old[3][0].Type != chroma.CommentMultiline {
		t.Fatalf("old multiline state: %#v", syntax.old[3])
	}
	if len(syntax.next[3]) == 0 || !syntax.next[3][0].Type.InSubCategory(chroma.String) {
		t.Fatalf("new multiline state: %#v", syntax.next[3])
	}
	old := m.renderReviewSource(file, full[3], "old", lipgloss.NewStyle())
	next := m.renderReviewSource(file, full[3], "new", lipgloss.NewStyle())
	if ansi.Strip(old) != full[3].Text || ansi.Strip(next) != full[3].Text || old == next {
		t.Fatalf("side highlighting lost text or lexical difference: %q / %q", old, next)
	}
	selected := m.renderReviewSource(file, full[6], "new", lipgloss.NewStyle().Background(selectedBg))
	if !strings.Contains(selected, "48;") || ansi.Strip(selected) != full[6].Text {
		t.Fatal("syntax highlighting lost selected background or source text")
	}
}

func TestReviewSyntaxPlainFallbackPreservesSource(t *testing.T) {
	m := newModel(nil, nil, nil)
	line := reviewdiff.Line{Text: "unknown syntax\t☃", Kind: '+', NewLine: 1}
	file := reviewdiff.File{Path: "source.unknown-language", Lines: []reviewdiff.Line{line}}
	rendered := m.renderReviewSource(file, line, "new", reviewAddedStyle)
	if rendered != reviewAddedStyle.Render(line.Text) {
		t.Fatalf("unknown language changed plain rendering: %q", rendered)
	}
	selection, err := reviewdiff.Select(file, 0, 0, "new")
	if err != nil || selection.Excerpt != line.Text {
		t.Fatal("plain fallback changed the source excerpt")
	}
}
