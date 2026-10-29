package webui

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeHighlightingPreservesAndEscapesSource(t *testing.T) {
	for _, source := range []string{"package main\nfunc main() { println(\"<script>\") }", "// comment\nconst n = 42\n", "\tfunc(", "\r\nhello\r\n"} {
		tokens := highlightSource(source, "go")
		var joined strings.Builder
		for _, token := range tokens {
			joined.WriteString(token.Text)
		}
		if joined.String() != source {
			t.Fatalf("source changed: %q", joined.String())
		}
	}
	html := markdown("```go\npackage main\nfunc main() { println(\"<script>\") }\n```\n")
	if !strings.Contains(html, `class="syntax-keyword"`) || !strings.Contains(html, "&lt;script&gt;") || strings.Contains(html, "<script>") {
		t.Fatal(html)
	}
	// The composer sends incomplete code without a final newline.
	tokens := highlightSource("const n = 42", "go")
	found := false
	for _, token := range tokens {
		if token.Class == "syntax-keyword" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tokens for incomplete composer block: %+v", tokens)
	}
}

func TestHighlightAPIHasNoHTMLOrExecution(t *testing.T) {
	s := testServer(t)
	body := `{"source":"const x = \"<img onerror=alert(1)>\"","language":"javascript"}`
	r := httptest.NewRequest("POST", "http://localhost/api/highlight", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var tokens []syntaxToken
	if err := json.Unmarshal(w.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) < 2 {
		t.Fatalf("missing syntax: %+v", tokens)
	}
}
