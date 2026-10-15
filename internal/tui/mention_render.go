package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// renderItemMarkdown resolves mentions only for display. The editor and stored
// conversation keep canonical IDs, so renames and status changes appear on the
// next render without rewriting any item text.
func renderItemMarkdown(markdown string, width int, items map[string]models.Item) string {
	if width <= 0 {
		return markdown
	}
	prepared, decorations := resolveMentionMarkdown(markdown, items)
	return decorateMentions(renderMarkdown(prepared, width), decorations)
}

// Mentions use a dedicated pale-lavender-on-plum palette. Apply it after
// Markdown wrapping, so headings, tables, and inline styles cannot strip it.
const (
	mentionForeground = "189"
	mentionBackground = "53"
)

type mentionDecoration struct {
	text       string
	color      string
	start, end bool
}

func decorateMentions(rendered string, decorations map[rune]mentionDecoration) string {
	if len(decorations) == 0 {
		return rendered
	}
	var out strings.Builder
	style := ""
	active := false
	writeText := func(text string) {
		var run strings.Builder
		flush := func() {
			if run.Len() == 0 {
				return
			}
			if active {
				out.WriteString(termenv.String(run.String()).Foreground(termenv.ANSI256.Color(mentionForeground)).Background(termenv.ANSI256.Color(mentionBackground)).String())
				out.WriteString(style)
			} else {
				out.WriteString(run.String())
			}
			run.Reset()
		}
		for _, r := range text {
			if d, ok := decorations[r]; ok {
				flush()
				if d.start {
					active = true
				}
				color := mentionForeground
				if d.color != "" {
					color = d.color
				}
				out.WriteString(termenv.String(d.text).Foreground(termenv.ANSI256.Color(color)).Background(termenv.ANSI256.Color(mentionBackground)).String())
				out.WriteString(style)
				if d.end {
					active = false
				}
			} else if r == '\n' {
				// Reset the background before advancing to the next terminal row.
				flush()
				out.WriteRune(r)
			} else {
				run.WriteRune(r)
			}
		}
		flush()
	}
	last := 0
	for _, span := range sgrSequence.FindAllStringIndex(rendered, -1) {
		writeText(rendered[last:span[0]])
		sequence := rendered[span[0]:span[1]]
		out.WriteString(sequence)
		if sequence == "\x1b[0m" || sequence == "\x1b[m" {
			style = ""
		} else {
			style += sequence
		}
		last = span[1]
	}
	writeText(rendered[last:])
	return out.String()
}

// Parse before substituting so code, URLs, and Markdown structure stay literal.
// Titles are encoded as entities to keep their punctuation from becoming new
// Markdown syntax when the result is passed to the terminal renderer.
func resolveMentionMarkdown(markdown string, items map[string]models.Item) (string, map[rune]mentionDecoration) {
	if len(items) == 0 || !strings.Contains(markdown, "@") {
		return markdown, nil
	}
	source := []byte(markdown)
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList)).Parser().Parse(text.NewReader(source))
	var out strings.Builder
	last := 0
	decorations := make(map[rune]mentionDecoration)
	nextMarker := '\ue100'
	// Reserve markers against both source text and titles, which can themselves
	// contain private-use characters.
	var reserved strings.Builder
	reserved.WriteString(markdown)
	for _, item := range items {
		reserved.WriteString(item.Title)
	}
	occupied := reserved.String()
	marker := func(d mentionDecoration) rune {
		for strings.ContainsRune(occupied, nextMarker) {
			nextMarker++
		}
		r := nextMarker
		nextMarker++
		decorations[r] = d
		return r
	}
	start := marker(mentionDecoration{text: "@", start: true})
	endMarker := marker(mentionDecoration{text: "]", end: true})
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case ast.KindCodeSpan, ast.KindCodeBlock, ast.KindFencedCodeBlock,
			ast.KindLink, ast.KindAutoLink, ast.KindImage, ast.KindRawHTML, ast.KindHTMLBlock:
			return ast.WalkSkipChildren, nil
		}
		n, ok := node.(*ast.Text)
		if !ok {
			return ast.WalkContinue, nil
		}
		for i := n.Segment.Start; i < n.Segment.Stop; {
			if markdown[i] != '@' || escapedAt(markdown, i) {
				i++
				continue
			}
			if i > 0 {
				previous, _ := utf8.DecodeLastRuneInString(markdown[:i])
				if !mentionBoundaryRune(previous) {
					i++
					continue
				}
			}
			end := i + 1
			for end < n.Segment.Stop {
				r, size := utf8.DecodeRuneInString(markdown[end:n.Segment.Stop])
				if !mentionQueryRune(r) {
					break
				}
				end += size
			}
			item, found := items[markdown[i+1:end]]
			if found && strings.TrimSpace(item.Title) != "" {
				out.WriteString(markdown[last:i])
				fmt.Fprintf(&out, "&#%d;[", start)
				if icon, color, visible := itemStatusIndicator(item); visible {
					iconMarker := marker(mentionDecoration{text: icon, color: string(color)})
					fmt.Fprintf(&out, "&#%d; ", iconMarker)
				}
				for _, r := range strings.Join(strings.Fields(ansi.Strip(item.Title)), " ") {
					fmt.Fprintf(&out, "&#%d;", r)
				}
				fmt.Fprintf(&out, "&#%d;", endMarker)
				last = end
			}
			i = end
		}
		return ast.WalkContinue, nil
	})
	if last == 0 {
		return markdown, nil
	}
	out.WriteString(markdown[last:])
	return out.String(), decorations
}
