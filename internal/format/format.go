package format

import (
	"fmt"
	"strings"

	"github.com/andrioid/gastro/internal/parser"
)

// FormatFile formats a .gastro file, returning the formatted content,
// whether it changed, and any error. On error the returned content is empty.
func FormatFile(filename, content string) (string, bool, error) {
	lineEnding := detectLineEnding(content)

	// Normalize to \n for processing
	normalized := content
	if lineEnding == "\r\n" {
		normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	}

	parsed, err := parser.Parse(filename, normalized)
	if err != nil {
		return "", false, fmt.Errorf("parse: %w", err)
	}

	hasFrontmatter := len(parsed.Imports) > 0 ||
		len(parsed.Uses) > 0 ||
		strings.TrimSpace(parsed.Frontmatter) != ""

	var result string
	if hasFrontmatter {
		formattedFM, fmErr := formatFrontmatter(parsed.Frontmatter, parsed.Imports, parsed.Uses)
		if fmErr != nil {
			return "", false, fmt.Errorf("format frontmatter: %w", fmErr)
		}

		formattedTpl := formatTemplate(parsed.TemplateBody)

		result = "---\n" + formattedFM + "\n---\n" + formattedTpl
	} else {
		result = formatTemplate(parsed.TemplateBody)
	}

	// Ensure final newline
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	// Restore original line endings
	if lineEnding == "\r\n" {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}

	changed := result != content
	return result, changed, nil
}

// detectLineEnding returns the line ending style used in the content.
func detectLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// collapseBlankLines collapses runs of multiple blank lines into at most one.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	prevBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		result = append(result, line)
		prevBlank = blank
	}
	return strings.Join(result, "\n")
}
