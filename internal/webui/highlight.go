package webui

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

type syntaxToken struct {
	Class string `json:"class"`
	Text  string `json:"text"`
}

func syntaxClass(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "syntax-comment"
	case t.InCategory(chroma.Keyword):
		return "syntax-keyword"
	case t.InSubCategory(chroma.LiteralString):
		return "syntax-string"
	case t.InSubCategory(chroma.LiteralNumber):
		return "syntax-number"
	case t.InCategory(chroma.Literal):
		return "syntax-literal"
	case t.InSubCategory(chroma.NameFunction), t == chroma.NameBuiltin, t == chroma.NameClass:
		return "syntax-name"
	case t.InCategory(chroma.Operator):
		return "syntax-operator"
	default:
		return "syntax-plain"
	}
}

// Highlighting must preserve the exact source, including incomplete code and
// trailing newlines. Some lexers normalize input; those fall back to plain text.
func highlightSource(source, language string) []syntaxToken {
	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return []syntaxToken{{Class: "syntax-plain", Text: source}}
	}
	tokens := []syntaxToken{}
	var reconstructed strings.Builder
	for token := iterator(); token != chroma.EOF; token = iterator() {
		tokens = append(tokens, syntaxToken{Class: syntaxClass(token.Type), Text: token.Value})
		reconstructed.WriteString(token.Value)
	}
	if reconstructed.String() != source {
		return []syntaxToken{{Class: "syntax-plain", Text: source}}
	}
	return tokens
}

func (s *Server) highlight(w http.ResponseWriter, r *http.Request) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, http.ErrNotSupported)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	var input struct {
		Source   string `json:"source"`
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		fail(w, 400, err)
		return
	}
	respond(w, highlightSource(input.Source, input.Language), nil)
}

type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderCodeBlock)
	reg.Register(ast.KindCodeBlock, renderCodeBlock)
}
func renderCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	language := ""
	if fenced, ok := node.(*ast.FencedCodeBlock); ok {
		language = string(fenced.Language(source))
	}
	var code bytes.Buffer
	for i := 0; i < node.Lines().Len(); i++ {
		segment := node.Lines().At(i)
		code.Write(segment.Value(source))
	}
	_, _ = w.WriteString("<pre><code>")
	for _, token := range highlightSource(code.String(), language) {
		_, _ = w.WriteString(`<span class="` + token.Class + `">` + html.EscapeString(token.Text) + `</span>`)
	}
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

var _ renderer.NodeRenderer = codeRenderer{}
