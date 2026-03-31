package parser

import (
	"fmt"
	"strings"
)

// UseDeclaration represents a `use ComponentName "path/to/component.gastro"` line.
type UseDeclaration struct {
	Name string // e.g. "Card"
	Path string // e.g. "components/card.gastro"
}

// File is the result of parsing a .gastro file.
type File struct {
	Filename         string
	Frontmatter      string           // Go code between --- delimiters, with imports and use declarations stripped
	TemplateBody     string           // HTML template after the second ---
	Imports          []string         // extracted import paths
	Uses             []UseDeclaration // extracted use declarations
	FrontmatterLine  int              // 1-indexed line number where frontmatter content starts
	TemplateBodyLine int              // 1-indexed line number where template body starts
}

const delimiter = "---"

// Parse parses a .gastro file's content and returns its constituent parts.
func Parse(filename, content string) (*File, error) {
	frontmatter, body, fmLine, bodyLine, err := splitSections(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	imports, err := extractImports(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	uses, err := extractUses(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	cleanedFrontmatter := stripImportsAndUses(frontmatter)

	return &File{
		Filename:         filename,
		Frontmatter:      cleanedFrontmatter,
		TemplateBody:     body,
		Imports:          imports,
		Uses:             uses,
		FrontmatterLine:  fmLine,
		TemplateBodyLine: bodyLine,
	}, nil
}

// splitSections splits content at --- delimiters into frontmatter and template
// body. A delimiter is only recognised when it appears on its own line (trimmed)
// and is NOT inside a string literal.
func splitSections(content string) (frontmatter, body string, fmLine, bodyLine int, err error) {
	lines := strings.Split(content, "\n")

	openDelim := -1
	closeDelim := -1
	inString := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track whether we're inside a multi-line string (backtick).
		// For regular quoted strings on a single line, the delimiter
		// can only appear inside the quotes so trimmed == "---" won't
		// match. We only need to worry about raw string literals.
		if !inString {
			inString = hasUnclosedBacktick(line)
		} else {
			if hasUnclosedBacktick(line) {
				inString = false
			}
			continue
		}

		if trimmed == delimiter {
			if openDelim == -1 {
				openDelim = i
			} else {
				closeDelim = i
				break
			}
		}
	}

	if openDelim == -1 {
		return "", "", 0, 0, fmt.Errorf("missing opening --- delimiter")
	}
	if closeDelim == -1 {
		return "", "", 0, 0, fmt.Errorf("missing closing --- delimiter")
	}

	// Frontmatter is the lines between the two delimiters
	fmLines := lines[openDelim+1 : closeDelim]
	frontmatter = strings.Join(fmLines, "\n")

	// Template body is everything after the closing delimiter
	if closeDelim+1 < len(lines) {
		bodyLines := lines[closeDelim+1:]
		body = strings.Join(bodyLines, "\n")
	}

	// 1-indexed line numbers
	fmLine = openDelim + 2    // line after first ---
	bodyLine = closeDelim + 2 // line after second ---

	return frontmatter, body, fmLine, bodyLine, nil
}

// hasUnclosedBacktick returns true if the line has an odd number of backticks,
// indicating a raw string literal that spans multiple lines.
func hasUnclosedBacktick(line string) bool {
	return strings.Count(line, "`")%2 != 0
}

// extractImports parses import declarations from frontmatter.
// Supports both single imports and grouped imports.
func extractImports(frontmatter string) ([]string, error) {
	var imports []string
	lines := strings.Split(frontmatter, "\n")

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		// Grouped import: import ( ... )
		if trimmed == "import (" {
			i++
			for i < len(lines) {
				trimmed = strings.TrimSpace(lines[i])
				if trimmed == ")" {
					break
				}
				path := unquote(trimmed)
				if path != "" {
					imports = append(imports, path)
				}
				i++
			}
			continue
		}

		// Single import: import "path"
		if strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, "import (") {
			rest := strings.TrimPrefix(trimmed, "import ")
			path := unquote(strings.TrimSpace(rest))
			if path != "" {
				imports = append(imports, path)
			}
		}
	}

	return imports, nil
}

// extractUses parses `use Name "path"` declarations from frontmatter.
func extractUses(frontmatter string) ([]UseDeclaration, error) {
	var uses []UseDeclaration
	lines := strings.Split(frontmatter, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "use ") {
			continue
		}

		rest := strings.TrimPrefix(trimmed, "use ")
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid use declaration: %s", trimmed)
		}

		name := parts[0]
		path := unquote(strings.TrimSpace(parts[1]))
		if path == "" {
			return nil, fmt.Errorf("invalid use declaration (missing path): %s", trimmed)
		}

		uses = append(uses, UseDeclaration{Name: name, Path: path})
	}

	return uses, nil
}

// stripImportsAndUses removes import and use declarations from frontmatter,
// returning only the remaining Go code. Trims leading/trailing blank lines.
func stripImportsAndUses(frontmatter string) string {
	lines := strings.Split(frontmatter, "\n")
	var kept []string
	inGroupedImport := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if inGroupedImport {
			if trimmed == ")" {
				inGroupedImport = false
			}
			continue
		}

		if trimmed == "import (" {
			inGroupedImport = true
			continue
		}

		// Skip single imports
		if strings.HasPrefix(trimmed, "import ") {
			continue
		}

		// Skip use declarations
		if strings.HasPrefix(trimmed, "use ") {
			continue
		}

		kept = append(kept, line)
	}

	result := strings.Join(kept, "\n")
	result = strings.TrimSpace(result)
	return result
}

// unquote strips surrounding double quotes from a string.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return ""
}
