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

// hasRestBag reports whether a component's Props schema includes an
// attribute-forwarding bag (a field of type gastro.Attrs). Such a
// component accepts arbitrary literal dict keys — they are forwarded as
// HTML attributes — so the unknown-prop diagnostic is suppressed for it.
func hasRestBag(fields []StructField) bool {
	for _, f := range fields {
		if f.Type == "gastro.Attrs" {
			return true
		}
	}
	return false
}

// DiagnosticSeverity classifies a TemplateDiagnostic. The integer
// values match the LSP spec (1=Error, 2=Warning) so callers can pass
// them through without remapping.
type DiagnosticSeverity int

const (
	// SeverityError is for diagnostics that fail generation: unknown
	// prop keys, bare calls to propful components, un-imported
	// components.
	SeverityError DiagnosticSeverity = 1
	// SeverityWarning is advisory — surfaced in the editor and as a
	// codegen warning; only strict builds fail on it. Today: deprecated
	// key hints.
	SeverityWarning DiagnosticSeverity = 2
)

// TemplateDiagnostic is a position-rich diagnostic for a template body.
// Both codegen (via ValidateTemplate) and the LSP (which lifts it to its
// own diagnostic format) consume the same underlying diagnostics, so the
// two paths cannot drift on which dict keys are valid —
// SyntheticPropKey is the only place that decision lives.
//
// Lines and columns are 1-indexed and resolved against the body string
// passed to the validator, in byte offsets (not runes). End positions
// are exclusive in the LSP convention but inclusive of the closing
// quote when the diagnostic anchors on a string literal — callers that
// need exclusive ends should add 1.
type TemplateDiagnostic struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	Severity  DiagnosticSeverity
	Message   string
}

// ValidateDictKeysOptions controls optional classes of diagnostics
// ValidateDictKeysFromAST emits. Codegen and the LSP pass the same
// options for the same project, so they cannot drift.
type ValidateDictKeysOptions struct {
	// TypecheckProps (gastro --typecheck-props) checks literal dict
	// values — strings, numbers, booleans and nil — against the field
	// type and reports mismatches as errors. Non-literal values are left
	// to the render-time check (gastroRuntime.CheckPropTypes).
	TypecheckProps bool
}

// ValidateDictKeysFromAST validates dict keys on a pre-parsed tree. It is
// the single source of truth for dict-key validation — codegen reaches it
// through ValidateTemplate and the LSP wraps it for its []Diagnostic
// channel.
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
//   - SeverityError, "requires props": the component declares a Props
//     struct but was called bare (no `(dict ...)` argument). At runtime
//     this would surface as "wrong number of args"; we catch it here
//     so editors and `gastro build` reject it before deployment.
//     Anchored on the component name.
//   - SeverityError, "missing required prop": a field tagged
//     gastro:"required" was not passed in the dict, anchored on the
//     component name. Optional fields (the default) never produce a
//     diagnostic when omitted. A {{ wrap }} call supplies Children.
//   - SeverityError, prop type mismatch (only when opts.TypecheckProps):
//     a literal value is not assignable to the field type, anchored on
//     the value.
//   - SeverityWarning, deprecated-key hint: the legacy "__children"
//     key was passed; runtime no longer recognises it.
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
) []TemplateDiagnostic {
	if tree == nil || tree.Root == nil || len(uses) == 0 {
		return nil
	}

	// Map local alias → component path so we can resolve each invocation
	// to its schema even when two importers use different aliases.
	aliasPath := make(map[string]string, len(uses))
	for _, u := range uses {
		aliasPath[u.Name] = u.Path
	}

	var diags []TemplateDiagnostic
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
		openSchema := hasRestBag(schema)

		var (
			literalKeys []*parse.StringNode
			values      []parse.Node
		)
		if dictPipe != nil {
			dictCmd := dictPipe.Cmds[0]
			// Dict args after the "dict" identifier are alternating key/value:
			// dict "K1" v1 "K2" v2 ...
			// Bail out if any odd-indexed arg isn't a string literal — the
			// dict is dynamic and we can't validate it without false
			// positives. A wrap call without a dict still reaches the
			// missing-required check below, so this early return only
			// applies when the dict is present-but-dynamic.
			for i := 1; i < len(dictCmd.Args); i += 2 {
				str, ok := dictCmd.Args[i].(*parse.StringNode)
				if !ok {
					return
				}
				literalKeys = append(literalKeys, str)
				var value parse.Node
				if i+1 < len(dictCmd.Args) {
					value = dictCmd.Args[i+1]
				}
				values = append(values, value)
			}
		} else if head.Ident != "wrap" {
			// Pure bare call ({{ Card }}) — no dict pipe. Always an error
			// for propful components: html/template will fail at execute
			// time with "wrong number of args" because the underlying
			// component method takes a single map[string]any. The compiler
			// only emits a variadic wrapper for propless components.
			// ({{ wrap Card }} without a dict is fine: the wrap transform
			// supplies a dict carrying Children.)
			nameStart := nodeLineCol(body, int(compIdent.Position()))
			nameEnd := nodeLineCol(body, int(compIdent.Position())+len(compIdent.Ident))
			diags = append(diags, TemplateDiagnostic{
				StartLine: nameStart.Line,
				StartCol:  nameStart.Col,
				EndLine:   nameEnd.Line,
				EndCol:    nameEnd.Col,
				Severity:  SeverityError,
				Message: fmt.Sprintf(
					"component %s requires props; call with (dict %q ...) — bare-call syntax is only valid for components with no Props struct",
					compName, firstFieldName(schema),
				),
			})
			// Missing required props are implied by this error, and
			// there are no keys to validate.
			return
		}
		fieldsByName := make(map[string]StructField, len(schema))
		for _, f := range schema {
			fieldsByName[f.Name] = f
		}

		providedKeys := make(map[string]bool, len(literalKeys)+1)
		if head.Ident == "wrap" {
			// The wrap transform injects the block body as Children.
			providedKeys["Children"] = true
		}
		for i, key := range literalKeys {
			name := key.Text
			providedKeys[name] = true
			switch kind, _ := SyntheticPropKey(name); kind {
			case SyntheticChildren:
				// Recognised synthetic key — silently skip.
				continue
			case SyntheticDeprecatedChildren:
				start, end := stringNodePosRange(body, key)
				diags = append(diags, TemplateDiagnostic{
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
			if field, ok := fieldsByName[name]; ok {
				if opts.TypecheckProps {
					if d, bad := literalTypeMismatch(body, compName, field, values[i]); bad {
						diags = append(diags, d)
					}
				}
				continue
			}
			if openSchema {
				continue
			}
			start, end := stringNodePosRange(body, key)
			diags = append(diags, TemplateDiagnostic{
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

		// Missing required props are anchored on the component name in
		// the source so the editor can underline the call site itself.
		// Optional fields are never reported: a zero value is a valid
		// default (D5).
		nameStart := nodeLineCol(body, int(compIdent.Position()))
		nameEnd := nodeLineCol(body, int(compIdent.Position())+len(compIdent.Ident))
		for _, f := range schema {
			if !f.Required || providedKeys[f.Name] {
				continue
			}
			diags = append(diags, TemplateDiagnostic{
				StartLine: nameStart.Line,
				StartCol:  nameStart.Col,
				EndLine:   nameEnd.Line,
				EndCol:    nameEnd.Col,
				Severity:  SeverityError,
				Message:   fmt.Sprintf("missing required prop %q on component %s", f.Name, compName),
			})
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

// firstFieldName returns the first declared Props field name, or a
// placeholder if the slice is empty. Used in the "requires props"
// diagnostic to give the user a concrete example of how to call the
// component (declaration order matches what the source file shows).
func firstFieldName(fields []StructField) string {
	if len(fields) == 0 {
		return "Key"
	}
	return fields[0].Name
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
