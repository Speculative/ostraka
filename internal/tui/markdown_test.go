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

func TestRenderMarkdownKeepsShortHyphenatedTokensTogether(t *testing.T) {
	inputs := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "inline code",
			input: "The project-brief portion of this item is already implemented. The wording now in `internal/prompt/prompt.go` came from completed child `20260819-065319` and was committed in `2f48206`. It states the intended two-part threshold (long-lived and broadly relevant to most items), identifies project description/goals/norms as appropriate, excludes recent-change and medium-duration state, and directs narrower knowledge to linked producing items.",
			want:  "20260819-065319",
		},
		{
			name:  "plain prose",
			input: "The project-brief portion of this item is already implemented. The wording now in internal/prompt/prompt.go came from completed child 20260819-065319 and was committed in 2f48206. It states the intended two-part threshold (long-lived and broadly relevant to most items), identifies project description/goals/norms as appropriate, excludes recent-change and medium-duration state, and directs narrower knowledge to linked producing items.",
			want:  "20260819-065319",
		},
		{
			name:  "hyphenated word",
			input: "This paragraph contains long-lived guidance and enough ordinary prose to exercise the available wrapping width without breaking the word.",
			want:  "long-lived",
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			plain := ansi.Strip(renderMarkdown(tc.input, 80))
			if !strings.Contains(plain, tc.want) {
				t.Fatalf("rendered text split %q: %q", tc.want, plain)
			}
			lines := strings.Split(plain, "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == "065319" || trimmed == "long-" {
					t.Fatalf("orphaned fragment on line %d: %q in %q", i, line, plain)
				}
				if strings.HasSuffix(trimmed, "-") && i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "065319") {
					t.Fatalf("hyphenated token split across lines %d-%d: %q / %q", i, i+1, line, lines[i+1])
				}
			}
		})
	}
}

func TestRenderMarkdownUsesPaneWidthAfterRemovingDocumentMargin(t *testing.T) {
	const width = 80
	plain := ansi.Strip(renderMarkdown("A paragraph with enough ordinary words to fill the available conversation pane instead of reserving an unexplained document margin on both sides.", width))
	fullWidth := false
	for _, line := range strings.Split(plain, "\n") {
		if actual := lipgloss.Width(line); actual > width {
			t.Fatalf("rendered line width %d exceeds %d: %q", actual, width, line)
		} else if actual == width {
			fullWidth = true
		}
	}
	if !fullWidth {
		t.Fatalf("rendered prose did not use the pane width: %q", plain)
	}
}
