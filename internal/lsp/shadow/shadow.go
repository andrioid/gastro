package shadow

import (
	"fmt"
	"strings"

	"github.com/andrioid/gastro/internal/lsp/sourcemap"
	"github.com/andrioid/gastro/internal/parser"
)

// VirtualFile represents a generated .go file from a .gastro file's frontmatter.
type VirtualFile struct {
	GoSource           string               // The complete virtual .go file content
	SourceMap          *sourcemap.SourceMap // Maps virtual line numbers to .gastro line numbers
	Filename           string               // Original .gastro filename
	FrontmatterEndLine int                  // 1-indexed gastro line of the closing ---
}

// GenerateVirtualFile creates a virtual .go file from a .gastro file's content.
// The frontmatter is wrapped in a valid Go function so gopls can analyze it.
// Import declarations are converted to comments to preserve line numbers.
// For files without frontmatter, returns a minimal valid Go file.
func GenerateVirtualFile(filename, gastroContent string) (*VirtualFile, error) {
	parsed, err := parser.Parse(filename, gastroContent)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	// No frontmatter means no Go code for gopls to analyze.
	if parsed.FrontmatterLine == 0 {
		return &VirtualFile{
			GoSource:  "package __gastro_virtual\n",
			SourceMap: sourcemap.New(1, 1),
			Filename:  filename,
		}, nil
	}

	// Reconstruct the raw frontmatter (before stripping) by getting lines
	// between the delimiters. We need the original lines including import
	// statements to preserve line numbers.
	rawFrontmatter := extractRawFrontmatter(gastroContent)

	// Comment out import lines to preserve line numbers
	processedFrontmatter := commentOutImports(rawFrontmatter)

	// Build the virtual .go file
	var sb strings.Builder

	// Line 1: package
	sb.WriteString("package __gastro_virtual\n")

	// Lines 2+: imports.
	// Track B (plans/frictions-plan.md §4.2) makes pages reference the
	// ambient r and w; the synthetic wrapper passes both as parameters
	// so frontmatter that calls r.Method, w.WriteHeader, etc. type-checks
	// in gopls. net/http is added unconditionally for the same reason.
	sb.WriteString("\nimport (\n\t\"net/http\"\n")
	for _, imp := range parsed.Imports {
		sb.WriteString(fmt.Sprintf("\t%q\n", imp))
	}
	sb.WriteString(")\n")

	// Suppress unused-import warnings if frontmatter doesn't reference net/http.
	sb.WriteString("\nvar _ http.ResponseWriter\n")

	// Gastro package stub so gopls doesn't error on gastro.Context() etc.
	sb.WriteString("\nvar gastro = struct{ Context func() interface{} }{}\n")

	// Function wrapper start. Track B injects (w, r) ambient parameters.
	// `_ = w` / `_ = r` suppress unused-parameter warnings for pages
	// that don't touch them and for components (which never do).
	sb.WriteString("\nfunc __handler(w http.ResponseWriter, r *http.Request) {\n")
	sb.WriteString("\t_ = w\n\t_ = r\n")
	virtualFMStart := strings.Count(sb.String(), "\n") + 1

	// Frontmatter content (with import lines commented out)
	sb.WriteString(processedFrontmatter)
	sb.WriteString("\n}\n")

	sm := sourcemap.New(parsed.FrontmatterLine, virtualFMStart)

	return &VirtualFile{
		GoSource:  sb.String(),
		SourceMap: sm,
		Filename:  filename,
	}, nil
}

// extractRawFrontmatter gets the content between --- delimiters from raw
// .gastro file content, without any processing.
func extractRawFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	firstDelim := -1
	secondDelim := -1

	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if firstDelim == -1 {
				firstDelim = i
			} else {
				secondDelim = i
				break
			}
		}
	}

	if firstDelim == -1 || secondDelim == -1 {
		return ""
	}

	return strings.Join(lines[firstDelim+1:secondDelim], "\n")
}
