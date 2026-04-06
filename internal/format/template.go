package format

import (
	"strings"
)

// formatTemplate formats the template body of a .gastro file by normalizing
// indentation. It does not split or join lines — only adjusts leading
// whitespace. Uses tabs for indentation to match gofmt.
func formatTemplate(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}

	lines := strings.Split(body, "\n")
	ind := &indenter{}
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		formatted := ind.processLine(line)
		result = append(result, formatted)
	}

	return strings.Join(result, "\n")
}

type indenter struct {
	depth              int
	inVerbatim         bool
	verbatimCloseTag   string
	inMultiLineAction  bool
	inMultiLineComment bool // HTML comment spanning multiple lines
	inTemplateComment  bool // {{/* ... */}} spanning multiple lines
}

func (ind *indenter) processLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Empty lines pass through
	if trimmed == "" {
		return ""
	}

	// --- Verbatim block handling ---
	if ind.inVerbatim {
		if isVerbatimClose(trimmed, ind.verbatimCloseTag) {
			ind.inVerbatim = false
			// Closing tag of verbatim block gets depth decremented
			ind.depth--
			if ind.depth < 0 {
				ind.depth = 0
			}
		}
		return line // Preserve exactly as-is
	}

	// --- Multi-line HTML comment handling ---
	if ind.inMultiLineComment {
		if strings.Contains(trimmed, "-->") {
			ind.inMultiLineComment = false
		}
		return line // Preserve exactly as-is
	}

	// --- Multi-line template comment handling ---
	if ind.inTemplateComment {
		if strings.Contains(trimmed, "*/}}") {
			ind.inTemplateComment = false
		}
		return line // Preserve exactly as-is
	}

	// --- Multi-line template action handling ---
	if ind.inMultiLineAction {
		if strings.Contains(trimmed, "}}") {
			ind.inMultiLineAction = false
		}
		return line // Preserve original indentation
	}

	// --- Check for multi-line comment starts ---
	if isMultiLineHTMLCommentStart(trimmed) {
		indented := ind.indentLine(trimmed)
		ind.inMultiLineComment = true
		return indented
	}

	if isMultiLineTemplateCommentStart(trimmed) {
		indented := ind.indentLine(trimmed)
		ind.inTemplateComment = true
		return indented
	}

	// --- Check for verbatim block starts ---
	if tag, ok := isVerbatimOpen(trimmed); ok {
		indented := ind.indentLine(trimmed)
		ind.inVerbatim = true
		ind.verbatimCloseTag = tag
		// The opening tag itself increases depth (for when verbatim ends)
		ind.depth++
		return indented
	}

	// --- Check for multi-line template action ---
	if hasTemplateOpen(trimmed) && !hasTemplateClose(trimmed) {
		indented := ind.indentLine(trimmed)
		ind.inMultiLineAction = true
		return indented
	}

	// --- Calculate depth change from this line ---
	before, after := ind.lineDepthChange(trimmed)

	// Apply "before" change (closing constructs at start of line)
	ind.depth += before
	if ind.depth < 0 {
		ind.depth = 0
	}

	indented := ind.indentLine(trimmed)

	// Apply "after" change (opening constructs increase for next line)
	ind.depth += after

	return indented
}

// lineDepthChange calculates the depth change for a line.
// Returns (before, after):
//   - before: applied before indenting this line (negative for closing tags/end)
//   - after: applied after indenting this line (positive for opening tags/block starts)
func (ind *indenter) lineDepthChange(line string) (before, after int) {
	// Analyze template actions
	actions := findAllActions(line)
	blockStarts := 0
	blockEnds := 0
	elses := 0
	for _, action := range actions {
		if isBlockEndAction(action) {
			blockEnds++
		} else if isElseAction(action) {
			elses++
		} else if isBlockStartAction(action) {
			blockStarts++
		}
	}

	// Pair up inline blocks: {{ if .X }}...{{ end }} on the same line cancel out
	paired := min(blockStarts, blockEnds)
	blockStarts -= paired
	blockEnds -= paired

	// Remaining unpaired ends go into "before" (dedent this line)
	before -= blockEnds

	// Remaining unpaired starts go into "after" (indent next line)
	after += blockStarts

	// else/else-if: dedent for this line, re-indent for children
	before -= elses
	after += elses

	// Analyze HTML tags.
	//
	// Only closing tags at the START of the line affect "before" (dedent this line).
	// This ensures `<p>text</p>` stays at parent depth (no before-decrease),
	// while `</div>` on its own line gets properly dedented.
	if startsWithClosingTag(line) {
		before--
	}

	// For "after", compute net depth change from ALL tags on the line.
	// This handles multi-tag lines like `<div><span>text</span></div>` correctly.
	opens := countOpeningTags(line)
	closes := countClosingTags(line)
	net := opens - closes

	// If line starts with a closing tag, we already accounted for one close
	// in "before", so add it back to net for "after" calculation.
	if startsWithClosingTag(line) {
		net++
	}

	if net > 0 {
		after += net
	} else if net < 0 {
		// More closes than opens (beyond the start-of-line one) — unusual
		// but handle it: these extra closes don't affect this line's indent
		// (that's handled by `before` when the close is on its own line).
		// They do reduce indent for subsequent lines.
		after += net
	}

	return before, after
}

func (ind *indenter) indentLine(trimmed string) string {
	if ind.depth <= 0 {
		return trimmed
	}
	return strings.Repeat("\t", ind.depth) + trimmed
}

// startsWithClosingTag returns true if the line starts with an HTML closing tag.
func startsWithClosingTag(line string) bool {
	return strings.HasPrefix(line, "</")
}

// --- Template action parsing ---

// extractActionKeyword extracts the first keyword from a template action.
// Input: "{{ if .Show }}" → "if"
// Input: "{{- range .Items -}}" → "range"
// Input: "{{ end }}" → "end"
func extractActionKeyword(action string) string {
	// Strip {{ and }}
	s := strings.TrimPrefix(action, "{{")
	if idx := strings.Index(s, "}}"); idx >= 0 {
		s = s[:idx]
	}

	// Strip whitespace-control markers
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSuffix(s, "-")
	s = strings.TrimSpace(s)

	// Return first word
	if idx := strings.IndexAny(s, " \t"); idx >= 0 {
		return s[:idx]
	}
	return s
}

// findAllActions returns all template actions ({{ ... }}) in a line.
func findAllActions(line string) []string {
	var actions []string
	remaining := line
	for {
		start := strings.Index(remaining, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(remaining[start:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		actions = append(actions, remaining[start:end])
		remaining = remaining[end:]
	}
	return actions
}

var blockStartKeywords = map[string]bool{
	"if":     true,
	"range":  true,
	"with":   true,
	"block":  true,
	"define": true,
	"wrap":   true,
}

// isBlockStartAction returns true if the action opens a block.
func isBlockStartAction(action string) bool {
	kw := extractActionKeyword(action)
	return blockStartKeywords[kw]
}

// isBlockEndAction returns true if the action is {{ end }}.
func isBlockEndAction(action string) bool {
	kw := extractActionKeyword(action)
	return kw == "end"
}

// isElseAction returns true if the action is {{ else }} or {{ else if ... }}.
func isElseAction(action string) bool {
	kw := extractActionKeyword(action)
	return kw == "else"
}

// hasTemplateOpen returns true if the line contains "{{".
func hasTemplateOpen(line string) bool {
	return strings.Contains(line, "{{")
}

// hasTemplateClose returns true if the line contains "}}".
func hasTemplateClose(line string) bool {
	return strings.Contains(line, "}}")
}

// --- Template comments ---

// isMultiLineTemplateCommentStart returns true for {{/* without closing */}}.
func isMultiLineTemplateCommentStart(line string) bool {
	return strings.Contains(line, "{{/*") && !strings.Contains(line, "*/}}")
}

// --- HTML tag parsing ---

// extractTagName extracts the tag name from an HTML tag string starting with "<".
// Returns empty string if not a valid tag.
func extractTagName(s string) string {
	if len(s) < 2 || s[0] != '<' {
		return ""
	}

	start := 1
	if s[1] == '/' {
		start = 2
	}

	end := start
	for end < len(s) {
		ch := s[end]
		if ch == ' ' || ch == '>' || ch == '/' || ch == '\t' || ch == '\n' {
			break
		}
		end++
	}

	if end == start {
		return ""
	}

	return strings.ToLower(s[start:end])
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

// --- Multi-line HTML comment ---

// isMultiLineHTMLCommentStart returns true for <!-- without closing -->.
func isMultiLineHTMLCommentStart(line string) bool {
	return strings.Contains(line, "<!--") && !strings.Contains(line, "-->")
}

// --- Verbatim blocks ---

var verbatimTags = map[string]bool{
	"pre": true, "script": true, "style": true, "textarea": true,
}

// isVerbatimOpen checks if the line opens a verbatim block.
// Returns the closing marker and true if it does.
func isVerbatimOpen(line string) (string, bool) {
	// {{ raw }} block
	for _, action := range findAllActions(line) {
		kw := extractActionKeyword(action)
		if kw == "raw" {
			return "endraw", true
		}
	}

	// HTML verbatim tags — only if this is an opening tag
	if strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "</") {
		tag := extractTagName(line)
		if verbatimTags[tag] {
			// Check that the closing tag isn't on the same line
			closingTag := "</" + tag
			if !strings.Contains(line, closingTag) {
				return tag, true
			}
		}
	}

	return "", false
}

// isVerbatimClose checks if the line closes the current verbatim block.
func isVerbatimClose(line, closeTag string) bool {
	if closeTag == "endraw" {
		for _, action := range findAllActions(line) {
			kw := extractActionKeyword(action)
			if kw == "endraw" {
				return true
			}
		}
		return false
	}
	return strings.Contains(line, "</"+closeTag+">")
}

// --- HTML tag counting ---

// countOpeningTags counts non-void, non-self-closing HTML opening tags in a line.
func countOpeningTags(line string) int {
	count := 0
	remaining := line
	for {
		idx := strings.Index(remaining, "<")
		if idx < 0 {
			break
		}
		rest := remaining[idx:]
		// Skip closing tags, comments, and doctype
		if strings.HasPrefix(rest, "</") || strings.HasPrefix(rest, "<!") {
			remaining = remaining[idx+1:]
			continue
		}

		tag := extractTagName(rest)
		if tag == "" || voidElements[tag] {
			remaining = remaining[idx+1:]
			continue
		}

		// Find the end of this tag to check for self-closing
		tagEnd := strings.Index(rest, ">")
		if tagEnd >= 0 {
			tagContent := rest[:tagEnd+1]
			if !strings.Contains(tagContent, "/>") {
				count++
			}
			remaining = remaining[idx+tagEnd+1:]
		} else {
			remaining = remaining[idx+1:]
		}
	}
	return count
}

// countClosingTags counts HTML closing tags in a line.
func countClosingTags(line string) int {
	count := 0
	remaining := line
	for {
		idx := strings.Index(remaining, "</")
		if idx < 0 {
			break
		}
		tag := extractTagName(remaining[idx:])
		if tag != "" {
			count++
		}
		remaining = remaining[idx+2:]
	}
	return count
}
