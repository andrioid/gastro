package gastro_test

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	_ "github.com/andrioid/gastro/pkg/chromalexer/gastro"
)

// tokenize runs the registered "gastro" lexer over src and returns the
// flattened token stream. It fails the test on any lexer error.
func tokenize(t *testing.T, src string) []chroma.Token {
	t.Helper()

	lex := lexers.Get("gastro")
	if lex == nil {
		t.Fatal(`lexers.Get("gastro") returned nil — init() did not register the lexer`)
	}

	it, err := lex.Tokenise(nil, src)
	if err != nil {
		t.Fatalf("tokenise: %v", err)
	}
	return it.Tokens()
}

// hasToken reports whether the stream contains a token whose type matches
// tt and whose value, trimmed of surrounding whitespace, equals value.
func hasToken(tokens []chroma.Token, tt chroma.TokenType, value string) bool {
	for _, tok := range tokens {
		if tok.Type == tt && strings.TrimSpace(tok.Value) == value {
			return true
		}
	}
	return false
}

func TestLexerIsRegistered(t *testing.T) {
	t.Parallel()

	lex := lexers.Get("gastro")
	if lex == nil {
		t.Fatal(`lexers.Get("gastro") returned nil`)
	}
	if got := lex.Config().Name; got != "Gastro" {
		t.Errorf("lexer name = %q, want %q", got, "Gastro")
	}

	// Also resolvable by extension.
	if byExt := lexers.Match("page.gastro"); byExt == nil {
		t.Error(`lexers.Match("page.gastro") returned nil`)
	}
}

func TestFrontmatterDelimitersAreHighlighted(t *testing.T) {
	t.Parallel()

	src := "---\nprops: struct{}\n---\n<div>hi</div>\n"
	tokens := tokenize(t, src)

	if !hasToken(tokens, chroma.CommentPreproc, "---") {
		t.Errorf("expected CommentPreproc token for frontmatter delimiter; got tokens:\n%v", tokens)
	}
}

func TestTemplateExpressionsAreEmbedded(t *testing.T) {
	t.Parallel()

	src := "---\n---\n<p>{{ .Name }}</p>\n"
	tokens := tokenize(t, src)

	if !hasToken(tokens, chroma.CommentPreproc, "{{ .Name }}") {
		t.Errorf("expected {{ .Name }} to be tokenised as CommentPreproc; got tokens:\n%v", tokens)
	}
}

func TestComponentTagsAndProps(t *testing.T) {
	t.Parallel()

	src := "---\n---\n<Button label=\"Go\" count={.N} />\n"
	tokens := tokenize(t, src)

	if !hasToken(tokens, chroma.NameClass, "Button") {
		t.Error("expected component name 'Button' as NameClass")
	}
	if !hasToken(tokens, chroma.NameAttribute, "label") {
		t.Error("expected prop name 'label' as NameAttribute")
	}
	if !hasToken(tokens, chroma.NameAttribute, "count") {
		t.Error("expected prop name 'count' as NameAttribute")
	}
	if !hasToken(tokens, chroma.LiteralString, `"Go"`) {
		t.Error("expected string prop value as LiteralString")
	}
}

func TestClosingComponentTag(t *testing.T) {
	t.Parallel()

	src := "---\n---\n<Card>body</Card>\n"
	tokens := tokenize(t, src)

	// The name appears twice: once for the open tag, once for the close tag.
	count := 0
	for _, tok := range tokens {
		if tok.Type == chroma.NameClass && tok.Value == "Card" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 'Card' NameClass token twice (open+close), got %d", count)
	}
}

func TestPlainHTMLIsDelegated(t *testing.T) {
	t.Parallel()

	// Without frontmatter (Gastro allows this), HTML tags should flow through
	// the delegated HTML lexer rather than being tokenised as component tags.
	src := "<div class=\"x\">hello</div>\n"
	tokens := tokenize(t, src)

	// The HTML lexer emits NameTag for "div" — evidence delegation fired.
	if !hasToken(tokens, chroma.NameTag, "div") {
		t.Errorf("expected HTML delegation to produce NameTag 'div'; got tokens:\n%v", tokens)
	}
}
