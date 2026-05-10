package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/parser"
)

// varTypeSpanInLine locates the type expression in a `var X T`
// declaration and returns its (start, end) byte offsets within the
// line. Returns ok=false for malformed input.
//
// The grammar this needs to handle is intentionally narrow: every
// other shape (parenthesized group, multi-name spec, explicit
// initializer) is already rejected upstream by
// codegen.ValidateEmbedDirectives with a different diagnostic kind,
// so the BadVarType code-action path only ever sees `var <name> <type>`
// with an optional trailing line comment.
//
// Supported shapes (cursor span shown with ^):
//
//	var X string                 → span over `string`
//	var X []byte                 → span over `[]byte`
//	var X template.HTML          → span over `template.HTML`
//	var X int  // tail comment   → span over `int`
func varTypeSpanInLine(line string) (start, end int, ok bool) {
	// Skip leading whitespace.
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	// Require `var` keyword followed by whitespace.
	if !strings.HasPrefix(line[i:], "var") {
		return 0, 0, false
	}
	i += len("var")
	if i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return 0, 0, false
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	// Skip the var name (an identifier).
	nameStart := i
	for i < len(line) && isIdentByte(line[i]) {
		i++
	}
	if i == nameStart {
		return 0, 0, false
	}
	// Require whitespace after the name.
	if i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return 0, 0, false
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	// Type expression starts here. End on `=`, `//`, or end-of-line.
	typeStart := i
	typeEnd := len(line)
	for j := i; j < len(line); j++ {
		if line[j] == '=' {
			typeEnd = j
			break
		}
		if j+1 < len(line) && line[j] == '/' && line[j+1] == '/' {
			typeEnd = j
			break
		}
	}
	// Trim trailing whitespace inside [typeStart, typeEnd).
	for typeEnd > typeStart && (line[typeEnd-1] == ' ' || line[typeEnd-1] == '\t') {
		typeEnd--
	}
	if typeEnd <= typeStart {
		return 0, 0, false
	}
	return typeStart, typeEnd, true
}

// isIdentByte reports whether b is allowed in a Go identifier (ASCII
// subset — sufficient because var names in user code are conventionally
// ASCII; the check is only used to delimit the name, not validate it).
func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// embedDirectiveAtLine inspects a single line of source for the
// `//gastro:embed PATH` shape and returns the path argument with its
// (start, end) byte offsets within the line. Returns ok=false when
// the line isn't an embed directive. Whitespace before `//` is allowed.
func embedDirectiveAtLine(line string) (path string, argStart, argEnd int, ok bool) {
	idx := strings.Index(line, "//gastro:embed")
	if idx < 0 {
		return "", 0, 0, false
	}
	// Reject mid-line directives: only treat as ours if the prefix is
	// pure whitespace. Anything else is probably a comment-of-comment
	// or a string literal containing the token.
	for i := 0; i < idx; i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return "", 0, 0, false
		}
	}
	rest := line[idx+len("//gastro:embed"):]
	// Require at least one whitespace separator.
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", 0, 0, false
	}
	// Strip leading separators, record start col.
	consumed := idx + len("//gastro:embed")
	for consumed < len(line) && (line[consumed] == ' ' || line[consumed] == '\t') {
		consumed++
	}
	argStart = consumed
	argEnd = len(line)
	// Trim trailing whitespace from the arg span for hover/completion.
	for argEnd > argStart && (line[argEnd-1] == ' ' || line[argEnd-1] == '\t') {
		argEnd--
	}
	if argStart >= argEnd {
		return "", 0, 0, false
	}
	return line[argStart:argEnd], argStart, argEnd, true
}

// embedDirectiveHover provides a markdown hover blob describing the
// resolved real path and var binding for the directive on the cursor's
// line. Returns nil when the cursor isn't on a directive line.
func (s *server) embedDirectiveHover(uri, content string, parsed *parser.File, pos proxy.Position) any {
	lines := strings.Split(content, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	line := lines[pos.Line]
	path, argStart, argEnd, ok := embedDirectiveAtLine(line)
	if !ok {
		return nil
	}

	sourceFile := uriToPath(uri)
	if sourceFile == "" {
		return nil
	}
	moduleRoot := codegen.FindModuleRootForFile(sourceFile)
	if moduleRoot == "" {
		return nil
	}

	// Try to resolve. The hover response includes status info even when
	// resolution fails so users see why their directive is failing.
	var statusLine string
	resolved, err := resolveEmbedPathLSP(path, sourceFile, moduleRoot)
	if err != nil {
		statusLine = fmt.Sprintf("**Error:** %s", err.Error())
	} else {
		// Try to attach a binding line if the next line(s) describe a
		// `var X T` decl. Cheap regex-free scan.
		var binding string
		for i := pos.Line + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" {
				continue // skip blank lines (orphan/grammar diag will fire elsewhere)
			}
			if strings.HasPrefix(trimmed, "var ") {
				binding = trimmed
			}
			break
		}
		if binding != "" {
			statusLine = fmt.Sprintf("Resolved to:\n```\n%s\n```\nBinds to: `%s`", resolved, binding)
		} else {
			statusLine = fmt.Sprintf("Resolved to:\n```\n%s\n```", resolved)
		}
	}

	return map[string]any{
		"contents": map[string]any{
			"kind":  "markdown",
			"value": fmt.Sprintf("**`//gastro:embed %s`**\n\n%s", path, statusLine),
		},
		"range": map[string]any{
			"start": map[string]any{"line": pos.Line, "character": argStart},
			"end":   map[string]any{"line": pos.Line, "character": argEnd},
		},
	}
}

// embedDirectiveCompletion offers filesystem-relative path completions
// when the cursor sits inside a `//gastro:embed PATH` argument span.
// Returns nil if the cursor isn't on a directive arg.
//
// The completer walks the candidate directory (resolved relative to
// the .gastro source file's location, with the typed prefix folded in)
// and emits LSP CompletionItem entries for matching files and dirs.
// `.md` is sorted to the top via sortText since it's the canonical
// embed target.
func (s *server) embedDirectiveCompletion(uri, content string, pos proxy.Position) []map[string]any {
	lines := strings.Split(content, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	line := lines[pos.Line]

	// Cursor must be inside the arg span. Allow cursor *at* argEnd
	// (position right after the last typed char) since that's the
	// natural completion trigger.
	_, argStart, argEnd, ok := embedDirectiveAtLine(line)
	if !ok {
		return nil
	}
	col := pos.Character
	if col < argStart || col > argEnd {
		return nil
	}

	sourceFile := uriToPath(uri)
	if sourceFile == "" {
		return nil
	}
	sourceDir := filepath.Dir(sourceFile)

	// Whatever the user has typed up to the cursor is the "input".
	input := line[argStart:col]
	// Split into directory prefix + partial basename: "../foo/ba" ->
	// dir="../foo", partial="ba".
	dirPart, partial := filepath.Split(input)
	if dirPart == "" {
		dirPart = "."
	}
	candidateDir := filepath.Join(sourceDir, dirPart)

	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		return nil
	}

	var items []map[string]any
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, partial) {
			continue
		}
		// Hide common noise: dotfiles, the generated .gastro/ dir.
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Replace the partial with the full entry name. Use a textEdit
		// so the editor swaps just the partial, not the whole arg.
		var insert string
		var sortPrefix string
		if entry.IsDir() {
			insert = name + "/"
			sortPrefix = "1-" // dirs after .md but before others
		} else if strings.HasSuffix(name, ".md") {
			insert = name
			sortPrefix = "0-" // .md first
		} else {
			insert = name
			sortPrefix = "2-"
		}
		kind := completionKindFile
		if entry.IsDir() {
			kind = completionKindFolder
		}
		// Compute the textEdit range: from where the partial starts to
		// the cursor.
		partialStart := col - len(partial)
		items = append(items, map[string]any{
			"label":    name,
			"kind":     kind,
			"sortText": sortPrefix + name,
			"textEdit": map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": pos.Line, "character": partialStart},
					"end":   map[string]any{"line": pos.Line, "character": col},
				},
				"newText": insert,
			},
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["sortText"].(string) < items[j]["sortText"].(string)
	})
	return items
}

// resolveEmbedPathLSP mirrors the codegen resolver but returns the
// error text in a form suitable for a hover blob (no line-number
// wrapping). Reuses codegen's actual resolver via a thin wrapper to
// stay in lockstep with the production rules.
func resolveEmbedPathLSP(path, sourceFile, moduleRoot string) (string, error) {
	// Constructing an EmbedContext + calling into codegen would normally
	// require importing the unexported helper. Replicate the public
	// shape via the exported FindModuleRootForFile for the boundary
	// check and EvalSymlinks for the resolution; keep the messages
	// short so they fit a hover.
	cleanCandidate, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), path))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(moduleRoot, cleanCandidate); err == nil &&
		(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", fmt.Errorf("escapes module root (%s)", moduleRoot)
	}
	resolved, err := filepath.EvalSymlinks(cleanCandidate)
	if err != nil {
		return "", fmt.Errorf("not found")
	}
	return resolved, nil
}
