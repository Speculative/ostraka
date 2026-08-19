package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdownStylesMarkdownAndTaggedCode(t *testing.T) {
	got := renderMarkdown("# Heading\n\n**bold** and *emphasis*\n\n```go\nfunc main() {\n\tprintln(\"hello\")\n}\n```", 80)
	plain := ansi.Strip(got)

	if !strings.Contains(plain, "Heading") || !strings.Contains(plain, "bold") || !strings.Contains(plain, "func main()") {
		t.Fatalf("rendered Markdown lost content: %q", plain)
	}
	if strings.Contains(plain, "```go") || strings.Contains(plain, "```") {
		t.Fatalf("fence markers should not be rendered: %q", plain)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("rendered Markdown has no ANSI styling: %q", got)
	}
}

func TestRenderMarkdownUsesBoxDrawingForTables(t *testing.T) {
	got := ansi.Strip(renderMarkdown("| Name | Value |\n| --- | ---: |\n| café | 42 |", 40))

	if !strings.Contains(got, "café") {
		t.Fatalf("table lost Unicode content: %q", got)
	}
	for _, want := range []string{"─", "│", "┼"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table output missing box-drawing character %q: %q", want, got)
		}
	}
}

func TestRenderMarkdownFallsBackToWrappedTextAtZeroWidth(t *testing.T) {
	in := "**literal**"
	if got := renderMarkdown(in, 0); got != in {
		t.Errorf("zero-width render = %q, want unmodified input %q", got, in)
	}
}

func TestConversationUsesMarkdownRendererForBodiesAndTurns(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.items = []models.Item{{
		ID:      "item-1",
		Channel: models.ChannelInbox,
		Status:  models.StatusActive,
		Created: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC),
		Title:   "markdown item",
		Body:    "| Name | Value |\n| --- | --- |\n| café | **42** |",
		Turns: []models.Turn{{
			Actor:     models.ActorAgent,
			Timestamp: time.Date(2026, 8, 19, 0, 1, 0, 0, time.UTC),
			Content:   "```go\nfmt.Println(\"hello\")\n```",
		}},
	}}
	m.selected = 0
	m.conv.Width = 80
	m.conv.Height = 40
	m.updateConv()

	view := m.conv.View()
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "┼") || !strings.Contains(plain, "fmt.Println") {
		t.Fatalf("conversation did not render Markdown content: %q", plain)
	}
	if strings.Contains(plain, "```go") || !strings.Contains(view, "\x1b[") {
		t.Fatalf("conversation Markdown output is not ANSI-rendered: %q", view)
	}
}

func TestRenderMarkdownKeepsRenderedLinesWithinWidth(t *testing.T) {
	for _, width := range []int{8, 16, 40, 80} {
		got := renderMarkdown("Unicode café 日本語\n\n```go\nfunc main() { println(42) }\n```", width)
		for _, line := range strings.Split(got, "\n") {
			if actual := lipgloss.Width(line); actual > width {
				t.Errorf("width %d: rendered line width %d exceeds it: %q", width, actual, line)
			}
		}
	}
}
