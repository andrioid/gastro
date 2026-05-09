package codegen

import (
	"fmt"
	"sort"
	"strings"
	"text/template/parse"

	"github.com/andrioid/gastro/internal/analysis"
	gastroParser "github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/pkg/gastro"
)

// ValidateFrontmatterReturns runs the shared response-write analyzer
// (internal/analysis) over a page's frontmatter and converts each finding
// into a Warning suitable for the codegen Warnings channel.
//
// Track B (docs/history/frictions-plan.md §4.9): every call that writes to the
// response (anything passing the literal `w`, `http.Redirect(w, r, …)`,
// or a method on `w`) must be followed by `return` — otherwise
// frontmatter execution continues and any uppercase variables computed
// after the write are dead code from the template's point of view.
//
// Components don't have `w`/`r` in scope, so this is a no-op for them in
// practice; callers nonetheless gate by the directory-derived
// isComponent flag for clarity and to avoid surprising warnings if a
// component author happens to use the names locally.
func ValidateFrontmatterReturns(frontmatter string) []Warning {
	findings := analysis.FindMissingReturns(frontmatter)
	if len(findings) == 0 {
		return nil
	}
	warnings := make([]Warning, 0, len(findings))
	for _, f := range findings {
		msg := "response was written but no return follows; frontmatter execution will continue and any uppercase variables computed after this point are dead code. Add `return` to short-circuit."
		if f.Snippet != "" {
			msg = fmt.Sprintf("%s (at: %s)", msg, f.Snippet)
		}
		warnings = append(warnings, Warning{
			Line:    f.Line,
			Message: msg,
		})
	}
	return warnings
}

// dictValidationStubFuncs returns a stub FuncMap suitable for parsing a
// post-TransformTemplate body with text/template/parse. Includes gastro
// default funcs, Go template builtins, the post-transform helper
// __gastro_render_children, and one stub per imported component name.
func dictValidationStubFuncs(uses []gastroParser.UseDeclaration) map[string]any {
	stubs := map[string]any{}
	for name := range gastro.DefaultFuncs() {
		stubs[name] = ""
	}
	// Go template builtins that text/template/parse requires in the FuncMap.
	for _, name := range []string{
		"and", "or", "not",
		"eq", "ne", "lt", "le", "gt", "ge",
		"print", "printf", "println",
		"len", "index", "slice", "call",
		"html", "js", "urlquery",
	} {
		stubs[name] = ""
	}
	// Post-transform plumbing.
	stubs["__gastro_render_children"] = ""
	// Imported components are bare-call functions in the post-transform body.
	for _, u := range uses {
		stubs[u.Name] = ""
	}
	return stubs
}

// DiagnosticSeverity classifies a DictKeyDiagnostic. The integer
// values match the LSP spec (1=Error, 2=Warning) so callers can pass
// them through without remapping.
type DiagnosticSeverity int

const (
	// SeverityError is for diagnostics that block strict-mode
	// compilation. Today: unknown prop keys.
	SeverityError DiagnosticSeverity = 1
	// SeverityWarning is advisory — surfaced in the editor and as a
	// codegen warning but does not block strict-mode builds. Today:
	// deprecated key hints, missing-prop notices.
	SeverityWarning DiagnosticSeverity = 2
)

// DictKeyDiagnostic is a position-rich diagnostic produced by the
// dict-key validator. Both codegen (which lossy-converts to []Warning
// for backward compatibility) and the LSP (which lifts to its own
// diagnostic format) consume the same underlying diagnostics, so the
// two paths cannot drift on which dict keys are valid —
// SyntheticPropKey is the only place that decision lives.
//
// Lines and columns are 1-indexed and resolved against the body string
// passed to the validator, in byte offsets (not runes). End positions
// are exclusive in the LSP convention but inclusive of the closing
// quote when the diagnostic anchors on a string literal — callers that
// need exclusive ends should add 1.
type DictKeyDiagnostic struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	Severity  DiagnosticSeverity
	Message   string
}

// ValidateDictKeys cross-checks the literal string keys passed to (dict ...)
// in component invocations against the destination component's Props field
// names. It returns one Warning per unknown key, mapped from the
// position-rich form via lossy conversion (StartLine only). New code
// should call ValidateDictKeysFromAST for column-rich diagnostics.
//
// The body must already have been through TransformTemplate; the {{ wrap }}
// form is gone by then and only the bare-call form {{ X (dict ...) }}
// remains. ValidateDictKeysFromAST also handles the pre-transform wrap
// form, so the LSP can use it on raw .gastro source.
//
// Synthetic prop keys ("Children", "__children") are handled via
// SyntheticPropKey — see synthetic.go for the canonical list.
func ValidateDictKeys(
	body string,
	uses []gastroParser.UseDeclaration,
	propsByPath map[string][]StructField,
) []Warning {
	if len(uses) == 0 {
		return nil
	}
	trees, err := parse.Parse("template", body, "{{", "}}", dictValidationStubFuncs(uses))
	if err != nil {
		return nil
	}
	tree := trees["template"]
	if tree == nil {
		return nil
	}
	diags := ValidateDictKeysFromAST(body, tree, uses, propsByPath, ValidateDictKeysOptions{})
	if len(diags) == 0 {
		return nil
	}
	warnings := make([]Warning, 0, len(diags))
	for _, d := range diags {
		warnings = append(warnings, Warning{Line: d.StartLine, Message: d.Message})
	}
	return warnings
}

// ValidateDictKeysOptions controls which classes of diagnostics
// ValidateDictKeysFromAST emits. The codegen-side caller leaves
// EmitMissingProps off (matching today's `gastro generate` behaviour
// of not warning about partial dicts — a component with
// zero-valued defaults can be intentionally invoked with no props).
// The LSP-side caller turns it on so the editor surfaces
// missing-prop hints as warnings. Both still share the unknown-prop
// and deprecated-key paths.
type ValidateDictKeysOptions struct {
	// EmitMissingProps adds a SeverityWarning diagnostic anchored on
	// the component name for every Props field that the caller did
	// not pass in the dict. Off by default to preserve
	// `gastro generate` behaviour; the LSP turns it on.
	EmitMissingProps bool
}

// ValidateDictKeysFromAST is the position-rich form of ValidateDictKeys
// that operates on a pre-parsed tree. It is the single source of truth
// for dict-key validation — codegen wraps it for its line-only []Warning
// channel and the LSP wraps it for its column-rich []Diagnostic channel.
//
// The tree may be either pre-transform (containing {{ wrap X (dict ...) }}
// blocks) or post-transform (only bare {{ X (dict ...) }} calls): both
// forms are recognised. The body string is the source the tree was parsed
// from — used to resolve byte offsets to (line, column) pairs.
//
// uses maps the caller's local PascalCase aliases to component file paths;
// propsByPath maps those file paths to the component's Props field list.
// (Schemas are keyed by path because the alias is user-chosen and may
// differ across importers of the same component. Two pages can import
// the same component as `Card` and `PostCard` respectively; both
// resolve to the same schema via the path.)
//
// Diagnostic kinds emitted:
//
//   - SeverityError, "unknown prop": a literal string key was passed
//     that does not match any field on the component's Props struct
//     and is not a recognised synthetic key.
//   - SeverityWarning, deprecated-key hint: the legacy "__children"
//     key was passed; runtime no longer recognises it.
//   - SeverityWarning, "missing prop" (only when opts.EmitMissingProps):
//     a Props field was not passed in the dict, anchored on the
//     component name in the source.
//
// Calls whose dict has any non-literal-string key are skipped entirely:
// when a template builds a dict dynamically (rare but legal) we can't
// know at compile time which keys it contains, so we don't false-positive.
func ValidateDictKeysFromAST(
	body string,
	tree *parse.Tree,
	uses []gastroParser.UseDeclaration,
	propsByPath map[string][]StructField,
	opts ValidateDictKeysOptions,
) []DictKeyDiagnostic {
	if tree == nil || tree.Root == nil || len(uses) == 0 {
		return nil
	}

	// Map local alias → component path so we can resolve each invocation
	// to its schema even when two importers use different aliases.
	aliasPath := make(map[string]string, len(uses))
	for _, u := range uses {
		aliasPath[u.Name] = u.Path
	}

	var diags []DictKeyDiagnostic
	walkParseNodes(tree.Root.Nodes, func(node parse.Node) {
		cmd, ok := node.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return
		}
		head, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			return
		}

		// Resolve which arg holds the component identifier and where the
		// dict pipe is expected to appear.
		//
		//   {{ X (dict ...) }}              → component is Args[0], dict is in Args[1:]
		//   {{ wrap X (dict ...) }}          → component is Args[1], dict is in Args[2:]
		//
		// The pre-transform form is what the LSP sees; the post-transform
		// form is what the compiler sees after TransformTemplate has run.
		var (
			compIdent      *parse.IdentifierNode
			dictSearchArgs []parse.Node
		)
		if head.Ident == "wrap" && len(cmd.Args) >= 2 {
			next, ok := cmd.Args[1].(*parse.IdentifierNode)
			if !ok {
				return
			}
			compIdent = next
			dictSearchArgs = cmd.Args[2:]
		} else {
			compIdent = head
			dictSearchArgs = cmd.Args[1:]
		}

		compName := compIdent.Ident
		compPath, isImported := aliasPath[compName]
		if !isImported {
			return
		}

		var dictPipe *parse.PipeNode
		for _, arg := range dictSearchArgs {
			pipe, ok := arg.(*parse.PipeNode)
			if !ok || len(pipe.Cmds) == 0 {
				continue
			}
			first := pipe.Cmds[0]
			if len(first.Args) == 0 {
				continue
			}
			if id, ok := first.Args[0].(*parse.IdentifierNode); ok && id.Ident == "dict" {
				dictPipe = pipe
				break
			}
		}

		schema, hasSchema := propsByPath[compPath]
		if !hasSchema {
			// Component file is known but has no Props struct (no fields to
			// dispatch to). If callers pass any dict args at all, that's
			// already odd — but it's not strictly illegal; the dict just
			// gets ignored at render time. Don't warn for this case.
			return
		}

		var literalKeys []*parse.StringNode
		if dictPipe != nil {
			dictCmd := dictPipe.Cmds[0]
			// Dict args after the "dict" identifier are alternating key/value:
			// dict "K1" v1 "K2" v2 ...
			// Bail out if any odd-indexed arg isn't a string literal — the
			// dict is dynamic and we can't validate it without false
			// positives. We still want to fire missing-prop warnings for
			// the bare-call case (no dict at all), so this early return
			// only applies when the dict is present-but-dynamic.
			for i := 1; i < len(dictCmd.Args); i += 2 {
				str, ok := dictCmd.Args[i].(*parse.StringNode)
				if !ok {
					return
				}
				literalKeys = append(literalKeys, str)
			}
		} else if !opts.EmitMissingProps {
			// Pure bare call ({{ Card }}) — no dict, no keys to validate.
			// Skip unless the caller wants missing-prop warnings, which
			// fire for every Props field on a bare call.
			return
		}
		validNames := make(map[string]bool, len(schema))
		for _, f := range schema {
			validNames[f.Name] = true
		}

		providedKeys := make(map[string]bool, len(literalKeys))
		for _, key := range literalKeys {
			name := key.Text
			providedKeys[name] = true
			switch kind, _ := SyntheticPropKey(name); kind {
			case SyntheticChildren:
				// Recognised synthetic key — silently skip.
				continue
			case SyntheticDeprecatedChildren:
				start, end := stringNodePosRange(body, key)
				diags = append(diags, DictKeyDiagnostic{
					StartLine: start.Line,
					StartCol:  start.Col,
					EndLine:   end.Line,
					EndCol:    end.Col,
					Severity:  SeverityWarning,
					Message: fmt.Sprintf(
						`dict key %q is no longer recognised on component %s; use %q instead`,
						name, compName, "Children",
					),
				})
				continue
			}
			if validNames[name] {
				continue
			}
			start, end := stringNodePosRange(body, key)
			diags = append(diags, DictKeyDiagnostic{
				StartLine: start.Line,
				StartCol:  start.Col,
				EndLine:   end.Line,
				EndCol:    end.Col,
				Severity:  SeverityError,
				Message: fmt.Sprintf(
					"unknown prop %q on component %s (valid: %s)",
					name, compName, joinSortedFieldNames(schema),
				),
			})
		}

		// Missing-prop warnings are anchored on the component name in
		// the source so the editor can underline the call site itself.
		// Off by default — partial dicts are sometimes intentional, and
		// surfacing them as warnings in `gastro generate` would noise up
		// CI runs that strict-mode-block on any warning.
		if opts.EmitMissingProps {
			nameStart := nodeLineCol(body, int(compIdent.Position()))
			nameEnd := nodeLineCol(body, int(compIdent.Position())+len(compIdent.Ident))
			for _, f := range schema {
				if providedKeys[f.Name] {
					continue
				}
				diags = append(diags, DictKeyDiagnostic{
					StartLine: nameStart.Line,
					StartCol:  nameStart.Col,
					EndLine:   nameEnd.Line,
					EndCol:    nameEnd.Col,
					Severity:  SeverityWarning,
					Message:   fmt.Sprintf("missing prop %q on component %s", f.Name, compName),
				})
			}
		}
	})
	return diags
}

// linecol is a 1-indexed (line, column) pair within a body string.
type linecol struct{ Line, Col int }

// stringNodePosRange returns the start and end positions of a
// *parse.StringNode in (line, col) form. The start is the byte position
// of the opening quote of the literal; the end is the byte position of
// the character following the closing quote (so EndCol-StartCol equals
// the literal's source length, including both quote characters). This
// keeps the diagnostic span aligned with what the user sees in their
// editor: the underlined region covers the entire "..." literal.
func stringNodePosRange(body string, n *parse.StringNode) (linecol, linecol) {
	start := nodeLineCol(body, int(n.Position()))
	end := nodeLineCol(body, int(n.Position())+len(n.String()))
	return start, end
}

// nodeLineCol converts a byte offset within body to a 1-indexed
// (line, column) pair.
func nodeLineCol(body string, offset int) linecol {
	if offset < 0 {
		offset = 0
	}
	if offset > len(body) {
		offset = len(body)
	}
	line := 1
	col := 1
	for i := 0; i < offset; i++ {
		if body[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return linecol{Line: line, Col: col}
}

// walkParseNodes is a small text/template/parse walker. It descends into
// list, action, if/range/with, template, and pipe nodes so component calls
// nested anywhere in the tree are visited.
func walkParseNodes(nodes []parse.Node, visit func(parse.Node)) {
	for _, n := range nodes {
		visitNode(n, visit)
	}
}

func visitNode(n parse.Node, visit func(parse.Node)) {
	if n == nil {
		return
	}
	visit(n)
	switch nn := n.(type) {
	case *parse.ListNode:
		if nn != nil {
			walkParseNodes(nn.Nodes, visit)
		}
	case *parse.ActionNode:
		if nn.Pipe != nil {
			visitNode(nn.Pipe, visit)
		}
	case *parse.PipeNode:
		for _, cmd := range nn.Cmds {
			visitNode(cmd, visit)
		}
	case *parse.CommandNode:
		for _, arg := range nn.Args {
			visitNode(arg, visit)
		}
	case *parse.IfNode:
		visitNode(nn.Pipe, visit)
		visitNode(nn.List, visit)
		visitNode(nn.ElseList, visit)
	case *parse.RangeNode:
		visitNode(nn.Pipe, visit)
		visitNode(nn.List, visit)
		visitNode(nn.ElseList, visit)
	case *parse.WithNode:
		visitNode(nn.Pipe, visit)
		visitNode(nn.List, visit)
		visitNode(nn.ElseList, visit)
	case *parse.TemplateNode:
		if nn.Pipe != nil {
			visitNode(nn.Pipe, visit)
		}
	}
}

// nodeLine converts a parse.Pos (byte offset) into a 1-indexed line number
// against the original template body.
func nodeLine(body string, pos parse.Pos) int {
	if int(pos) > len(body) {
		return 1
	}
	return 1 + strings.Count(body[:int(pos)], "\n")
}

// joinSortedFieldNames returns a comma-separated, alphabetically-sorted list
// of struct-field names. Used in unknown-prop warnings so the suggestion is
// stable across runs.
func joinSortedFieldNames(fields []StructField) string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
