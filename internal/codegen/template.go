package codegen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/andrioid/gastro/internal/parser"
)

// wrapRegex matches {{ wrap ComponentName ... }} where ComponentName is PascalCase.
var wrapRegex = regexp.MustCompile(`\{\{\s*wrap\s+([A-Z][a-zA-Z0-9]*)(\s*)`)

// oldPropSyntaxRegex detects old Gastro-specific {.Expr} prop syntax in HTML tags.
// This pattern cannot appear in valid HTML, so it's a reliable migration signal.
var oldPropSyntaxRegex = regexp.MustCompile(`<[A-Z][a-zA-Z0-9]*[^>]*\w+=\{[^}]+\}`)

// commentRegex matches Go template comments {{/* ... */}}.
var commentRegex = regexp.MustCompile(`\{\{/\*[\s\S]*?\*/\}\}`)

const commentPlaceholder = "\x00__GASTRO_COMMENT_"

// extractComments removes Go template comments from the body, replacing them
// with null-byte-delimited placeholders. This prevents the wrap regex
// from matching inside comments.
func extractComments(body string) (string, []string) {
	var comments []string
	result := commentRegex.ReplaceAllStringFunc(body, func(match string) string {
		comments = append(comments, match)
		return fmt.Sprintf("%s%d\x00", commentPlaceholder, len(comments)-1)
	})
	return result, comments
}

func restoreComments(body string, comments []string) string {
	for i, c := range comments {
		placeholder := fmt.Sprintf("%s%d\x00", commentPlaceholder, i)
		body = strings.Replace(body, placeholder, c, 1)
	}
	return body
}

// TransformTemplate transforms the template body:
//   - {{ raw }}...{{ endraw }} → escaped content (literal template syntax)
//   - {{ wrap ComponentName (dict ...) }}...{{ end }} → function call + {{define}} block
//
// Leaf components use bare function calls ({{ Card (dict ...) }}) which are already
// valid Go template syntax and pass through unchanged.
//
// Component names must be imported via UseDeclaration. Unknown components in wrap
// blocks produce errors.
func TransformTemplate(body string, uses []parser.UseDeclaration) (string, error) {
	known := make(map[string]bool, len(uses))
	for _, u := range uses {
		known[u.Name] = true
	}

	// Detect old HTML-like syntax and provide migration hints
	if err := detectOldSyntax(body, known); err != nil {
		return "", err
	}

	// Extract comments to prevent regexes from matching inside them
	body, comments := extractComments(body)

	// Escape raw blocks — after comments so {{ raw }} in comments is ignored,
	// before wrap transformation so {{ wrap }} inside raw blocks is escaped.
	var err error
	body, err = escapeRawBlocks(body)
	if err != nil {
		return "", err
	}

	// Transform {{ wrap X ... }}...{{ end }} blocks (components with children)
	result := body
	childIdx := 0
	for {
		newResult, changed, wrapErr := transformOneWrap(result, known, &childIdx)
		if wrapErr != nil {
			return "", wrapErr
		}
		if !changed {
			break
		}
		result = newResult
	}

	// Restore comments
	result = restoreComments(result, comments)

	return result, nil
}

// transformOneWrap finds the first {{ wrap X ... }} block, extracts its children,
// and replaces it with a function call + {{define}} block. Returns false if no
// wrap block was found.
func transformOneWrap(body string, known map[string]bool, childIdx *int) (string, bool, error) {
	loc := wrapRegex.FindStringIndex(body)
	if loc == nil {
		return body, false, nil
	}

	match := wrapRegex.FindStringSubmatch(body[loc[0]:loc[1]])
	name := match[1]

	if !known[name] {
		return "", false, fmt.Errorf("unknown component %q in {{ wrap }}: not imported", name)
	}

	// Find the end of the {{ wrap ... }} action (the closing }})
	wrapClose := findActionClose(body, loc[0])
	if wrapClose == -1 {
		return "", false, fmt.Errorf("unclosed {{ wrap %s }}: missing }}", name)
	}

	// Extract the arguments between the component name and }}
	// body[loc[1]:wrapClose] contains everything after "wrap ComponentName " up to "}}"
	argsStr := strings.TrimSpace(body[loc[1]:wrapClose])

	// Find the matching {{ end }} using a state-aware scanner
	endStart, endClose, err := findMatchingEnd(body, wrapClose+2) // +2 to skip past }}
	if err != nil {
		return "", false, fmt.Errorf("{{ wrap %s }}: %w", name, err)
	}

	// Extract child content between {{ wrap ... }} and {{ end }}
	childContent := body[wrapClose+2 : endStart]

	childTemplateName := fmt.Sprintf("%s_children_%d", strings.ToLower(name), *childIdx)
	*childIdx++

	// Build the dict call. The user passes (dict ...) as argsStr.
	// We need to inject "__children" into the dict arguments.
	// Strip outer parens from the dict expression to get the inner args.
	dictInner := argsStr
	if strings.HasPrefix(dictInner, "(") && strings.HasSuffix(dictInner, ")") {
		dictInner = dictInner[1 : len(dictInner)-1]
	}
	if dictInner == "" {
		dictInner = "dict"
	}

	replacement := fmt.Sprintf(
		`{{ %s (%s "__children" (__gastro_render_children "%s" .)) }}`,
		name, dictInner, childTemplateName,
	)

	defineBlock := fmt.Sprintf(
		"\n{{define %q}}%s{{end}}",
		childTemplateName, childContent,
	)

	result := body[:loc[0]] + replacement + body[endClose:] + defineBlock
	return result, true, nil
}

// findActionClose finds the position of the }} that closes the {{ action starting at pos.
// It skips over quoted strings inside the action. Returns the index of the first } of }},
// or -1 if not found.
func findActionClose(body string, pos int) int {
	i := pos
	// Skip past {{
	for i < len(body)-1 {
		if body[i] == '{' && body[i+1] == '{' {
			i += 2
			break
		}
		i++
	}

	for i < len(body)-1 {
		switch body[i] {
		case '"':
			// Skip double-quoted string
			i++
			for i < len(body) && body[i] != '"' {
				if body[i] == '\\' {
					i++ // skip escaped char
				}
				i++
			}
		case '`':
			// Skip raw string
			i++
			for i < len(body) && body[i] != '`' {
				i++
			}
		case '}':
			if i+1 < len(body) && body[i+1] == '}' {
				return i
			}
		}
		i++
	}
	return -1
}

// findMatchingEnd scans from startPos to find the {{ end }} that matches
// the current nesting depth. It correctly handles nested {{ if }}, {{ range }},
// {{ with }}, {{ block }}, {{ define }}, and {{ wrap }} blocks, and skips
// over comments and string literals.
//
// Returns (endStart, endClose, error) where endStart is the position of the
// opening {{ of {{ end }}, and endClose is the position after the closing }}.
func findMatchingEnd(body string, startPos int) (int, int, error) {
	depth := 1
	i := startPos

	for i < len(body)-1 {
		// Skip non-action content
		if body[i] != '{' || (i+1 < len(body) && body[i+1] != '{') {
			i++
			continue
		}

		// We found {{ — determine what kind of action it is
		actionStart := i

		// Check for comment {{/* ... */}}
		if i+3 < len(body) && body[i+2] == '/' && body[i+3] == '*' {
			end := strings.Index(body[i:], "*/}}")
			if end == -1 {
				return -1, -1, fmt.Errorf("unclosed comment")
			}
			i += end + 4
			continue
		}

		// Read the keyword of the action
		keyword, actionEnd := readActionKeyword(body, i)

		switch keyword {
		case "if", "range", "with", "block", "define", "wrap":
			depth++
		case "end":
			depth--
			if depth == 0 {
				return actionStart, actionEnd, nil
			}
		}

		i = actionEnd
	}

	return -1, -1, fmt.Errorf("missing {{ end }}")
}

// readActionKeyword reads the first keyword from a {{ ... }} action starting at pos.
// Returns the keyword and the position after the closing }}.
func readActionKeyword(body string, pos int) (string, int) {
	i := pos + 2 // skip {{

	// Skip whitespace and optional leading dash ({{- ...) after {{
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r' || body[i] == '-') {
		i++
	}

	// Read keyword
	start := i
	for i < len(body) && isWordChar(body[i]) {
		i++
	}
	keyword := body[start:i]

	// Find the closing }}
	for i < len(body)-1 {
		switch body[i] {
		case '"':
			i++
			for i < len(body) && body[i] != '"' {
				if body[i] == '\\' {
					i++
				}
				i++
			}
		case '`':
			i++
			for i < len(body) && body[i] != '`' {
				i++
			}
		case '}':
			if i+1 < len(body) && body[i+1] == '}' {
				return keyword, i + 2
			}
		}
		i++
	}

	return keyword, len(body)
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// detectOldSyntax checks for old HTML-like component syntax and provides
// helpful migration errors.
func detectOldSyntax(body string, known map[string]bool) error {
	m := oldPropSyntaxRegex.FindStringSubmatch(body)
	if m != nil {
		return fmt.Errorf("found old component syntax (e.g. %s): use {{ X (dict ...) }} or {{ wrap X (dict ...) }}...{{ end }} instead", m[0])
	}

	return nil
}

// escapeRawBlocks finds {{ raw }}...{{ endraw }} blocks and escapes all
// {{ and }} delimiters within them so Go's template engine treats the
// content as literal text. Markers are removed from the output.
//
// Whitespace is always trimmed: before {{ raw }}, at the start and end
// of the content, and after {{ endraw }}. This ensures raw blocks
// integrate cleanly into <pre><code> blocks without extra blank lines.
//
// Uses a manual scanner (not regex) because the content between markers
// contains {{ and }} which would confuse regex matching.
func escapeRawBlocks(body string) (string, error) {
	var result strings.Builder
	i := 0

	for i < len(body) {
		// Look for the next {{ that could be {{ raw }} or {{ endraw }}
		actionStart := findNextAction(body, i)
		if actionStart == -1 {
			result.WriteString(body[i:])
			break
		}

		keyword, actionEnd := readActionKeyword(body, actionStart)

		if keyword == "endraw" {
			line, _ := offsetToLineCol(body, actionStart)
			return "", fmt.Errorf("unexpected {{ endraw }} without matching {{ raw }} at line %d", line)
		}

		if keyword != "raw" {
			// Not a raw block, copy up to and past this action
			result.WriteString(body[i:actionEnd])
			i = actionEnd
			continue
		}

		// Found {{ raw }} — copy everything before it, trimming trailing whitespace
		before := strings.TrimRight(body[i:actionStart], " \t\n\r")
		result.WriteString(before)

		// Find the matching {{ endraw }}
		contentStart := actionEnd
		endrawStart, endrawEnd, err := findEndraw(body, contentStart)
		if err != nil {
			line, _ := offsetToLineCol(body, actionStart)
			return "", fmt.Errorf("unclosed {{ raw }} block at line %d", line)
		}

		// Extract content, trim surrounding whitespace, and escape for literal display
		content := strings.TrimSpace(body[contentStart:endrawStart])
		result.WriteString(escapeRawContent(content))

		// Skip whitespace after {{ endraw }}
		i = endrawEnd
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
			i++
		}
	}

	return result.String(), nil
}

// findNextAction finds the next {{ in body starting at pos.
// Returns -1 if not found.
func findNextAction(body string, pos int) int {
	for i := pos; i < len(body)-1; i++ {
		if body[i] == '{' && body[i+1] == '{' {
			return i
		}
	}
	return -1
}

// endrawRegex matches {{ endraw }} (with optional whitespace).
// Used only inside findEndraw to locate the closing marker in raw block content
// where {{ and }} appear as literal text.
var endrawRegex = regexp.MustCompile(`\{\{\s*endraw\s*\}\}`)

// findEndraw scans from startPos to find the {{ endraw }} that closes
// a raw block. Uses regex rather than readActionKeyword because the content
// inside raw blocks contains arbitrary {{ and }} that would confuse the
// action-aware scanner.
func findEndraw(body string, startPos int) (int, int, error) {
	loc := endrawRegex.FindStringIndex(body[startPos:])
	if loc == nil {
		return -1, -1, fmt.Errorf("missing {{ endraw }}")
	}
	return startPos + loc[0], startPos + loc[1], nil
}

// escapeRawContent escapes raw block content for literal display in a single
// pass. Template delimiters and HTML special characters are escaped so that
// Go's template engine and the browser both treat the content as plain text.
//
//   - {{ → {{ "{{" }}
//   - }} → {{ "}}" }}
//   - &  → &amp;
//   - <  → &lt;
//   - >  → &gt;
func escapeRawContent(content string) string {
	var result strings.Builder
	result.Grow(len(content) * 2) // pre-allocate for typical expansion
	i := 0
	for i < len(content) {
		if i < len(content)-1 && content[i] == '{' && content[i+1] == '{' {
			result.WriteString(`{{ "{{" }}`)
			i += 2
		} else if i < len(content)-1 && content[i] == '}' && content[i+1] == '}' {
			result.WriteString(`{{ "}}" }}`)
			i += 2
		} else if content[i] == '&' {
			result.WriteString("&amp;")
			i++
		} else if content[i] == '<' {
			result.WriteString("&lt;")
			i++
		} else if content[i] == '>' {
			result.WriteString("&gt;")
			i++
		} else {
			result.WriteByte(content[i])
			i++
		}
	}
	return result.String()
}

// offsetToLineCol converts a byte offset to a 1-indexed line number
// and 1-indexed column. Used for error messages.
func offsetToLineCol(text string, offset int) (int, int) {
	line := 1
	col := 1
	for i := 0; i < offset && i < len(text); i++ {
		if text[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
