package template

import (
	"fmt"
	"text/template/parse"
)

// WalkDiagnostics walks a parsed template tree and produces diagnostics for
// unknown variable references, respecting scope changes from range/with blocks.
//
// At depth 0, .FieldName is checked against exportedNames.
// At depth > 0 (inside range/with), .FieldName checks are silently skipped
// because the dot has been rebound to the range element or with value.
// $.FieldName is always checked regardless of depth ($ refers to root data).
func WalkDiagnostics(tree *parse.Tree, body string, exportedNames map[string]bool) []Diagnostic {
	if tree == nil || tree.Root == nil {
		return nil
	}

	w := &walker{
		body:          body,
		exportedNames: exportedNames,
	}
	w.walkList(tree.Root, 0)
	return w.diags
}

type walker struct {
	body          string
	exportedNames map[string]bool
	diags         []Diagnostic
}

func (w *walker) walkList(list *parse.ListNode, depth int) {
	if list == nil {
		return
	}
	for _, node := range list.Nodes {
		w.walkNode(node, depth)
	}
}

func (w *walker) walkNode(node parse.Node, depth int) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *parse.ListNode:
		w.walkList(n, depth)

	case *parse.ActionNode:
		w.walkPipe(n.Pipe, depth)

	case *parse.RangeNode:
		// The pipe (what's being ranged over) is evaluated at current depth
		w.walkPipe(n.Pipe, depth)
		// The body has dot rebound to each element
		w.walkList(n.List, depth+1)
		w.walkList(n.ElseList, depth+1)

	case *parse.WithNode:
		// The pipe (the with value) is evaluated at current depth
		w.walkPipe(n.Pipe, depth)
		// The body has dot rebound to the with value
		w.walkList(n.List, depth+1)
		w.walkList(n.ElseList, depth+1)

	case *parse.IfNode:
		// if does NOT rebind dot
		w.walkPipe(n.Pipe, depth)
		w.walkList(n.List, depth)
		w.walkList(n.ElseList, depth)

	case *parse.TemplateNode:
		w.walkPipe(n.Pipe, depth)

	case *parse.PipeNode:
		w.walkPipe(n, depth)

	case *parse.FieldNode:
		w.checkField(n, depth)

	case *parse.VariableNode:
		w.checkVariable(n, depth)

	case *parse.CommandNode:
		w.walkCommand(n, depth)

	case *parse.ChainNode:
		w.walkNode(n.Node, depth)

		// Text, Dot, String, Number, Bool, Nil, Comment, Break, Continue
		// — no variable references to check
	}
}

func (w *walker) walkPipe(pipe *parse.PipeNode, depth int) {
	if pipe == nil {
		return
	}
	for _, cmd := range pipe.Cmds {
		w.walkCommand(cmd, depth)
	}
}

func (w *walker) walkCommand(cmd *parse.CommandNode, depth int) {
	if cmd == nil {
		return
	}
	for _, arg := range cmd.Args {
		w.walkNode(arg, depth)
	}
}

// ScopeInfo describes the cursor's position within the template AST.
type ScopeInfo struct {
	Depth    int    // 0 = top-level, >0 = inside range/with
	RangeVar string // the variable being ranged/with'd (e.g. "Posts" from .Posts)
}

// CursorScope walks the template AST to determine what scope the cursor is in.
// cursorOffset is the byte offset of the cursor within the template body.
// Returns the innermost scope context, or depth 0 if the cursor is at top level.
func CursorScope(tree *parse.Tree, cursorOffset int) ScopeInfo {
	if tree == nil || tree.Root == nil {
		return ScopeInfo{}
	}
	return cursorScopeList(tree.Root, cursorOffset, ScopeInfo{})
}

func cursorScopeList(list *parse.ListNode, cursor int, current ScopeInfo) ScopeInfo {
	if list == nil {
		return current
	}
	for _, node := range list.Nodes {
		result := cursorScopeNode(node, cursor, current)
		if result.Depth > current.Depth || (result.Depth == current.Depth && result.RangeVar != current.RangeVar) {
			current = result
		}
	}
	return current
}

func cursorScopeNode(node parse.Node, cursor int, current ScopeInfo) ScopeInfo {
	if node == nil {
		return current
	}

	switch n := node.(type) {
	case *parse.ListNode:
		return cursorScopeList(n, cursor, current)

	case *parse.RangeNode:
		if nodeContainsCursor(n.List, cursor) || nodeContainsCursor(n.ElseList, cursor) {
			rangeVar := pipeFieldName(n.Pipe)
			inner := ScopeInfo{Depth: current.Depth + 1, RangeVar: rangeVar}
			if nodeContainsCursor(n.List, cursor) {
				return cursorScopeList(n.List, cursor, inner)
			}
			return cursorScopeList(n.ElseList, cursor, inner)
		}

	case *parse.WithNode:
		if nodeContainsCursor(n.List, cursor) || nodeContainsCursor(n.ElseList, cursor) {
			withVar := pipeFieldName(n.Pipe)
			inner := ScopeInfo{Depth: current.Depth + 1, RangeVar: withVar}
			if nodeContainsCursor(n.List, cursor) {
				return cursorScopeList(n.List, cursor, inner)
			}
			return cursorScopeList(n.ElseList, cursor, inner)
		}

	case *parse.IfNode:
		// if does not rebind dot — keep current scope
		if nodeContainsCursor(n.List, cursor) {
			return cursorScopeList(n.List, cursor, current)
		}
		if nodeContainsCursor(n.ElseList, cursor) {
			return cursorScopeList(n.ElseList, cursor, current)
		}
	}

	return current
}

// nodeContainsCursor checks if the cursor byte offset falls within a ListNode's range.
func nodeContainsCursor(list *parse.ListNode, cursor int) bool {
	if list == nil || len(list.Nodes) == 0 {
		return false
	}
	first := list.Nodes[0]
	last := list.Nodes[len(list.Nodes)-1]
	return cursor >= int(first.Position()) && cursor <= int(last.Position())+nodeLen(last)
}

// nodeLen estimates the byte length of a node. For text nodes this is exact;
// for others it's approximate. Used only for cursor containment checks.
func nodeLen(node parse.Node) int {
	if tn, ok := node.(*parse.TextNode); ok {
		return len(tn.Text)
	}
	// For non-text nodes, use the String() representation length as estimate
	return len(node.String())
}

// pipeFieldName extracts the first field name from a pipe expression.
// For "range .Posts", returns "Posts". For complex pipes, returns "".
func pipeFieldName(pipe *parse.PipeNode) string {
	if pipe == nil || len(pipe.Cmds) == 0 {
		return ""
	}
	cmd := pipe.Cmds[0]
	if len(cmd.Args) == 0 {
		return ""
	}
	if field, ok := cmd.Args[0].(*parse.FieldNode); ok && len(field.Ident) > 0 {
		return field.Ident[0]
	}
	return ""
}

// HoverTarget describes the template AST node under the cursor.
type HoverTarget struct {
	Kind   string // "field", "variable", "function"
	Name   string // field/var name (e.g. "Title") or func name (e.g. "upper")
	Pos    int    // byte offset of the start of this token in the template body
	EndPos int    // byte offset of the end of this token
}

// NodeAtCursor walks the template AST to find the leaf node at the given
// byte offset within the template body. Returns nil if the cursor is not
// on a meaningful token (e.g. on HTML text or whitespace).
func NodeAtCursor(tree *parse.Tree, cursorOffset int) *HoverTarget {
	if tree == nil || tree.Root == nil {
		return nil
	}
	return nodeAtCursorList(tree.Root, cursorOffset)
}

func nodeAtCursorList(list *parse.ListNode, cursor int) *HoverTarget {
	if list == nil {
		return nil
	}
	for _, node := range list.Nodes {
		if result := nodeAtCursorNode(node, cursor); result != nil {
			return result
		}
	}
	return nil
}

func nodeAtCursorNode(node parse.Node, cursor int) *HoverTarget {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *parse.ListNode:
		return nodeAtCursorList(n, cursor)

	case *parse.ActionNode:
		return nodeAtCursorPipe(n.Pipe, cursor)

	case *parse.RangeNode:
		if r := nodeAtCursorPipe(n.Pipe, cursor); r != nil {
			return r
		}
		if r := nodeAtCursorList(n.List, cursor); r != nil {
			return r
		}
		return nodeAtCursorList(n.ElseList, cursor)

	case *parse.WithNode:
		if r := nodeAtCursorPipe(n.Pipe, cursor); r != nil {
			return r
		}
		if r := nodeAtCursorList(n.List, cursor); r != nil {
			return r
		}
		return nodeAtCursorList(n.ElseList, cursor)

	case *parse.IfNode:
		if r := nodeAtCursorPipe(n.Pipe, cursor); r != nil {
			return r
		}
		if r := nodeAtCursorList(n.List, cursor); r != nil {
			return r
		}
		return nodeAtCursorList(n.ElseList, cursor)

	case *parse.TemplateNode:
		return nodeAtCursorPipe(n.Pipe, cursor)

	case *parse.PipeNode:
		return nodeAtCursorPipe(n, cursor)

	case *parse.CommandNode:
		return nodeAtCursorCommand(n, cursor)

	case *parse.FieldNode:
		// .FieldName — Pos is at the dot, range is 1 + len(Ident[0])
		if len(n.Ident) > 0 {
			start := int(n.Pos)
			end := start + 1 + len(n.Ident[0])
			if cursor >= start && cursor < end {
				return &HoverTarget{Kind: "field", Name: n.Ident[0], Pos: start, EndPos: end}
			}
		}

	case *parse.VariableNode:
		// $.FieldName — Pos points to the '.' after '$', so '$' is at Pos-1
		if len(n.Ident) >= 2 && n.Ident[0] == "$" {
			start := int(n.Pos) - 1                 // include the '$'
			end := int(n.Pos) + 1 + len(n.Ident[1]) // dot + fieldName
			if cursor >= start && cursor < end {
				return &HoverTarget{Kind: "variable", Name: n.Ident[1], Pos: start, EndPos: end}
			}
		}

	case *parse.IdentifierNode:
		start := int(n.Pos)
		end := start + len(n.Ident)
		if cursor >= start && cursor < end {
			return &HoverTarget{Kind: "function", Name: n.Ident, Pos: start, EndPos: end}
		}

	case *parse.ChainNode:
		return nodeAtCursorNode(n.Node, cursor)
	}

	return nil
}

func nodeAtCursorPipe(pipe *parse.PipeNode, cursor int) *HoverTarget {
	if pipe == nil {
		return nil
	}
	for _, cmd := range pipe.Cmds {
		if r := nodeAtCursorCommand(cmd, cursor); r != nil {
			return r
		}
	}
	return nil
}

func nodeAtCursorCommand(cmd *parse.CommandNode, cursor int) *HoverTarget {
	if cmd == nil {
		return nil
	}
	for _, arg := range cmd.Args {
		if r := nodeAtCursorNode(arg, cursor); r != nil {
			return r
		}
	}
	return nil
}

// checkField handles .FieldName nodes. At depth 0, the first field in the
// chain is checked against exported frontmatter names.
func (w *walker) checkField(n *parse.FieldNode, depth int) {
	if depth > 0 {
		return // dot has been rebound — can't validate without type info
	}

	if len(n.Ident) == 0 {
		return
	}

	fieldName := n.Ident[0]
	if w.exportedNames[fieldName] {
		return
	}

	// Position is the byte offset of the dot before the field name
	offset := int(n.Pos)
	startLine, startChar := OffsetToLineChar(w.body, offset)
	// End position covers ".FieldName" (dot + first identifier)
	endOffset := offset + 1 + len(fieldName)
	endLine, endChar := OffsetToLineChar(w.body, endOffset)

	w.diags = append(w.diags, Diagnostic{
		StartLine: startLine,
		StartChar: startChar,
		EndLine:   endLine,
		EndChar:   endChar,
		Message:   fmt.Sprintf("unknown template variable %q", "."+fieldName),
	})
}

// checkVariable handles $-prefixed variables. $.FieldName always refers to the
// root data, so it is checked against exports regardless of depth.
func (w *walker) checkVariable(n *parse.VariableNode, depth int) {
	// Only check $.FieldName — bare $ or local $var are not field accesses
	if len(n.Ident) < 2 || n.Ident[0] != "$" {
		return
	}

	fieldName := n.Ident[1]
	if w.exportedNames[fieldName] {
		return
	}

	// Position covers the $.FieldName expression
	offset := int(n.Pos)
	startLine, startChar := OffsetToLineChar(w.body, offset)
	endOffset := offset + 1 + 1 + len(fieldName) // $ + . + fieldName
	endLine, endChar := OffsetToLineChar(w.body, endOffset)

	w.diags = append(w.diags, Diagnostic{
		StartLine: startLine,
		StartChar: startChar,
		EndLine:   endLine,
		EndChar:   endChar,
		Message:   fmt.Sprintf("unknown template variable %q", "."+fieldName),
	})
}
