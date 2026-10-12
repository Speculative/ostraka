// Package reviewcode formats selected source without changing its contents.
package reviewcode

import (
	"strings"

	"github.com/alecthomas/chroma/v2/lexers"
)

// Block tags known source languages and uses a fence longer than any backtick
// run in the excerpt, so selected Markdown cannot close its own code block.
func Block(path, excerpt string) string {
	language := "text"
	if lexer := lexers.Match(path); lexer != nil && len(lexer.Config().Aliases) > 0 {
		language = lexer.Config().Aliases[0]
	}
	fence := "```"
	for strings.Contains(excerpt, fence) {
		fence += "`"
	}
	return fence + language + "\n" + strings.TrimSuffix(excerpt, "\n") + "\n" + fence + "\n"
}
