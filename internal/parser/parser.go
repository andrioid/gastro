package parser

import (
	"fmt"
	"strings"
	"unicode"
)

// UseDeclaration represents a component import: `import ComponentName "path/to/component.gastro"`.
type UseDeclaration struct {
	Name string // e.g. "Card"
	Path string // e.g. "components/card.gastro"
}

// File is the result of parsing a .gastro file.
type File struct {
	Filename         string
	Frontmatter      string           // Go code between --- delimiters, with imports stripped
	TemplateBody     string           // HTML template after the second ---
	Imports          []string         // extracted Go package import paths
	Uses             []UseDeclaration // extracted component imports (aliased .gastro imports)
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

	imports, uses, err := extractImports(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	cleanedFrontmatter := stripImports(frontmatter)

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
//
// Aliased imports with a .gastro path suffix are treated as component imports
// and returned as UseDeclarations. All other imports are returned as Go
// package import paths.
//
// Examples:
//
//	import "fmt"                                  → Go import
//	import Layout "components/layout.gastro"      → component import
//	import (
//	    "fmt"
//	    Layout "components/layout.gastro"
//	)                                             → mixed
func extractImports(frontmatter string) ([]string, []UseDeclaration, error) {
	var imports []string
	var uses []UseDeclaration
	lines := strings.Split(frontmatter, "\n")
	inString := false

	for i := 0; i < len(lines); i++ {
		// Skip lines inside raw string literals (backtick strings).
		if !inString {
			inString = hasUnclosedBacktick(lines[i])
		} else {
			if hasUnclosedBacktick(lines[i]) {
				inString = false
			}
			continue
		}

		trimmed := strings.TrimSpace(lines[i])

		// Grouped import: import ( ... )
		if trimmed == "import (" {
			i++
			for i < len(lines) {
				trimmed = strings.TrimSpace(lines[i])
				if trimmed == ")" {
					break
				}
				if trimmed == "" {
					i++
					continue
				}

				imp, use, err := parseImportSpec(trimmed)
				if err != nil {
					return nil, nil, err
				}
				if use != nil {
					uses = append(uses, *use)
				} else if imp != "" {
					imports = append(imports, imp)
				}
				i++
			}
			continue
		}

		// Single import: import "path" or import Alias "path"
		if strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, "import (") {
			rest := strings.TrimPrefix(trimmed, "import ")
			imp, use, err := parseImportSpec(strings.TrimSpace(rest))
			if err != nil {
				return nil, nil, fmt.Errorf("in %q: %w", trimmed, err)
			}
			if use != nil {
				uses = append(uses, *use)
			} else if imp != "" {
				imports = append(imports, imp)
			}
		}
	}

	return imports, uses, nil
}

// parseImportSpec parses a single import spec like:
//
//	"fmt"                              → Go import "fmt"
//	Layout "components/layout.gastro"  → component UseDeclaration
//	. "components/foo.gastro"          → error (dot import not allowed for .gastro)
//	_ "components/foo.gastro"          → error (blank import not allowed for .gastro)
func parseImportSpec(spec string) (goImport string, use *UseDeclaration, err error) {
	// Simple quoted path: "fmt"
	if path := unquote(spec); path != "" {
		if strings.HasSuffix(path, ".gastro") {
			return "", nil, fmt.Errorf("component import %q requires an alias (e.g. import MyComponent %q)", path, path)
		}
		return path, nil, nil
	}

	// Aliased import: Alias "path"
	parts := strings.SplitN(spec, " ", 2)
	if len(parts) != 2 {
		return "", nil, nil
	}

	alias := parts[0]
	path := unquote(strings.TrimSpace(parts[1]))
	if path == "" {
		return "", nil, nil
	}

	if strings.HasSuffix(path, ".gastro") {
		if alias == "." {
			return "", nil, fmt.Errorf("dot imports are not allowed for component imports (%s)", path)
		}
		if alias == "_" {
			return "", nil, fmt.Errorf("blank imports are not allowed for component imports (%s)", path)
		}
		if !isExportedName(alias) {
			return "", nil, fmt.Errorf("component import alias %q must start with an uppercase letter", alias)
		}
		return "", &UseDeclaration{Name: alias, Path: path}, nil
	}

	// Aliased Go import — not currently used in gastro frontmatter but
	// we preserve the path for the generated import block. The alias is
	// not tracked because the codegen emits the raw import path.
	return path, nil, nil
}

// isExportedName returns true if the name starts with an uppercase letter.
func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// stripImports removes all import declarations from frontmatter, returning
// only the remaining Go code. Trims leading/trailing blank lines.
func stripImports(frontmatter string) string {
	lines := strings.Split(frontmatter, "\n")
	var kept []string
	inGroupedImport := false
	inString := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Preserve lines inside raw string literals (backtick strings).
		if !inString {
			inString = hasUnclosedBacktick(line)
		} else {
			if hasUnclosedBacktick(line) {
				inString = false
			}
			kept = append(kept, line)
			continue
		}

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
