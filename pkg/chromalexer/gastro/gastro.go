// Package gastro registers a Chroma syntax-highlighting lexer for .gastro
// files with Chroma's global lexer registry.
//
// Usage:
//
//	import (
//	    "github.com/alecthomas/chroma/v2/lexers"
//	    _ "github.com/andrioid/gastro/pkg/chromalexer/gastro"
//	)
//
//	lex := lexers.Get("gastro")
//
// The lexer delegates non-Gastro content to the HTML lexer and frontmatter
// content to the Go lexer. Go template expressions ({{ ... }}) and component
// tags (<PascalCase ...>) are tokenised with dedicated rules. The approach
// intentionally mirrors the minimal tree-sitter grammar under
// tree-sitter-gastro/: the inside of {{ ... }} is not sub-tokenised.
package gastro

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Lexer is the Chroma lexer for .gastro files. It is registered with the
// global lexer registry at init time and can be retrieved via
// lexers.Get("gastro").
var Lexer chroma.Lexer

func init() {
	Lexer = lexers.Register(chroma.DelegatingLexer(
		lexers.Get("html"),
		chroma.MustNewLexer(
			&chroma.Config{
				Name:      "Gastro",
				Aliases:   []string{"gastro"},
				Filenames: []string{"*.gastro"},
				MimeTypes: []string{"text/x-gastro"},
				DotAll:    true,
			},
			gastroRules,
		),
	))
}

func gastroRules() chroma.Rules {
	return chroma.Rules{
		"root": {
			// Frontmatter must appear at the very start of the document.
			// Note: we do NOT consume the trailing newline, so the closing
			// delimiter's leading \n can still match in the "frontmatter" state.
			{`\A---`, chroma.CommentPreproc, chroma.Push("frontmatter")},

			// Go template expression: {{ ... }}. Kept opaque on purpose to
			// match the tree-sitter grammar's @embedded region.
			{`\{\{.*?\}\}`, chroma.CommentPreproc, nil},

			// Opening component tag: <PascalCase ...> or <PascalCase ... />
			{`(<)([A-Z][A-Za-z0-9]*)`, chroma.ByGroups(chroma.Punctuation, chroma.NameClass), chroma.Push("component")},

			// Closing component tag: </PascalCase>
			{`(</)([A-Z][A-Za-z0-9]*)(>)`, chroma.ByGroups(chroma.Punctuation, chroma.NameClass, chroma.Punctuation), nil},

			// Everything else is delegated to the HTML lexer by the outer
			// DelegatingLexer.
			{`[^<{]+`, chroma.Other, nil},
			{`[<{]`, chroma.Other, nil},
		},
		"frontmatter": {
			// Closing delimiter ends frontmatter. Tried first so it wins over
			// the catch-all rules below.
			{`\n---\n`, chroma.CommentPreproc, chroma.Pop(1)},
			// Everything inside is Go. Split into two rules so both newlines
			// and non-newline runs are consumed.
			{`\n`, chroma.Using("go"), nil},
			{`[^\n]+`, chroma.Using("go"), nil},
		},
		"component": {
			// Self-closing or regular close of the opening tag.
			{`\s*/>`, chroma.Punctuation, chroma.Pop(1)},
			{`\s*>`, chroma.Punctuation, chroma.Pop(1)},

			{`\s+`, chroma.Text, nil},

			// prop={go-expr}
			{
				`([A-Za-z][A-Za-z0-9]*)(=)(\{)([^}]*)(\})`,
				chroma.ByGroups(
					chroma.NameAttribute,
					chroma.Operator,
					chroma.Punctuation,
					chroma.Using("go"),
					chroma.Punctuation,
				),
				nil,
			},

			// prop="literal"
			{
				`([A-Za-z][A-Za-z0-9]*)(=)("[^"]*")`,
				chroma.ByGroups(
					chroma.NameAttribute,
					chroma.Operator,
					chroma.LiteralString,
				),
				nil,
			},

			// Bare attribute (no value).
			{`[A-Za-z][A-Za-z0-9]*`, chroma.NameAttribute, nil},
		},
	}
}
