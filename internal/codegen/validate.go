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
// Track B (plans/frictions-plan.md §4.9): every call that writes to the
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

// ValidateDictKeys cross-checks the literal string keys passed to (dict ...)
// in component invocations against the destination component's Props field
// names. It returns one Warning per unknown key.
//
// The body must already have been through TransformTemplate; the {{ wrap }}
// form is gone by then and only the bare-call form {{ X (dict ...) }}
// remains. The compile-time-injected "Children" key (and any user-authored
// "Children" key on a children-rendering component) is recognised and
// silently skipped (it's plumbed through without being a user-defined
// Props field). The pre-A5 sentinel "__children" is no longer recognised
// at runtime; if encountered, the validator emits a targeted hint
// suggesting the new key name.
//
// Calls whose dict has any non-literal-string key are skipped entirely:
// when a template builds a dict dynamically (rare but legal) we can't know
// at compile time which keys it contains, so we don't false-positive.
//
// uses maps the caller's local PascalCase aliases to component file paths;
// propsByPath maps those file paths to the component's Props field list.
// (Schemas are keyed by path because the alias is user-chosen and may
// differ across importers of the same component.)
//
// If the body fails to parse with the stub FuncMap, ValidateDictKeys
// returns no warnings — the runtime template parser, the LSP, and
// upstream codegen errors are responsible for surfacing parse failures.
func ValidateDictKeys(
	body string,
	uses []gastroParser.UseDeclaration,
	propsByPath map[string][]StructField,
) []Warning {
	if len(uses) == 0 {
		return nil
	}

	// Map local alias → component path so we can resolve each invocation
	// to its schema even when two importers use different aliases.
	aliasPath := make(map[string]string, len(uses))
	for _, u := range uses {
		aliasPath[u.Name] = u.Path
	}

	trees, err := parse.Parse("template", body, "{{", "}}", dictValidationStubFuncs(uses))
	if err != nil {
		return nil
	}
	tree := trees["template"]
	if tree == nil || tree.Root == nil {
		return nil
	}

	var warnings []Warning
	walkParseNodes(tree.Root.Nodes, func(node parse.Node) {
		cmd, ok := node.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return
		}
		ident, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			return
		}
		compName := ident.Ident
		compPath, isImported := aliasPath[compName]
		if !isImported {
			return
		}

		// Look for the (dict ...) PipeNode in the remaining args.
		var dictPipe *parse.PipeNode
		for _, arg := range cmd.Args[1:] {
			pipe, ok := arg.(*parse.PipeNode)
			if !ok || len(pipe.Cmds) == 0 {
				continue
			}
			head := pipe.Cmds[0]
			if len(head.Args) == 0 {
				continue
			}
			if id, ok := head.Args[0].(*parse.IdentifierNode); ok && id.Ident == "dict" {
				dictPipe = pipe
				break
			}
		}
		if dictPipe == nil {
			// Bare call like {{ Card }} — no dict to validate.
			return
		}

		dictCmd := dictPipe.Cmds[0]
		// Dict args after the "dict" identifier are alternating key/value:
		// dict "K1" v1 "K2" v2 ...
		// Bail out if any odd-indexed arg isn't a string literal — the dict
		// is dynamic and we can't validate it without false positives.
		var literalKeys []*parse.StringNode
		for i := 1; i < len(dictCmd.Args); i += 2 {
			str, ok := dictCmd.Args[i].(*parse.StringNode)
			if !ok {
				return
			}
			literalKeys = append(literalKeys, str)
		}

		schema, hasSchema := propsByPath[compPath]
		if !hasSchema {
			// Component file is known but has no Props struct (no fields to
			// dispatch to). If callers pass any dict args at all, that's
			// already odd — but it's not strictly illegal; the dict just
			// gets ignored at render time. Don't warn for this case.
			return
		}
		validNames := make(map[string]bool, len(schema))
		for _, f := range schema {
			validNames[f.Name] = true
		}

		for _, key := range literalKeys {
			name := key.Text
			if name == "Children" {
				// Compile-time injected by TransformTemplate for wrap form,
				// or user-authored on a children-rendering component. Either
				// way, plumbed through to {{ .Children }} without being a
				// user-defined Props field.
				continue
			}
			if name == "__children" {
				// Pre-A5 sentinel. No longer recognised by the runtime.
				// Surface a targeted hint so users encountering this in
				// hand-written dicts know exactly what to change.
				line := nodeLine(body, key.Position())
				warnings = append(warnings, Warning{
					Line: line,
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
			line := nodeLine(body, key.Position())
			warnings = append(warnings, Warning{
				Line: line,
				Message: fmt.Sprintf(
					"unknown prop %q on component %s (valid: %s)",
					name, compName, joinSortedFieldNames(schema),
				),
			})
		}
	})
	return warnings
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
