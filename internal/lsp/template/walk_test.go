package template_test

import (
	"testing"

	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

func parseAndWalk(t *testing.T, body string, exportedNames map[string]bool) []lsptemplate.Diagnostic {
	t.Helper()
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("ParseTemplateBody failed: %v", err)
	}
	return lsptemplate.WalkDiagnostics(tree, body, exportedNames, nil, nil)
}

func hasDiagMessage(diags []lsptemplate.Diagnostic, msg string) bool {
	for _, d := range diags {
		if d.Message == msg {
			return true
		}
	}
	return false
}

func TestWalk_TopLevelKnownVar(t *testing.T) {
	exports := map[string]bool{"Title": true}
	diags := parseAndWalk(t, `<h1>{{ .Title }}</h1>`, exports)

	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for known variable, got %d: %v", len(diags), diags)
	}
}

func TestWalk_TopLevelUnknownVar(t *testing.T) {
	exports := map[string]bool{"Title": true}
	diags := parseAndWalk(t, `<p>{{ .Unknown }}</p>`, exports)

	if !hasDiagMessage(diags, `unknown template variable ".Unknown"`) {
		t.Errorf("expected diagnostic for .Unknown, got: %v", diags)
	}
}

func TestWalk_RangeBlockSkipsFieldChecks(t *testing.T) {
	exports := map[string]bool{"Title": true, "Posts": true}
	body := `<h1>{{ .Title }}</h1>
{{ range .Posts }}
<p>{{ .Slug }}</p>
<p>{{ .Author }}</p>
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	// .Slug and .Author inside range should NOT be flagged
	if hasDiagMessage(diags, `unknown template variable ".Slug"`) {
		t.Error(".Slug inside range should not be flagged")
	}
	if hasDiagMessage(diags, `unknown template variable ".Author"`) {
		t.Error(".Author inside range should not be flagged")
	}
	// .Title at top level should pass (it's exported)
	if hasDiagMessage(diags, `unknown template variable ".Title"`) {
		t.Error(".Title should not be flagged — it's exported")
	}
}

func TestWalk_WithBlockSkipsFieldChecks(t *testing.T) {
	exports := map[string]bool{"Author": true}
	body := `{{ with .Author }}
<p>{{ .Name }}</p>
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	if hasDiagMessage(diags, `unknown template variable ".Name"`) {
		t.Error(".Name inside with should not be flagged")
	}
}

func TestWalk_IfBlockDoesNotRebindDot(t *testing.T) {
	exports := map[string]bool{"ShowTitle": true}
	body := `{{ if .ShowTitle }}
<h1>{{ .Missing }}</h1>
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	if !hasDiagMessage(diags, `unknown template variable ".Missing"`) {
		t.Error("expected diagnostic for .Missing inside if (dot is not rebound)")
	}
}

func TestWalk_DollarVarInRangeCheckedAgainstExports(t *testing.T) {
	exports := map[string]bool{"Posts": true}
	body := `{{ range .Posts }}
<p>{{ $.Title }}</p>
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	if !hasDiagMessage(diags, `unknown template variable ".Title"`) {
		t.Error("expected diagnostic for $.Title (not in exports)")
	}
}

func TestWalk_DollarVarKnownExport(t *testing.T) {
	exports := map[string]bool{"Posts": true, "Title": true}
	body := `{{ range .Posts }}
<p>{{ $.Title }}</p>
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	if hasDiagMessage(diags, `unknown template variable ".Title"`) {
		t.Error("$.Title should not be flagged — it's exported")
	}
}

func TestWalk_NestedRange(t *testing.T) {
	exports := map[string]bool{"Categories": true}
	body := `{{ range .Categories }}
{{ range .Items }}
<p>{{ .Name }}</p>
{{ end }}
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	// .Items and .Name are inside rebound scopes — should not be flagged
	if hasDiagMessage(diags, `unknown template variable ".Items"`) {
		t.Error(".Items inside outer range should not be flagged")
	}
	if hasDiagMessage(diags, `unknown template variable ".Name"`) {
		t.Error(".Name inside nested range should not be flagged")
	}
}

func TestWalk_MultipleVarsInPipe(t *testing.T) {
	exports := map[string]bool{"Title": true}
	body := `{{ .Title | upper }}`

	diags := parseAndWalk(t, body, exports)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics, got: %v", diags)
	}
}

func TestWalk_ChainedFieldAccess(t *testing.T) {
	exports := map[string]bool{"Post": true}
	body := `{{ .Post.Title }}`

	diags := parseAndWalk(t, body, exports)

	// .Post is exported, .Title is a field on Post — only Post is checked
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for .Post.Title (Post is exported), got: %v", diags)
	}
}

func TestWalk_ChainedFieldAccessUnknownRoot(t *testing.T) {
	exports := map[string]bool{"Title": true}
	body := `{{ .Unknown.Field }}`

	diags := parseAndWalk(t, body, exports)

	if !hasDiagMessage(diags, `unknown template variable ".Unknown"`) {
		t.Error("expected diagnostic for .Unknown in .Unknown.Field")
	}
}

func TestWalk_EmptyTree(t *testing.T) {
	diags := lsptemplate.WalkDiagnostics(nil, "", nil, nil, nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for nil tree, got %d", len(diags))
	}
}

func TestWalk_RangeElseList(t *testing.T) {
	exports := map[string]bool{"Items": true}
	body := `{{ range .Items }}
<p>{{ .Name }}</p>
{{ else }}
<p>No items</p>
{{ end }}`

	diags := parseAndWalk(t, body, exports)

	// .Name inside range body should not be flagged
	if hasDiagMessage(diags, `unknown template variable ".Name"`) {
		t.Error(".Name inside range should not be flagged")
	}
}

func TestCursorScope_TopLevel(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Cursor at offset 8 — inside {{ .Title }}
	scope := lsptemplate.CursorScope(tree, 8)
	if scope.Depth != 0 {
		t.Errorf("expected depth 0, got %d", scope.Depth)
	}
}

func TestCursorScope_InsideRange(t *testing.T) {
	body := `{{ range .Posts }}
<p>{{ .Slug }}</p>
{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Cursor at offset 25 — inside the range body, on the <p> line
	scope := lsptemplate.CursorScope(tree, 25)
	if scope.Depth != 1 {
		t.Errorf("expected depth 1, got %d", scope.Depth)
	}
	if scope.RangeVar != "Posts" {
		t.Errorf("expected RangeVar='Posts', got %q", scope.RangeVar)
	}
}

func TestCursorScope_InsideNestedRange(t *testing.T) {
	body := `{{ range .Categories }}
{{ range .Items }}
<p>{{ .Name }}</p>
{{ end }}
{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Cursor at offset 50 — inside the inner range
	scope := lsptemplate.CursorScope(tree, 50)
	if scope.Depth != 2 {
		t.Errorf("expected depth 2, got %d", scope.Depth)
	}
	if scope.RangeVar != "Items" {
		t.Errorf("expected RangeVar='Items', got %q", scope.RangeVar)
	}
}

func TestCursorScope_InsideIf(t *testing.T) {
	body := `{{ if .Show }}
<p>{{ .Title }}</p>
{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Cursor inside if body — depth should stay 0 (if doesn't rebind)
	scope := lsptemplate.CursorScope(tree, 20)
	if scope.Depth != 0 {
		t.Errorf("expected depth 0 inside if, got %d", scope.Depth)
	}
}

func TestNodeAtCursor_Field(t *testing.T) {
	// <h1>{{ .Title }}</h1>
	//        ^--- offset 7 is the dot
	body := `<h1>{{ .Title }}</h1>`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	target := lsptemplate.NodeAtCursor(tree, 7)
	if target == nil {
		t.Fatal("expected hover target, got nil")
	}
	if target.Kind != "field" {
		t.Errorf("expected kind='field', got %q", target.Kind)
	}
	if target.Name != "Title" {
		t.Errorf("expected name='Title', got %q", target.Name)
	}
}

func TestNodeAtCursor_FieldMiddle(t *testing.T) {
	// Cursor in the middle of "Title"
	body := `<h1>{{ .Title }}</h1>`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	target := lsptemplate.NodeAtCursor(tree, 10) // on 'l' in Title
	if target == nil {
		t.Fatal("expected hover target, got nil")
	}
	if target.Kind != "field" || target.Name != "Title" {
		t.Errorf("expected field 'Title', got %s %q", target.Kind, target.Name)
	}
}

func TestNodeAtCursor_Function(t *testing.T) {
	// {{ .Title | upper }}
	//             ^--- "upper" starts at offset 12
	body := `{{ .Title | upper }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	target := lsptemplate.NodeAtCursor(tree, 12)
	if target == nil {
		t.Fatal("expected hover target for function, got nil")
	}
	if target.Kind != "function" {
		t.Errorf("expected kind='function', got %q", target.Kind)
	}
	if target.Name != "upper" {
		t.Errorf("expected name='upper', got %q", target.Name)
	}
}

func TestNodeAtCursor_DollarVariable(t *testing.T) {
	body := `{{ range .Items }}{{ $.Title }}{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	// '$' is at byte 21, '.' is at 22. parse.VariableNode.Pos points to the dot (22).
	// Our NodeAtCursor starts the range at Pos-1 to include '$'.
	idx := 21
	target := lsptemplate.NodeAtCursor(tree, idx)
	if target == nil {
		t.Fatal("expected hover target for $ variable, got nil")
	}
	if target.Kind != "variable" {
		t.Errorf("expected kind='variable', got %q", target.Kind)
	}
	if target.Name != "Title" {
		t.Errorf("expected name='Title', got %q", target.Name)
	}
}

func TestNodeAtCursor_HTMLText(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Cursor on "<h1>" — should return nil
	target := lsptemplate.NodeAtCursor(tree, 2)
	if target != nil {
		t.Errorf("expected nil for HTML text, got %+v", target)
	}
}

func TestNodeAtCursor_FieldInsideRange(t *testing.T) {
	body := `{{ range .Posts }}
<p>{{ .Slug }}</p>
{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	// ".Slug" starts at byte 25 (the dot) in the template body
	idx := 25
	target := lsptemplate.NodeAtCursor(tree, idx)
	if target == nil {
		t.Fatal("expected hover target for .Slug inside range, got nil")
	}
	if target.Kind != "field" || target.Name != "Slug" {
		t.Errorf("expected field 'Slug', got %s %q", target.Kind, target.Name)
	}
}

func TestNodeAtCursor_NilTree(t *testing.T) {
	target := lsptemplate.NodeAtCursor(nil, 5)
	if target != nil {
		t.Errorf("expected nil for nil tree, got %+v", target)
	}
}

// TestNodeAtCursor_ChainedFieldSegments is a regression for Phase 4.2.
// Previously NodeAtCursor only matched the *first* segment of a chained
// field reference like `.Agent.Name`, so hovering on `.Name` produced
// no target and the user got no type info. After the rewrite, every
// segment in the chain is reachable and the returned HoverTarget
// carries the full Chain plus ChainIdx so consumers can resolve the
// segment's type via FieldResolver.
func TestNodeAtCursor_ChainedFieldSegments(t *testing.T) {
	body := `<text>{{ .Agent.Name }}</text>`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	// `.Agent` spans bytes 9-15 (the leading dot through 'Agent').
	// `.Name`  spans bytes 15-20 (dot through 'Name').
	tests := []struct {
		cursor   int
		wantSeg  string
		wantIdx  int
		wantPos  int
		wantEnd  int
		wantKind string
	}{
		{cursor: 9, wantSeg: "Agent", wantIdx: 0, wantPos: 9, wantEnd: 15, wantKind: "field"},
		{cursor: 12, wantSeg: "Agent", wantIdx: 0, wantPos: 9, wantEnd: 15, wantKind: "field"},
		{cursor: 14, wantSeg: "Agent", wantIdx: 0, wantPos: 9, wantEnd: 15, wantKind: "field"},
		{cursor: 15, wantSeg: "Name", wantIdx: 1, wantPos: 15, wantEnd: 20, wantKind: "field"},
		{cursor: 17, wantSeg: "Name", wantIdx: 1, wantPos: 15, wantEnd: 20, wantKind: "field"},
		{cursor: 19, wantSeg: "Name", wantIdx: 1, wantPos: 15, wantEnd: 20, wantKind: "field"},
	}

	wantChain := []string{"Agent", "Name"}
	for _, tt := range tests {
		target := lsptemplate.NodeAtCursor(tree, tt.cursor)
		if target == nil {
			t.Errorf("cursor=%d: expected target on chain segment, got nil", tt.cursor)
			continue
		}
		if target.Kind != tt.wantKind || target.Name != tt.wantSeg || target.ChainIdx != tt.wantIdx {
			t.Errorf("cursor=%d: got {Kind=%s Name=%q ChainIdx=%d}, want {Kind=%s Name=%q ChainIdx=%d}",
				tt.cursor, target.Kind, target.Name, target.ChainIdx, tt.wantKind, tt.wantSeg, tt.wantIdx)
		}
		if target.Pos != tt.wantPos || target.EndPos != tt.wantEnd {
			t.Errorf("cursor=%d: span got [%d,%d), want [%d,%d)", tt.cursor, target.Pos, target.EndPos, tt.wantPos, tt.wantEnd)
		}
		if !equalStringSlices(target.Chain, wantChain) {
			t.Errorf("cursor=%d: Chain = %v, want %v", tt.cursor, target.Chain, wantChain)
		}
	}
}

// TestNodeAtCursor_ChainedDollarVariable mirrors the chained-field test
// for $.A.B.C references. Same plumbing, slightly different offset math
// because of the leading '$'.
func TestNodeAtCursor_ChainedDollarVariable(t *testing.T) {
	body := `{{ $.User.Name }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	// `$.User` spans bytes 3-9 ('$' + '.' + 'User').
	// `.Name`  spans bytes 9-14 ('.' + 'Name').
	target := lsptemplate.NodeAtCursor(tree, 5) // inside $.User
	if target == nil || target.Kind != "variable" || target.Name != "User" || target.ChainIdx != 0 {
		t.Fatalf("cursor=5 inside $.User: got %+v", target)
	}
	target = lsptemplate.NodeAtCursor(tree, 11) // inside .Name
	if target == nil || target.Kind != "variable" || target.Name != "Name" || target.ChainIdx != 1 {
		t.Fatalf("cursor=11 inside .Name: got %+v", target)
	}
	want := []string{"User", "Name"}
	if !equalStringSlices(target.Chain, want) {
		t.Errorf("Chain = %v, want %v", target.Chain, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mockResolver returns a resolver that maps type names to field entries.
func mockResolver(types map[string][]lsptemplate.FieldEntry) lsptemplate.FieldResolver {
	return func(typeName string, chainExpr string) []lsptemplate.FieldEntry {
		return types[typeName]
	}
}

func TestWalk_RangeWithFieldInfo_UnknownField(t *testing.T) {
	exports := map[string]bool{"Posts": true}
	typeMap := map[string]string{"Posts": "[]db.Post"}
	resolver := mockResolver(map[string][]lsptemplate.FieldEntry{
		"db.Post": {
			{Name: "Title", Type: "string"},
			{Name: "Slug", Type: "string"},
			{Name: "Author", Type: "string"},
		},
	})

	body := `{{ range .Posts }}{{ .Titl }}{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, typeMap, resolver)

	if !hasDiagMessage(diags, `unknown field ".Titl" on type "db.Post"`) {
		t.Errorf("expected diagnostic for .Titl inside range, got: %v", diags)
	}
}

func TestWalk_RangeWithFieldInfo_KnownField(t *testing.T) {
	exports := map[string]bool{"Posts": true}
	typeMap := map[string]string{"Posts": "[]db.Post"}
	resolver := mockResolver(map[string][]lsptemplate.FieldEntry{
		"db.Post": {
			{Name: "Title", Type: "string"},
			{Name: "Slug", Type: "string"},
		},
	})

	body := `{{ range .Posts }}{{ .Title }}{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, typeMap, resolver)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for known field, got: %v", diags)
	}
}

func TestWalk_NestedRangeWithFieldInfo(t *testing.T) {
	exports := map[string]bool{"Categories": true}
	typeMap := map[string]string{"Categories": "[]db.Category"}
	resolver := mockResolver(map[string][]lsptemplate.FieldEntry{
		"db.Category": {
			{Name: "Name", Type: "string"},
			{Name: "Items", Type: "[]db.Item"},
		},
		"db.Item": {
			{Name: "Label", Type: "string"},
			{Name: "Price", Type: "int"},
		},
	})

	body := `{{ range .Categories }}{{ range .Items }}{{ .Label }}{{ .Missing }}{{ end }}{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, typeMap, resolver)

	if !hasDiagMessage(diags, `unknown field ".Missing" on type "db.Item"`) {
		t.Errorf("expected diagnostic for .Missing in nested range, got: %v", diags)
	}
	if hasDiagMessage(diags, `unknown field ".Label"`) {
		t.Error(".Label should not be flagged — it's a known field on db.Item")
	}
}

func TestWalk_ChainedFieldWithTypeInfo(t *testing.T) {
	exports := map[string]bool{"Post": true}
	typeMap := map[string]string{"Post": "db.Post"}
	resolver := mockResolver(map[string][]lsptemplate.FieldEntry{
		"db.Post": {
			{Name: "Title", Type: "string"},
			{Name: "Author", Type: "db.Author"},
		},
	})

	// .Post.Missing — Post is exported, but Missing is not a field on db.Post
	body := `{{ .Post.Missing }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, typeMap, resolver)

	if !hasDiagMessage(diags, `unknown field ".Missing" on type "db.Post"`) {
		t.Errorf("expected diagnostic for .Post.Missing, got: %v", diags)
	}
}

func TestWalk_ChainedFieldKnown(t *testing.T) {
	exports := map[string]bool{"Post": true}
	typeMap := map[string]string{"Post": "db.Post"}
	resolver := mockResolver(map[string][]lsptemplate.FieldEntry{
		"db.Post": {
			{Name: "Title", Type: "string"},
		},
	})

	body := `{{ .Post.Title }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, typeMap, resolver)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for .Post.Title, got: %v", diags)
	}
}

func TestWalk_RangeElseBranchKeepsOuterScope(t *testing.T) {
	exports := map[string]bool{"Items": true, "Title": true}
	body := `{{ range .Items }}
<p>{{ .Name }}</p>
{{ else }}
<p>{{ .Title }}</p>
<p>{{ .Missing }}</p>
{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, nil, nil)

	// .Title in else branch should be checked against exports (not skipped)
	if hasDiagMessage(diags, `unknown template variable ".Title"`) {
		t.Error(".Title in else branch should not be flagged — it's exported")
	}
	// .Missing in else branch should be flagged
	if !hasDiagMessage(diags, `unknown template variable ".Missing"`) {
		t.Error("expected diagnostic for .Missing in range else branch")
	}
}

func TestWalk_WithFieldInfo(t *testing.T) {
	exports := map[string]bool{"Author": true}
	typeMap := map[string]string{"Author": "db.Author"}
	resolver := mockResolver(map[string][]lsptemplate.FieldEntry{
		"db.Author": {
			{Name: "Name", Type: "string"},
			{Name: "Email", Type: "string"},
		},
	})

	body := `{{ with .Author }}{{ .Name }}{{ .Missing }}{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, typeMap, resolver)

	if hasDiagMessage(diags, `unknown field ".Name"`) {
		t.Error(".Name should not be flagged — it's a known field on db.Author")
	}
	if !hasDiagMessage(diags, `unknown field ".Missing" on type "db.Author"`) {
		t.Errorf("expected diagnostic for .Missing in with block, got: %v", diags)
	}
}

func TestWalk_RangeNoFieldInfo_StillSkips(t *testing.T) {
	exports := map[string]bool{"Posts": true}
	body := `{{ range .Posts }}{{ .Anything }}{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatal(err)
	}

	// No typeMap or resolver — should skip silently
	diags := lsptemplate.WalkDiagnostics(tree, body, exports, nil, nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics without type info, got: %v", diags)
	}
}

func TestWalk_GoBuiltinFunctions(t *testing.T) {
	exports := map[string]bool{"Status": true, "Items": true}
	body := `{{ if eq .Status "active" }}
<p>{{ len .Items }}</p>
{{ end }}`
	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("template with Go builtins should parse: %v", err)
	}

	diags := lsptemplate.WalkDiagnostics(tree, body, exports, nil, nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for template with builtins, got: %v", diags)
	}
}

func TestWalk_PositionAccuracy(t *testing.T) {
	exports := map[string]bool{}
	body := `line one
{{ .Missing }}`

	diags := parseAndWalk(t, body, exports)

	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic")
	}

	d := diags[0]
	if d.StartLine != 1 {
		t.Errorf("expected StartLine=1, got %d", d.StartLine)
	}
	// ".Missing" starts at column 3 (after "{{ ")
	if d.StartChar != 3 {
		t.Errorf("expected StartChar=3, got %d", d.StartChar)
	}
}
