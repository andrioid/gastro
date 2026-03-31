package template

import (
	"fmt"
	"strings"
	"text/template/parse"
)

// FieldEntry describes a single field on a type, including its type
// for recursive resolution of nested range/with blocks.
type FieldEntry struct {
	Name string
	Type string // e.g. "string", "[]db.Item", "*db.Author"
}

// FieldResolver resolves a type's fields given the type name and a Go
// expression that evaluates to a value of that type (for gopls probing).
// Returns nil if the type is unknown or can't be resolved.
type FieldResolver func(typeName string, chainExpr string) []FieldEntry

// WalkDiagnostics walks a parsed template tree and produces diagnostics for
// unknown variable references, respecting scope changes from range/with blocks.
//
// typeMap maps top-level variable names to their type strings (e.g. "Posts" → "[]db.Post").
// resolver lazily resolves a type name to its fields. Both can be nil for basic checking.
func WalkDiagnostics(tree *parse.Tree, body string, exportedNames map[string]bool, typeMap map[string]string, resolver FieldResolver) []Diagnostic {
	if tree == nil || tree.Root == nil {
		return nil
	}

	w := &walker{
		body:          body,
		exportedNames: exportedNames,
		typeMap:       typeMap,
		resolver:      resolver,
	}
	root := walkScope{depth: 0}
	w.walkList(tree.Root, root)
	return w.diags
}

type walker struct {
	body          string
	exportedNames map[string]bool
	typeMap       map[string]string // top-level var → type string
	resolver      FieldResolver
	diags         []Diagnostic
}

// walkScope tracks the current template scope during AST walking.
type walkScope struct {
	depth         int
	allowedFields map[string]FieldEntry // field name → entry; nil = no type info
	typeName      string                // element type for error messages (e.g. "db.Post")
	chainExpr     string                // Go expression to reach this type (e.g. "Posts[0]")
}

func (w *walker) walkList(list *parse.ListNode, scope walkScope) {
	if list == nil {
		return
	}
	for _, node := range list.Nodes {
		w.walkNode(node, scope)
	}
}

func (w *walker) walkNode(node parse.Node, scope walkScope) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *parse.ListNode:
		w.walkList(n, scope)

	case *parse.ActionNode:
		w.walkPipe(n.Pipe, scope)

	case *parse.RangeNode:
		w.walkPipe(n.Pipe, scope)
		innerScope := w.resolveRangeScope(n.Pipe, scope)
		w.walkList(n.List, innerScope)
		// Else branch does NOT rebind dot — it runs when the collection
		// is empty, so the outer dot binding is preserved.
		w.walkList(n.ElseList, scope)

	case *parse.WithNode:
		w.walkPipe(n.Pipe, scope)
		innerScope := w.resolveWithScope(n.Pipe, scope)
		w.walkList(n.List, innerScope)
		// Else branch does NOT rebind dot — it runs when the value is
		// falsy, so the outer dot binding is preserved.
		w.walkList(n.ElseList, scope)

	case *parse.IfNode:
		w.walkPipe(n.Pipe, scope)
		w.walkList(n.List, scope)
		w.walkList(n.ElseList, scope)

	case *parse.TemplateNode:
		w.walkPipe(n.Pipe, scope)

	case *parse.PipeNode:
		w.walkPipe(n, scope)

	case *parse.FieldNode:
		w.checkField(n, scope)

	case *parse.VariableNode:
		w.checkVariable(n, scope)

	case *parse.CommandNode:
		w.walkCommand(n, scope)

	case *parse.ChainNode:
		w.walkNode(n.Node, scope)
	}
}

func (w *walker) walkPipe(pipe *parse.PipeNode, scope walkScope) {
	if pipe == nil {
		return
	}
	for _, cmd := range pipe.Cmds {
		w.walkCommand(cmd, scope)
	}
}

func (w *walker) walkCommand(cmd *parse.CommandNode, scope walkScope) {
	if cmd == nil {
		return
	}
	for _, arg := range cmd.Args {
		w.walkNode(arg, scope)
	}
}

// resolveRangeScope builds the scope for a range block body by resolving the
// element type of the range target variable.
func (w *walker) resolveRangeScope(pipe *parse.PipeNode, outer walkScope) walkScope {
	inner := walkScope{depth: outer.depth + 1}

	rangeVar := pipeFieldName(pipe)
	if rangeVar == "" {
		return inner
	}

	var containerType, chainBase string

	if outer.depth == 0 {
		// Range on a top-level variable
		if w.typeMap != nil {
			containerType = w.typeMap[rangeVar]
			chainBase = rangeVar
		}
	} else if outer.allowedFields != nil {
		// Range on an element field (nested range)
		if entry, ok := outer.allowedFields[rangeVar]; ok {
			containerType = entry.Type
			if outer.chainExpr != "" {
				chainBase = outer.chainExpr + "." + rangeVar
			}
		}
	}

	if containerType == "" || w.resolver == nil {
		return inner
	}

	elemType := ElementTypeFromContainer(containerType)
	if elemType == "" {
		return inner
	}

	queryType := strings.TrimPrefix(elemType, "*")
	chainExpr := chainBase + "[0]"

	fields := w.resolver(queryType, chainExpr)
	if fields == nil {
		return inner
	}

	inner.allowedFields = fieldEntryMap(fields)
	inner.typeName = elemType
	inner.chainExpr = chainExpr
	return inner
}

// resolveWithScope builds the scope for a with block body by resolving the
// type of the with target variable.
func (w *walker) resolveWithScope(pipe *parse.PipeNode, outer walkScope) walkScope {
	inner := walkScope{depth: outer.depth + 1}

	withVar := pipeFieldName(pipe)
	if withVar == "" {
		return inner
	}

	var varType, chainBase string

	if outer.depth == 0 {
		if w.typeMap != nil {
			varType = w.typeMap[withVar]
			chainBase = withVar
		}
	} else if outer.allowedFields != nil {
		if entry, ok := outer.allowedFields[withVar]; ok {
			varType = entry.Type
			if outer.chainExpr != "" {
				chainBase = outer.chainExpr + "." + withVar
			}
		}
	}

	if varType == "" || w.resolver == nil {
		return inner
	}

	// With doesn't extract element type — the dot becomes the value itself.
	// But strip pointer for field resolution.
	queryType := strings.TrimPrefix(varType, "*")
	chainExpr := chainBase

	fields := w.resolver(queryType, chainExpr)
	if fields == nil {
		return inner
	}

	inner.allowedFields = fieldEntryMap(fields)
	inner.typeName = varType
	inner.chainExpr = chainExpr
	return inner
}

// fieldEntryMap converts a slice of FieldEntry to a map keyed by name.
func fieldEntryMap(entries []FieldEntry) map[string]FieldEntry {
	m := make(map[string]FieldEntry, len(entries))
	for _, e := range entries {
		m[e.Name] = e
	}
	return m
}

// checkField handles .FieldName and .Field.SubField nodes.
func (w *walker) checkField(n *parse.FieldNode, scope walkScope) {
	if len(n.Ident) == 0 {
		return
	}

	fieldName := n.Ident[0]

	if scope.depth == 0 {
		// Top-level: check against exported frontmatter names
		if !w.exportedNames[fieldName] {
			w.emitFieldDiag(n, fieldName, "")
			return
		}
		// Check chained sub-fields if type info is available
		w.checkChainedFields(n, 1, fieldName)
	} else {
		// Inside range/with: check against allowed fields
		if scope.allowedFields == nil {
			return // no type info — skip silently
		}
		entry, ok := scope.allowedFields[fieldName]
		if !ok {
			w.emitFieldDiag(n, fieldName, scope.typeName)
			return
		}
		// Check chained sub-fields using the field's type
		w.checkChainedFieldsWithType(n, 1, entry.Type, scope.chainExpr+"."+fieldName)
	}
}

// checkChainedFields validates sub-fields of a top-level variable (e.g. .Post.Title).
func (w *walker) checkChainedFields(n *parse.FieldNode, startIdx int, rootVar string) {
	if w.typeMap == nil || w.resolver == nil || startIdx >= len(n.Ident) {
		return
	}

	varType, ok := w.typeMap[rootVar]
	if !ok {
		return
	}

	queryType := strings.TrimPrefix(varType, "*")
	chainExpr := rootVar

	for i := startIdx; i < len(n.Ident); i++ {
		fields := w.resolver(queryType, chainExpr)
		if fields == nil {
			return // can't resolve further — stop checking
		}

		fieldMap := fieldEntryMap(fields)
		subField := n.Ident[i]
		entry, ok := fieldMap[subField]
		if !ok {
			w.emitChainedFieldDiag(n, i, queryType)
			return
		}

		queryType = strings.TrimPrefix(entry.Type, "*")
		chainExpr = chainExpr + "." + subField
	}
}

// checkChainedFieldsWithType validates sub-fields starting from a known type.
func (w *walker) checkChainedFieldsWithType(n *parse.FieldNode, startIdx int, currentType, currentChain string) {
	if w.resolver == nil || startIdx >= len(n.Ident) || currentType == "" {
		return
	}

	queryType := strings.TrimPrefix(currentType, "*")

	for i := startIdx; i < len(n.Ident); i++ {
		fields := w.resolver(queryType, currentChain)
		if fields == nil {
			return
		}

		fieldMap := fieldEntryMap(fields)
		subField := n.Ident[i]
		entry, ok := fieldMap[subField]
		if !ok {
			w.emitChainedFieldDiag(n, i, queryType)
			return
		}

		queryType = strings.TrimPrefix(entry.Type, "*")
		currentChain = currentChain + "." + subField
	}
}

// emitFieldDiag emits a diagnostic for an unknown field at position 0 of a FieldNode.
func (w *walker) emitFieldDiag(n *parse.FieldNode, fieldName, typeName string) {
	offset := int(n.Pos)
	startLine, startChar := OffsetToLineChar(w.body, offset)
	endOffset := offset + 1 + len(fieldName)
	endLine, endChar := OffsetToLineChar(w.body, endOffset)

	msg := fmt.Sprintf("unknown template variable %q", "."+fieldName)
	if typeName != "" {
		msg = fmt.Sprintf("unknown field %q on type %q", "."+fieldName, typeName)
	}

	w.diags = append(w.diags, Diagnostic{
		StartLine: startLine, StartChar: startChar,
		EndLine: endLine, EndChar: endChar,
		Message: msg,
	})
}

// emitChainedFieldDiag emits a diagnostic for an unknown sub-field in a chain.
func (w *walker) emitChainedFieldDiag(n *parse.FieldNode, identIdx int, parentType string) {
	subField := n.Ident[identIdx]
	// Calculate the offset: Pos is the initial dot, then each ident has a dot + name
	offset := int(n.Pos)
	for i := 0; i < identIdx; i++ {
		offset += 1 + len(n.Ident[i]) // dot + identifier
	}
	// offset now points to the dot before this sub-field
	startLine, startChar := OffsetToLineChar(w.body, offset)
	endOffset := offset + 1 + len(subField)
	endLine, endChar := OffsetToLineChar(w.body, endOffset)

	w.diags = append(w.diags, Diagnostic{
		StartLine: startLine, StartChar: startChar,
		EndLine: endLine, EndChar: endChar,
		Message: fmt.Sprintf("unknown field %q on type %q", "."+subField, parentType),
	})
}

// checkVariable handles $-prefixed variables. $.FieldName always refers to the
// root data, so it is checked against exports regardless of depth.
func (w *walker) checkVariable(n *parse.VariableNode, scope walkScope) {
	if len(n.Ident) < 2 || n.Ident[0] != "$" {
		return
	}

	fieldName := n.Ident[1]
	if !w.exportedNames[fieldName] {
		offset := int(n.Pos) - 1 // Pos is at '.', '$' is one before
		startLine, startChar := OffsetToLineChar(w.body, offset)
		endOffset := int(n.Pos) + 1 + len(fieldName)
		endLine, endChar := OffsetToLineChar(w.body, endOffset)

		w.diags = append(w.diags, Diagnostic{
			StartLine: startLine, StartChar: startChar,
			EndLine: endLine, EndChar: endChar,
			Message: fmt.Sprintf("unknown template variable %q", "."+fieldName),
		})
		return
	}

	// Check chained sub-fields: $.Post.Title
	if w.typeMap != nil && w.resolver != nil && len(n.Ident) > 2 {
		varType, ok := w.typeMap[fieldName]
		if !ok {
			return
		}
		queryType := strings.TrimPrefix(varType, "*")
		chainExpr := fieldName

		for i := 2; i < len(n.Ident); i++ {
			fields := w.resolver(queryType, chainExpr)
			if fields == nil {
				return
			}
			fm := fieldEntryMap(fields)
			subField := n.Ident[i]
			entry, ok := fm[subField]
			if !ok {
				// Calculate offset for the sub-field in the $.A.B.C chain
				subOffset := int(n.Pos) // starts at '.' after '$'
				for j := 1; j < i; j++ {
					subOffset += 1 + len(n.Ident[j]) // dot + ident
				}
				startLine, startChar := OffsetToLineChar(w.body, subOffset)
				endOffset := subOffset + 1 + len(subField)
				endLine, endChar := OffsetToLineChar(w.body, endOffset)

				w.diags = append(w.diags, Diagnostic{
					StartLine: startLine, StartChar: startChar,
					EndLine: endLine, EndChar: endChar,
					Message: fmt.Sprintf("unknown field %q on type %q", "."+subField, queryType),
				})
				return
			}
			queryType = strings.TrimPrefix(entry.Type, "*")
			chainExpr = chainExpr + "." + subField
		}
	}
}

// ScopeInfo describes the cursor's position within the template AST.
type ScopeInfo struct {
	Depth    int    // 0 = top-level, >0 = inside range/with
	RangeVar string // the variable being ranged/with'd (e.g. "Posts" from .Posts)
}

// CursorScope walks the template AST to determine what scope the cursor is in.
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
		if nodeContainsCursor(n.List, cursor) {
			return cursorScopeList(n.List, cursor, current)
		}
		if nodeContainsCursor(n.ElseList, cursor) {
			return cursorScopeList(n.ElseList, cursor, current)
		}
	}

	return current
}

func nodeContainsCursor(list *parse.ListNode, cursor int) bool {
	if list == nil || len(list.Nodes) == 0 {
		return false
	}
	first := list.Nodes[0]
	last := list.Nodes[len(list.Nodes)-1]
	return cursor >= int(first.Position()) && cursor <= int(last.Position())+nodeLen(last)
}

func nodeLen(node parse.Node) int {
	if tn, ok := node.(*parse.TextNode); ok {
		return len(tn.Text)
	}
	return len(node.String())
}

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
// byte offset within the template body.
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
		if len(n.Ident) > 0 {
			start := int(n.Pos)
			end := start + 1 + len(n.Ident[0])
			if cursor >= start && cursor < end {
				return &HoverTarget{Kind: "field", Name: n.Ident[0], Pos: start, EndPos: end}
			}
		}
	case *parse.VariableNode:
		if len(n.Ident) >= 2 && n.Ident[0] == "$" {
			start := int(n.Pos) - 1
			end := int(n.Pos) + 1 + len(n.Ident[1])
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
