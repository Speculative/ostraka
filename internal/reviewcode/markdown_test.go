package reviewcode

import (
	"strings"
	"testing"
)

func TestSourceLanguageAndEmbeddedFences(t *testing.T) {
	for _, tc := range []struct{ path, code, prefix string }{
		{"main.go", "return nil", "```go\n"},
		{"config.unknown", "value", "```text\n"},
		{"notes.md", "```go\nreturn nil\n```", "````md\n"},
	} {
		got := Block(tc.path, tc.code)
		if !strings.HasPrefix(got, tc.prefix) || !strings.Contains(got, tc.code) {
			t.Fatalf("invalid code block: %q", got)
		}
	}
}
