package tui

import (
	"strings"
	"testing"
)

func TestWrapTextPreservesShortLines(t *testing.T) {
	in := "short line\n\nanother one"
	if got := wrapText(in, 40); got != in {
		t.Errorf("got %q want %q", got, in)
	}
}

func TestWrapTextZeroWidthPassesThrough(t *testing.T) {
	in := "a line far longer than nothing"
	if got := wrapText(in, 0); got != in {
		t.Errorf("got %q want %q", got, in)
	}
}

func TestWrapTextBreaksAtWordBoundary(t *testing.T) {
	got := wrapText("aaa bbb ccc ddd", 7)
	want := "aaa bbb\nccc ddd"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWrapTextPreservesBlankLines(t *testing.T) {
	got := wrapText("aaa bbb ccc\n\nddd", 7)
	want := "aaa bbb\nccc\n\nddd"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWrapTextPreservesIndentOnContinuation(t *testing.T) {
	// Indented code blocks in turn bodies must keep their shape, including on
	// the lines the wrap creates.
	got := wrapText("    aaa bbb ccc", 11)
	want := "    aaa bbb\n    ccc"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWrapTextDropsIndentPastHalfWidth(t *testing.T) {
	// 8 spaces of indent in a 10-column pane leaves 2 usable columns, so the
	// indent is abandoned rather than shredding the words.
	got := wrapText("        aaa bbb", 10)
	if strings.HasPrefix(got, " ") {
		t.Errorf("indent should have been dropped, got %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > 10 {
			t.Errorf("line %q exceeds width 10", line)
		}
	}
}

func TestWrapTextHardSplitsUnbreakableToken(t *testing.T) {
	got := wrapText("https://example.com/a/very/long/path", 10)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the token to be split, got %q", got)
	}
	for _, line := range lines {
		if len([]rune(line)) > 10 {
			t.Errorf("line %q exceeds width 10", line)
		}
	}
	if joined := strings.Join(lines, ""); joined != "https://example.com/a/very/long/path" {
		t.Errorf("split lost characters: %q", joined)
	}
}

func TestWrapTextNeverExceedsWidth(t *testing.T) {
	body := "A turn body with a mix of prose, an indented block\n\n    code line that is quite long indeed\n\nand a trailing paragraph."
	for _, w := range []int{1, 2, 5, 13, 40, 200} {
		for _, line := range strings.Split(wrapText(body, w), "\n") {
			if len([]rune(line)) > w {
				t.Errorf("width %d: line %q exceeds it", w, line)
			}
		}
	}
}
