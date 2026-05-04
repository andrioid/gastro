package shadow

import (
	"fmt"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
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

// GenerateVirtualFile creates a virtual .go file from a .gastro file's
// content using the real codegen pipeline. The result is the same
// source `gastro generate` would emit (with `package gastro` rewritten
// to a synthetic per-file package name and stripped of any external
// runtime references that require a project context).
//
// Used by tests and one-shot callers that don't need a long-lived
// Workspace. Production LSP traffic goes through Workspace.UpdateFile,
// which additionally writes the virtual file plus a Router-stub
// companion to disk so gopls can analyse them as a Go package.
//
// For files without frontmatter (.gastro fragments used purely for
// markup), returns a minimal `package main` shell so callers receive a
// valid VirtualFile rather than nil.
func GenerateVirtualFile(filename, gastroContent string) (*VirtualFile, error) {
	parsed, err := parser.Parse(filename, gastroContent)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	if parsed.FrontmatterLine == 0 {
		return &VirtualFile{
			GoSource:  "package main\n\nfunc main() {}\n",
			SourceMap: sourcemap.New(1, 1),
			Filename:  filename,
		}, nil
	}

	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("analyzing frontmatter for %s: %w", filename, err)
	}

	isComponent := info.IsComponent
	if !isComponent && strings.HasPrefix(filename, "components/") {
		isComponent = true
	}

	src, err := codegen.GenerateHandler(parsed, info, isComponent)
	if err != nil {
		return nil, fmt.Errorf("generating shadow source for %s: %w", filename, err)
	}

	hasProps := info.PropsTypeName != ""
	virtualFmStart := codegen.FindFrontmatterStart(src, isComponent, hasProps)
	gastroFmStart := firstFrontmatterContentLine(gastroContent, parsed.FrontmatterLine, parsed.TemplateBodyLine-1, isComponent)

	return &VirtualFile{
		GoSource:           src,
		SourceMap:          sourcemap.New(gastroFmStart, virtualFmStart),
		Filename:           filename,
		FrontmatterEndLine: parsed.TemplateBodyLine - 1,
	}, nil
}
