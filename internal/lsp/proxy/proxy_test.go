package proxy_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/lsp/sourcemap"
)

func TestMapPosition_GastroToVirtual(t *testing.T) {
	sm := sourcemap.New(2, 5) // gastro fm at line 2, virtual fm at line 5

	pos := proxy.Position{Line: 3, Character: 10} // gastro line 4 (0-indexed 3)
	mapped := proxy.MapPositionToVirtual(pos, sm)

	// gastro line 4 (1-indexed) = 0-indexed 3
	// virtual offset: 4 - 2 + 5 = 7 (1-indexed) = 6 (0-indexed)
	if mapped.Line != 6 {
		t.Errorf("mapped line: got %d, want 6", mapped.Line)
	}
	if mapped.Character != 10 {
		t.Errorf("character should be unchanged: got %d, want 10", mapped.Character)
	}
}

func TestMapPosition_VirtualToGastro(t *testing.T) {
	sm := sourcemap.New(2, 5) // gastro fm at line 2, virtual fm at line 5

	pos := proxy.Position{Line: 6, Character: 5} // virtual line 7 (0-indexed 6)
	mapped := proxy.MapPositionToGastro(pos, sm)

	// virtual line 7 (1-indexed) -> gastro line 7 - 5 + 2 = 4 (1-indexed) = 3 (0-indexed)
	if mapped.Line != 3 {
		t.Errorf("mapped line: got %d, want 3", mapped.Line)
	}
	if mapped.Character != 5 {
		t.Errorf("character should be unchanged: got %d, want 5", mapped.Character)
	}
}

func TestMapPosition_Roundtrip(t *testing.T) {
	sm := sourcemap.New(2, 5)

	original := proxy.Position{Line: 4, Character: 8}
	virtual := proxy.MapPositionToVirtual(original, sm)
	back := proxy.MapPositionToGastro(virtual, sm)

	if back.Line != original.Line || back.Character != original.Character {
		t.Errorf("roundtrip failed: %v -> %v -> %v", original, virtual, back)
	}
}

func TestRewriteURI_GastroToVirtual(t *testing.T) {
	gastroURI := "file:///Users/me/project/pages/index.gastro"
	virtualPath := "/tmp/shadow/gastro_pages_index/main.go"

	got := proxy.RewriteURIToVirtual(gastroURI, virtualPath)
	want := "file:///tmp/shadow/gastro_pages_index/main.go"

	if got != want {
		t.Errorf("rewrite URI:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRewriteURI_VirtualToGastro(t *testing.T) {
	virtualURI := "file:///tmp/shadow/gastro_pages_index/main.go"
	gastroURI := "file:///Users/me/project/pages/index.gastro"

	got := proxy.RewriteURIToGastro(virtualURI, gastroURI)

	if got != gastroURI {
		t.Errorf("rewrite URI:\ngot:  %q\nwant: %q", got, gastroURI)
	}
}

func TestBackoff_Increases(t *testing.T) {
	b := proxy.NewBackoff(1*time.Second, 30*time.Second)

	d1 := b.Next()
	d2 := b.Next()
	d3 := b.Next()

	if d1 != 1*time.Second {
		t.Errorf("first backoff: got %v, want 1s", d1)
	}
	if d2 != 2*time.Second {
		t.Errorf("second backoff: got %v, want 2s", d2)
	}
	if d3 != 4*time.Second {
		t.Errorf("third backoff: got %v, want 4s", d3)
	}
}

func TestBackoff_CapsAtMax(t *testing.T) {
	b := proxy.NewBackoff(1*time.Second, 5*time.Second)

	b.Next()      // 1s
	b.Next()      // 2s
	b.Next()      // 4s
	d := b.Next() // should cap at 5s

	if d != 5*time.Second {
		t.Errorf("backoff should cap at max: got %v, want 5s", d)
	}
}

func TestBackoff_ResetRestores(t *testing.T) {
	b := proxy.NewBackoff(1*time.Second, 30*time.Second)

	b.Next() // 1s
	b.Next() // 2s
	b.Reset()
	d := b.Next() // should be 1s again

	if d != 1*time.Second {
		t.Errorf("after reset: got %v, want 1s", d)
	}
}

func TestRemapCompletionPositions(t *testing.T) {
	// Source map: gastro frontmatter starts at line 2, virtual at line 16
	// So virtual line 16 = gastro line 2, offset = 14
	sm := sourcemap.New(2, 16)

	tests := []struct {
		name     string
		input    string
		wantLine int // expected line in the first textEdit.range.start after remapping
		wantChar int // expected character (should be unchanged)
	}{
		{
			name: "CompletionList with textEdit",
			input: `{
				"isIncomplete": false,
				"items": [{
					"label": "posts",
					"textEdit": {
						"range": {
							"start": {"line": 20, "character": 9},
							"end": {"line": 20, "character": 13}
						},
						"newText": "posts"
					}
				}]
			}`,
			wantLine: 6, // virtual 21 (1-indexed) -> gastro 2 + (21 - 16) = 7 (1-indexed) = 6 (0-indexed)
			wantChar: 9,
		},
		{
			name: "plain CompletionItem array",
			input: `[{
				"label": "myVar",
				"textEdit": {
					"range": {
						"start": {"line": 18, "character": 5},
						"end": {"line": 18, "character": 10}
					},
					"newText": "myVar"
				}
			}]`,
			wantLine: 4, // virtual 19 (1-indexed) -> gastro 2 + (19 - 16) = 5 (1-indexed) = 4 (0-indexed)
			wantChar: 5,
		},
		{
			name:     "item without textEdit passes through",
			input:    `{"isIncomplete": false, "items": [{"label": "foo"}]}`,
			wantLine: -1, // no textEdit to check
		},
		{
			name:     "null input passes through",
			input:    `null`,
			wantLine: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(tt.input)
			result := proxy.RemapCompletionPositions(raw, sm)

			if tt.wantLine == -1 {
				// Just verify it doesn't panic and returns valid JSON
				if !json.Valid(result) {
					t.Errorf("result is not valid JSON: %s", result)
				}
				return
			}

			// Parse the result and verify the textEdit range was remapped
			var parsed any
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatalf("failed to parse result: %v", err)
			}

			// Extract first item's textEdit
			var items []any
			switch v := parsed.(type) {
			case []any:
				items = v
			case map[string]any:
				items, _ = v["items"].([]any)
			}

			if len(items) == 0 {
				t.Fatal("expected at least one completion item")
			}

			item := items[0].(map[string]any)
			te, ok := item["textEdit"].(map[string]any)
			if !ok {
				t.Fatal("expected textEdit in completion item")
			}
			r := te["range"].(map[string]any)
			start := r["start"].(map[string]any)
			gotLine := int(start["line"].(float64))
			gotChar := int(start["character"].(float64))

			if gotLine != tt.wantLine {
				t.Errorf("textEdit.range.start.line = %d, want %d", gotLine, tt.wantLine)
			}
			if gotChar != tt.wantChar {
				t.Errorf("textEdit.range.start.character = %d, want %d", gotChar, tt.wantChar)
			}
		})
	}
}

func TestRemapCompletionPositions_AdditionalTextEdits(t *testing.T) {
	sm := sourcemap.New(2, 16)

	// Completion with additionalTextEdits (e.g., auto-import)
	input := `{
		"isIncomplete": false,
		"items": [{
			"label": "Println",
			"textEdit": {
				"range": {
					"start": {"line": 18, "character": 4},
					"end": {"line": 18, "character": 8}
				},
				"newText": "Println"
			},
			"additionalTextEdits": [{
				"range": {
					"start": {"line": 17, "character": 0},
					"end": {"line": 17, "character": 0}
				},
				"newText": "import \"fmt\"\n"
			}]
		}]
	}`

	result := proxy.RemapCompletionPositions(json.RawMessage(input), sm)

	var parsed map[string]any
	json.Unmarshal(result, &parsed)
	items := parsed["items"].([]any)
	item := items[0].(map[string]any)
	ates := item["additionalTextEdits"].([]any)
	ate := ates[0].(map[string]any)
	r := ate["range"].(map[string]any)
	start := r["start"].(map[string]any)
	gotLine := int(start["line"].(float64))

	// Virtual line 18 (0-indexed) = 19 (1-indexed) -> gastro 2 + (19 - 16) = 5 (1-indexed) = 4 (0-indexed) -- wait
	// Virtual line 17 (0-indexed) = 18 (1-indexed) -> gastro 2 + (18 - 16) = 4 (1-indexed) = 3 (0-indexed)
	if gotLine != 3 {
		t.Errorf("additionalTextEdits.range.start.line = %d, want 3", gotLine)
	}
}

func TestRemapHoverRange(t *testing.T) {
	sm := sourcemap.New(2, 16)

	tests := []struct {
		name     string
		input    string
		wantLine int // expected range.start.line after remapping, -1 to skip check
	}{
		{
			name: "hover with range",
			input: `{
				"contents": {"kind": "markdown", "value": "func Println(a ...any)"},
				"range": {
					"start": {"line": 18, "character": 4},
					"end": {"line": 18, "character": 11}
				}
			}`,
			wantLine: 4, // virtual 19 (1-indexed) -> gastro 5 (1-indexed) = 4 (0-indexed)
		},
		{
			name:     "hover without range",
			input:    `{"contents": {"kind": "markdown", "value": "some type"}}`,
			wantLine: -1,
		},
		{
			name:     "null input",
			input:    `null`,
			wantLine: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(tt.input)
			result := proxy.RemapHoverRange(raw, sm)

			if tt.wantLine == -1 {
				if !json.Valid(result) {
					t.Errorf("result is not valid JSON: %s", result)
				}
				return
			}

			var parsed map[string]any
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatalf("failed to parse result: %v", err)
			}

			r := parsed["range"].(map[string]any)
			start := r["start"].(map[string]any)
			gotLine := int(start["line"].(float64))

			if gotLine != tt.wantLine {
				t.Errorf("range.start.line = %d, want %d", gotLine, tt.wantLine)
			}
		})
	}
}

func TestRemapDefinitionResult_Location(t *testing.T) {
	sm := sourcemap.New(2, 5) // gastro fm at line 2, virtual fm at line 5

	checker := func(uri string) (string, *sourcemap.SourceMap) {
		if uri == "file:///tmp/shadow/gastro_pages_index/main.go" {
			return "file:///project/pages/index.gastro", sm
		}
		return "", nil
	}

	raw := json.RawMessage(`{
		"uri": "file:///tmp/shadow/gastro_pages_index/main.go",
		"range": {
			"start": {"line": 6, "character": 5},
			"end": {"line": 6, "character": 10}
		}
	}`)

	result := proxy.RemapDefinitionResult(raw, checker)

	var loc map[string]any
	if err := json.Unmarshal(result, &loc); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if loc["uri"] != "file:///project/pages/index.gastro" {
		t.Errorf("uri: got %v, want file:///project/pages/index.gastro", loc["uri"])
	}

	r := loc["range"].(map[string]any)
	start := r["start"].(map[string]any)
	gotLine := int(start["line"].(float64))
	if gotLine != 3 {
		t.Errorf("start.line: got %d, want 3", gotLine)
	}
}

func TestRemapDefinitionResult_RealFile(t *testing.T) {
	checker := func(uri string) (string, *sourcemap.SourceMap) {
		// Not a virtual file
		return "", nil
	}

	raw := json.RawMessage(`{
		"uri": "file:///project/db/posts.go",
		"range": {
			"start": {"line": 15, "character": 0},
			"end": {"line": 15, "character": 20}
		}
	}`)

	result := proxy.RemapDefinitionResult(raw, checker)

	var loc map[string]any
	if err := json.Unmarshal(result, &loc); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if loc["uri"] != "file:///project/db/posts.go" {
		t.Errorf("uri should be unchanged: got %v", loc["uri"])
	}

	r := loc["range"].(map[string]any)
	start := r["start"].(map[string]any)
	if int(start["line"].(float64)) != 15 {
		t.Error("line should be unchanged for real files")
	}
}

func TestRemapDefinitionResult_LocationLink(t *testing.T) {
	sm := sourcemap.New(2, 5)

	checker := func(uri string) (string, *sourcemap.SourceMap) {
		if uri == "file:///tmp/shadow/gastro_pages_index/main.go" {
			return "file:///project/pages/index.gastro", sm
		}
		return "", nil
	}

	raw := json.RawMessage(`[{
		"targetUri": "file:///tmp/shadow/gastro_pages_index/main.go",
		"targetRange": {
			"start": {"line": 6, "character": 0},
			"end": {"line": 6, "character": 20}
		},
		"targetSelectionRange": {
			"start": {"line": 6, "character": 5},
			"end": {"line": 6, "character": 10}
		}
	}]`)

	result := proxy.RemapDefinitionResult(raw, checker)

	var locs []map[string]any
	if err := json.Unmarshal(result, &locs); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}

	link := locs[0]
	if link["targetUri"] != "file:///project/pages/index.gastro" {
		t.Errorf("targetUri: got %v, want file:///project/pages/index.gastro", link["targetUri"])
	}

	tr := link["targetRange"].(map[string]any)
	start := tr["start"].(map[string]any)
	if int(start["line"].(float64)) != 3 {
		t.Errorf("targetRange.start.line: got %v, want 3", start["line"])
	}

	tsr := link["targetSelectionRange"].(map[string]any)
	selStart := tsr["start"].(map[string]any)
	if int(selStart["line"].(float64)) != 3 {
		t.Errorf("targetSelectionRange.start.line: got %v, want 3", selStart["line"])
	}
}

func TestRemapDefinitionResult_LocationArray(t *testing.T) {
	sm := sourcemap.New(2, 5)

	checker := func(uri string) (string, *sourcemap.SourceMap) {
		if uri == "file:///tmp/shadow/gastro_pages_index/main.go" {
			return "file:///project/pages/index.gastro", sm
		}
		return "", nil
	}

	raw := json.RawMessage(`[
		{
			"uri": "file:///tmp/shadow/gastro_pages_index/main.go",
			"range": {"start": {"line": 6, "character": 0}, "end": {"line": 6, "character": 10}}
		},
		{
			"uri": "file:///project/db/posts.go",
			"range": {"start": {"line": 20, "character": 0}, "end": {"line": 20, "character": 10}}
		}
	]`)

	result := proxy.RemapDefinitionResult(raw, checker)

	var locs []map[string]any
	if err := json.Unmarshal(result, &locs); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}

	// First location: virtual file -> remapped
	if locs[0]["uri"] != "file:///project/pages/index.gastro" {
		t.Errorf("first uri: got %v, want remapped gastro URI", locs[0]["uri"])
	}

	// Second location: real file -> unchanged
	if locs[1]["uri"] != "file:///project/db/posts.go" {
		t.Errorf("second uri: got %v, want unchanged real file URI", locs[1]["uri"])
	}
	r2 := locs[1]["range"].(map[string]any)
	s2 := r2["start"].(map[string]any)
	if int(s2["line"].(float64)) != 20 {
		t.Error("second location line should be unchanged")
	}
}

func TestRemapDefinitionResult_NullInput(t *testing.T) {
	checker := func(uri string) (string, *sourcemap.SourceMap) { return "", nil }

	result := proxy.RemapDefinitionResult(json.RawMessage("null"), checker)
	if string(result) != "null" {
		t.Errorf("null input should return null, got: %s", result)
	}

	result = proxy.RemapDefinitionResult(json.RawMessage(""), checker)
	if string(result) != "" {
		t.Errorf("empty input should return empty, got: %s", result)
	}
}
