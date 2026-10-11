package compiler

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

// Diagnostic is one finding reported by Compile, positioned in a .gastro
// source file.
type Diagnostic struct {
	File     string // project-relative path, e.g. "pages/index.gastro"
	Line     int    // 1-indexed
	Col      int    // 1-indexed, in bytes
	Severity codegen.DiagnosticSeverity
	Message  string
}

// IsError reports whether d fails generation.
func (d Diagnostic) IsError() bool {
	return d.Severity == codegen.SeverityError
}

// String formats d as `file:line:col: severity: message`, the shape
// editors and terminals recognise as a jump target.
func (d Diagnostic) String() string {
	severity := "warning"
	if d.IsError() {
		severity = "error"
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s", d.File, d.Line, d.Col, severity, d.Message)
}

// DiagnosticsError is returned by Compile when a run has at least one
// error-severity diagnostic (any diagnostic under CompileOptions.Strict).
// It carries every diagnostic of the run, errors and warnings, ordered by
// file and position. Compile writes nothing to the output directory when
// it returns a DiagnosticsError, so a previously generated tree stays
// intact.
type DiagnosticsError struct {
	Diagnostics []Diagnostic
}

// Error lists every diagnostic, one per line.
func (e *DiagnosticsError) Error() string {
	lines := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

// Summary counts the diagnostics, e.g. "3 errors, 1 warning".
func (e *DiagnosticsError) Summary() string {
	var errs, warns int
	for _, d := range e.Diagnostics {
		if d.IsError() {
			errs++
		} else {
			warns++
		}
	}
	s := plural(errs, "error")
	if warns > 0 {
		s += ", " + plural(warns, "warning")
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// sortDiagnostics orders diagnostics by file, then position. The sort is
// stable so findings at the same position keep their discovery order.
func sortDiagnostics(diags []Diagnostic) {
	slices.SortStableFunc(diags, func(a, b Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Col, b.Col),
		)
	})
}

// sourcePositions resolves positions within one .gastro file.
type sourcePositions struct {
	file  string
	lines []string
}

func newSourcePositions(file, content string) sourcePositions {
	return sourcePositions{file: file, lines: strings.Split(content, "\n")}
}

// at builds a diagnostic at line:col.
func (p sourcePositions) at(line, col int, severity codegen.DiagnosticSeverity, msg string) Diagnostic {
	return Diagnostic{File: p.file, Line: max(line, 1), Col: max(col, 1), Severity: severity, Message: msg}
}

// atLine builds a diagnostic for a finding known only by line: the
// column is the line's first non-blank character, where the statement
// it reports on starts.
func (p sourcePositions) atLine(line int, severity codegen.DiagnosticSeverity, msg string) Diagnostic {
	col := 1
	if line >= 1 && line <= len(p.lines) {
		text := p.lines[line-1]
		col = len(text) - len(strings.TrimLeft(text, " \t")) + 1
	}
	return p.at(line, col, severity, msg)
}

// embedDiagnostics positions a //gastro:embed failure. ProcessEmbedDirectives
// stops at the first problem; ValidateEmbedDirectives reports all of them
// with directive lines, so it is used to describe the failure. Problems
// only the full pass detects (e.g. an unreadable file) fall back to err at
// the start of the frontmatter.
func embedDiagnostics(pos sourcePositions, file *parser.File, ctx codegen.EmbedContext, err error) []Diagnostic {
	_, embedDiags := codegen.ValidateEmbedDirectives(file.Frontmatter, ctx)
	if len(embedDiags) == 0 {
		return []Diagnostic{pos.at(file.FrontmatterLine, 1, codegen.SeverityError, err.Error())}
	}
	diags := make([]Diagnostic, 0, len(embedDiags))
	for _, d := range embedDiags {
		diags = append(diags, pos.atLine(file.FrontmatterSourceLine(d.DirectiveLine), codegen.SeverityError, d.Message))
	}
	return diags
}
