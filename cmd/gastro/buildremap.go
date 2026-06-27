package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/andrioid/gastro/internal/compiler"
	"github.com/andrioid/gastro/internal/parser"
)

// genDiagRegex matches a `go build` diagnostic that points at a generated
// file at the .gastro/ root, e.g. ".gastro/pages_index.go:24:9" or
// ".gastro/components_card.go:12". Captures path, line, and optional column.
var genDiagRegex = regexp.MustCompile(`\.gastro/([A-Za-z0-9_]+)\.go:(\d+)(?::(\d+))?`)

// funcSigRegex matches the generated handler/component method signature so
// the remapper can locate the start of the in-function frontmatter region.
var funcSigRegex = regexp.MustCompile(`^func \(__router \*Router\) \w+\(`)

// fmEndAnchor marks the line immediately after the emitted frontmatter body
// in both the page handler and component templates (see internal/codegen
// generate.go handlerTmpl/componentTmpl).
const fmEndAnchor = "// Suppress unused-var warnings for exported frontmatter vars"

// genFileMap describes how to translate a line in one generated Go file back
// to its .gastro source. The frontmatter body is contiguous in the source
// (imports are stripped from the top, hoisted decls are blanked in place),
// so a single offset measured at the tail of the region maps every
// frontmatter line exactly. Lines outside [fmGenStart, fmGenEnd] have no
// precise source line and fall back to a file-level pointer.
type genFileMap struct {
	source     string // source .gastro relative path
	fmGenStart int    // first generated line of the frontmatter region (exclusive bound: funcSig+1)
	fmGenEnd   int    // last generated line of the frontmatter region (the fmEndAnchor line minus 1)
	offset     int    // sourceLine = genLine + offset, valid within the region
	hasOffset  bool   // false when the region/offset could not be determined
}

// buildGenFileIndex walks projectDir for .gastro sources and returns a map
// keyed by generated basename ("pages_index.go") describing how to remap its
// diagnostics. Files that exist but can't be analysed still appear with
// hasOffset=false so the file-level pointer still works.
func buildGenFileIndex(projectDir string) map[string]genFileMap {
	index := map[string]genFileMap{}
	sources, err := collectGastroFiles(projectDir)
	if err != nil {
		return index
	}
	outputDir := filepath.Join(projectDir, ".gastro")
	for _, src := range sources {
		rel, relErr := filepath.Rel(projectDir, src)
		if relErr != nil {
			rel = src
		}
		rel = filepath.ToSlash(rel)
		base := compiler.OutputGoFile(rel)
		m := genFileMap{source: rel}
		if off, gs, ge, ok := computeFrontmatterOffset(src, filepath.Join(outputDir, base)); ok {
			m.offset, m.fmGenStart, m.fmGenEnd, m.hasOffset = off, gs, ge, true
		}
		index[base] = m
	}
	return index
}

// computeFrontmatterOffset reads the source .gastro and its generated .go and
// derives the line offset that maps generated frontmatter lines to source
// lines. It anchors on the LAST non-blank line of the frontmatter region in
// each file: imports are stripped above the body (a uniform upward shift) and
// hoisted declarations are blanked in place (no shift), so the tail offset is
// constant across the whole body.
func computeFrontmatterOffset(srcPath, genPath string) (offset, genStart, genEnd int, ok bool) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 0, 0, 0, false
	}
	parsed, err := parser.Parse(srcPath, string(srcBytes))
	if err != nil || parsed.TemplateBodyLine <= parsed.FrontmatterLine {
		return 0, 0, 0, false
	}
	srcLines := strings.Split(string(srcBytes), "\n")
	// Frontmatter content occupies [FrontmatterLine, TemplateBodyLine-2]
	// (TemplateBodyLine-1 is the closing `---`). Find its last code line.
	srcLastCode := lastNonBlank(srcLines, parsed.FrontmatterLine, parsed.TemplateBodyLine-2)
	if srcLastCode == 0 {
		return 0, 0, 0, false
	}

	genBytes, err := os.ReadFile(genPath)
	if err != nil {
		return 0, 0, 0, false
	}
	genLines := strings.Split(string(genBytes), "\n")
	funcLine := 0
	endLine := 0
	for i, line := range genLines {
		if funcLine == 0 && funcSigRegex.MatchString(line) {
			funcLine = i + 1
			continue
		}
		if funcLine != 0 && strings.Contains(line, fmEndAnchor) {
			endLine = i + 1
			break
		}
	}
	if funcLine == 0 || endLine == 0 || endLine <= funcLine+1 {
		return 0, 0, 0, false
	}
	genLastCode := lastNonBlank(genLines, funcLine+1, endLine-1)
	if genLastCode == 0 {
		return 0, 0, 0, false
	}
	return srcLastCode - genLastCode, funcLine + 1, genLastCode, true
}

// lastNonBlank returns the 1-indexed line number of the last non-blank line
// within [start, end] (1-indexed, inclusive), or 0 if none. lines is
// 0-indexed.
func lastNonBlank(lines []string, start, end int) int {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	for i := end; i >= start; i-- {
		if strings.TrimSpace(lines[i-1]) != "" {
			return i
		}
	}
	return 0
}

// remapBuildOutput rewrites `go build` diagnostics that reference generated
// .gastro/*.go files so they point at the original .gastro source. Lines in a
// file's frontmatter region are remapped to the exact source line; every
// other generated reference keeps the source file with a breadcrumb to the
// generated coordinate. Output with no generated references is returned
// unchanged.
func remapBuildOutput(output, projectDir string) string {
	if !strings.Contains(output, ".gastro/") {
		return output
	}
	index := buildGenFileIndex(projectDir)
	return genDiagRegex.ReplaceAllStringFunc(output, func(match string) string {
		sub := genDiagRegex.FindStringSubmatch(match)
		base := sub[1] + ".go"
		m, found := index[base]
		if !found {
			return match
		}
		line, _ := strconv.Atoi(sub[2])
		if m.hasOffset && line > m.fmGenStart-1 && line <= m.fmGenEnd {
			srcLine := line + m.offset
			if sub[3] != "" {
				return fmt.Sprintf("%s:%d:%s", m.source, srcLine, sub[3])
			}
			return fmt.Sprintf("%s:%d", m.source, srcLine)
		}
		// Outside the frontmatter region (hoisted declarations or template
		// plumbing): point at the source file, keep the generated location
		// as a breadcrumb.
		return fmt.Sprintf("%s [generated %s]", m.source, match)
	})
}
