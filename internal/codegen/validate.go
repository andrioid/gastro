package codegen

import (
	"fmt"
	"sort"
	"strings"
	"text/template/parse"

	gastroParser "github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/pkg/gastro"
)

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
// remains. The compile-time-injected "__children" key is recognised and
// silently skipped (it's not a Props field).
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
			if name == "__children" {
				// Compile-time injected by TransformTemplate for wrap form;
				// not a user-authored prop key.
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
