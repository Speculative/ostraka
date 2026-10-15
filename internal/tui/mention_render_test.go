package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderItemMarkdownMentions(t *testing.T) {
	items := map[string]models.Item{
		"abcd-1234": {ID: "abcd-1234", Title: "Ship café 日本語", Status: models.StatusPendingUser},
		"done-1234": {ID: "done-1234", Title: "Finished work", Status: models.StatusArchived},
	}
	got := renderItemMarkdown("See @abcd-1234, then @done-1234. Unknown @gone-1234.", 100, items)
	plain := ansi.Strip(got)
	for _, want := range []string{"See @[● Ship café 日本語], then @[Finished work].", "Unknown @gone-1234."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered mentions missing %q: %q", want, plain)
		}
	}
	if !strings.Contains(got, "\x1b[93;48;5;53m●") {
		t.Fatalf("mention should use the pending-user marker color: %q", got)
	}
}

func TestRenderItemMarkdownInMarkdownStructures(t *testing.T) {
	items := map[string]models.Item{
		"abcd-1234": {ID: "abcd-1234", Title: "Target | title", Status: models.StatusPendingAgent},
	}
	for _, input := range []string{
		"**@abcd-1234**", "~~@abcd-1234~~", "> @abcd-1234",
		"- @abcd-1234", "# @abcd-1234",
		"| Item |\n| --- |\n| @abcd-1234 |",
	} {
		t.Run(input, func(t *testing.T) {
			plain := ansi.Strip(renderItemMarkdown(input, 80, items))
			if !strings.Contains(plain, "@[○ Target | title]") || strings.Contains(plain, "@abcd-1234") {
				t.Fatalf("mention not resolved in Markdown structure: %q", plain)
			}
		})
	}
}

func TestMentionDecorationPreservesSurroundingMarkdownStyle(t *testing.T) {
	items := map[string]models.Item{
		"abcd-1234": {ID: "abcd-1234", Title: "Target", Status: models.StatusPendingAgent},
	}
	for _, pattern := range []string{"**%s AFTER**", "> %s AFTER", "# %s AFTER"} {
		input := strings.ReplaceAll(pattern, "%s", "@abcd-1234")
		literal := strings.ReplaceAll(pattern, "%s", "@[○ Target]")
		got := parseTerminal(t, []byte(renderItemMarkdown(input, 80, items)))
		want := parseTerminal(t, []byte(renderMarkdown(literal, 80)))
		for y := 0; y < integrationTermHeight; y++ {
			afterMention := false
			for x := 0; x < integrationTermWidth; x++ {
				cell := want.Cell(x, y)
				if cell.Char == ']' {
					afterMention = true
					continue
				}
				if afterMention {
					if actual := got.Cell(x, y); actual != cell {
						t.Fatalf("%q changed surrounding style at (%d,%d): got %#v, want %#v", input, x, y, actual, cell)
					}
				}
			}
		}
	}
}

func TestMentionHighlightBoundariesAndWrapping(t *testing.T) {
	items := map[string]models.Item{
		"done-1234": {ID: "done-1234", Title: "Wide 日本語 title wraps across rows", Status: models.StatusArchived},
		"live-1234": {ID: "live-1234", Title: "Active", Status: models.StatusPendingUser},
	}
	for _, width := range []int{12, 80} {
		rendered := renderItemMarkdown("Before @done-1234, after @live-1234!", width, items)
		term := parseTerminal(t, []byte(strings.ReplaceAll(rendered, "\n", "\r\n")))
		inMention := false
		mentions := 0
		for y := 0; y < integrationTermHeight; y++ {
			for x := 0; x < width; x++ {
				cell := term.Cell(x, y)
				if cell.Char == '@' {
					inMention = true
					mentions++
				}
				if cell.Char == 0 || cell.Char == ' ' {
					continue
				}
				if inMention {
					if cell.BG != 53 {
						t.Fatalf("width %d: mention cell %q has background %d", width, cell.Char, cell.BG)
					}
					wantForeground := uint32(189)
					if cell.Char == '●' {
						wantForeground = 11
					}
					if uint32(cell.FG) != wantForeground {
						t.Fatalf("width %d: mention cell %q has foreground %d", width, cell.Char, cell.FG)
					}
				} else if cell.BG == 53 {
					t.Fatalf("width %d: mention background leaked onto %q", width, cell.Char)
				}
				if cell.Char == ']' {
					inMention = false
				}
			}
		}
		if mentions != 2 || inMention {
			t.Fatalf("width %d: incomplete mention boundaries (%d, %v)", width, mentions, inMention)
		}
	}
}

func TestRenderItemMarkdownPreservesLiterals(t *testing.T) {
	items := map[string]models.Item{
		"abcd-1234": {ID: "abcd-1234", Title: "Resolved title", Status: models.StatusArchived},
	}
	for _, input := range []string{
		"`@abcd-1234`",
		"``@abcd-1234``",
		"```text\n@abcd-1234\n```",
		"~~~text\n@abcd-1234\n~~~",
		"    @abcd-1234\n",
		"[label](https://example.test/@abcd-1234)",
		"[label][ref]\n\n[ref]: https://example.test/@abcd-1234",
		"<https://example.test/@abcd-1234>",
		"https://example.test/@abcd-1234",
		"name@abcd-1234",
		"日本語@abcd-1234",
		`\@abcd-1234`,
		"@abcd-1234-extra",
	} {
		t.Run(input, func(t *testing.T) {
			if got, _ := resolveMentionMarkdown(input, items); got != input {
				t.Fatalf("literal changed: %q => %q", input, got)
			}
		})
	}
}

func TestRenderItemMarkdownKeepsTitlesLiteralAndWraps(t *testing.T) {
	title := "**bold** [link](url) `code` <tag> & :smile: @other-1234"
	items := map[string]models.Item{
		"abcd-1234":  {ID: "abcd-1234", Title: title, Status: models.StatusArchived},
		"other-1234": {ID: "other-1234", Title: "Do not expand recursively", Status: models.StatusArchived},
	}
	plain := ansi.Strip(renderItemMarkdown("@abcd-1234", 100, items))
	if strings.TrimSpace(plain) != "@["+title+"]" {
		t.Fatalf("title punctuation was interpreted: %q", plain)
	}
	for _, width := range []int{8, 20, 40} {
		for _, line := range strings.Split(renderItemMarkdown("Before @abcd-1234 after.", width, items), "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("rendered title width %d exceeds %d: %q", got, width, line)
			}
		}
	}
}

func TestConversationResolvesMentionsAndRefreshesTargets(t *testing.T) {
	m := newModel(nil, nil, nil)
	source := models.Item{
		ID: "source-1234", Title: "Source", Body: "Body @abcd-1234.",
		Turns: []models.Turn{{Actor: models.ActorUser, Timestamp: time.Now(), Content: "Turn @abcd-1234."}},
	}
	m.items = []models.Item{source}
	m.allItems = []models.Item{source, {ID: "abcd-1234", Title: "Target", Status: models.StatusPendingUser}}
	m.conv.Width = 80
	m.conv.Height = 40
	m.updateConv()
	plain := ansi.Strip(m.conv.View())
	if !strings.Contains(plain, "Body @[● Target].") || !strings.Contains(plain, "Turn @[● Target].") {
		t.Fatalf("conversation mentions not resolved: %q", plain)
	}
	m.allItems[1].Title = "Renamed target"
	m.allItems[1].Status = models.StatusPendingAgent
	m.updateConv()
	plain = ansi.Strip(m.conv.View())
	if !strings.Contains(plain, "Body @[○ Renamed target].") || !strings.Contains(plain, "Turn @[○ Renamed target].") {
		t.Fatalf("target changes not reflected: %q", plain)
	}
	if m.items[0].Body != source.Body || m.items[0].Turns[0].Content != source.Turns[0].Content {
		t.Fatal("rendering mutated canonical mention text")
	}
}
