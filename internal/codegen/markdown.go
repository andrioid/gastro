package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
)

// markdownDirectiveRegex matches {{ markdown "..." }} directives.
// The argument must be a double-quoted string literal with no embedded quotes.
var markdownDirectiveRegex = regexp.MustCompile(`\{\{\s*markdown\s+"([^"]+)"\s*\}\}`)

// MarkdownContext provides the path information needed to resolve
// {{ markdown "..." }} directive arguments to absolute file paths.
type MarkdownContext struct {
	// ProjectRoot is the absolute path of the project root
	// (the directory containing pages/, components/, static/).
	ProjectRoot string
	// SourceDir is the absolute path of the directory containing
	// the .gastro file being compiled. Used for "./" and "../" relative paths.
	SourceDir string
}

// ProcessMarkdownDirectives replaces {{ markdown "path" }} directives in the
// template body with the rendered HTML of the referenced markdown file.
//
// Path resolution rules:
//   - Paths starting with "./" or "../" are resolved relative to the .gastro
//     file's directory. They may reference files outside the project root
//     (e.g. a shared docs directory one level up).
//   - All other paths are resolved relative to the project root.
//   - Absolute paths are errors.
//
// The rendered HTML has {{ and }} escaped so Go's html/template engine will
// not re-parse any template-looking content inside code fences.
//
// Returns the transformed body and the list of absolute markdown file paths
// referenced (for dependency tracking by the dev watcher).
func ProcessMarkdownDirectives(body string, ctx MarkdownContext) (string, []string, error) {
	var deps []string
	var firstErr error

	result := markdownDirectiveRegex.ReplaceAllStringFunc(body, func(match string) string {
		if firstErr != nil {
			return match
		}
		sub := markdownDirectiveRegex.FindStringSubmatch(match)
		arg := sub[1]

		absPath, err := resolveMarkdownPath(arg, ctx)
		if err != nil {
			firstErr = err
			return match
		}

		html, err := renderMarkdownFile(absPath)
		if err != nil {
			firstErr = err
			return match
		}

		deps = append(deps, absPath)
		return escapeTemplateDelimiters(html)
	})

	if firstErr != nil {
		return "", nil, firstErr
	}

	// De-duplicate deps.
	if len(deps) > 1 {
		sort.Strings(deps)
		unique := deps[:0]
		var prev string
		for i, d := range deps {
			if i == 0 || d != prev {
				unique = append(unique, d)
			}
			prev = d
		}
		deps = unique
	}

	return result, deps, nil
}

// resolveMarkdownPath turns the literal argument of a {{ markdown }} directive
// into an absolute filesystem path, enforcing the path-resolution rules.
func resolveMarkdownPath(arg string, ctx MarkdownContext) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("markdown directive: empty path")
	}
	if filepath.IsAbs(arg) {
		return "", fmt.Errorf("markdown directive: absolute paths are not allowed: %q", arg)
	}

	var base string
	if strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") {
		if ctx.SourceDir == "" {
			return "", fmt.Errorf("markdown directive: relative path %q used without source directory context", arg)
		}
		base = ctx.SourceDir
	} else {
		if ctx.ProjectRoot == "" {
			return "", fmt.Errorf("markdown directive: root-relative path %q used without project root context", arg)
		}
		base = ctx.ProjectRoot
	}

	abs, err := filepath.Abs(filepath.Join(base, arg))
	if err != nil {
		return "", fmt.Errorf("markdown directive: resolving %q: %w", arg, err)
	}

	if !strings.HasSuffix(abs, ".md") {
		return "", fmt.Errorf("markdown directive: %q does not have .md extension", arg)
	}

	return abs, nil
}

// markdownRenderer is the process-wide goldmark instance.
// Configured once and reused across all directive expansions.
var markdownRenderer = buildMarkdownRenderer()

func buildMarkdownRenderer() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
				),
			),
		),
	)
}

// renderMarkdownFile reads a markdown file and returns rendered HTML.
func renderMarkdownFile(absPath string) (string, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("markdown directive: reading %s: %w", absPath, err)
	}
	var buf bytes.Buffer
	if err := markdownRenderer.Convert(src, &buf); err != nil {
		return "", fmt.Errorf("markdown directive: rendering %s: %w", absPath, err)
	}
	return buf.String(), nil
}

// escapeTemplateDelimiters escapes only template delimiters ({{, }}) in the
// input, leaving HTML tags and entities untouched. This is the right escape
// for pre-rendered HTML being inlined into a Go template: we don't want the
// template engine to re-parse any {{ that happens to appear inside a code
// fence, but we must preserve the surrounding HTML structure verbatim.
func escapeTemplateDelimiters(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			b.WriteString(`{{ "{{" }}`)
			i += 2
		} else if i+1 < len(s) && s[i] == '}' && s[i+1] == '}' {
			b.WriteString(`{{ "}}" }}`)
			i += 2
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}
