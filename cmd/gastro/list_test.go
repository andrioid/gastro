package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestBuildListEntry_Component(t *testing.T) {
	dir := t.TempDir()
	compDir := filepath.Join(dir, "components")
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "---\ntype Props struct { Title string; Count int }\np := gastro.Props()\nTitle := p.Title\n---\n<div>{{.Title}}</div>\n"
	absPath := filepath.Join(compDir, "card.gastro")
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := buildListEntry("components/card.gastro", absPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.kind != "component" {
		t.Errorf("kind = %q, want %q", entry.kind, "component")
	}
	if entry.name != "Card" {
		t.Errorf("name = %q, want %q", entry.name, "Card")
	}
	if len(entry.fields) != 2 {
		t.Errorf("expected 2 props fields, got %d: %v", len(entry.fields), entry.fields)
	}
}

func TestBuildListEntry_Page(t *testing.T) {
	dir := t.TempDir()
	pagesDir := filepath.Join(dir, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "---\nctx := gastro.Context()\n---\n<h1>hello</h1>\n"
	absPath := filepath.Join(pagesDir, "index.gastro")
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := buildListEntry("pages/index.gastro", absPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.kind != "page" {
		t.Errorf("kind = %q, want %q", entry.kind, "page")
	}
	if entry.name != "Index" {
		t.Errorf("name = %q, want %q", entry.name, "Index")
	}
	if len(entry.fields) != 0 {
		t.Errorf("pages should have no fields, got %d", len(entry.fields))
	}
}

func TestFormatFields_Empty(t *testing.T) {
	got := formatFields(nil)
	if got != "" {
		t.Errorf("formatFields(nil) = %q, want empty string", got)
	}
}

func TestPrintListJSON_EmptyProducesArray(t *testing.T) {
	// Capture stdout.
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	err := printListJSON(nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf strings.Builder
	io.Copy(&buf, r) //nolint:errcheck

	var got []listJSON
	if err := json.Unmarshal([]byte(buf.String()), &got); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if got == nil {
		t.Error("expected non-nil (empty) array, got null")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestPrintListJSON_ContainsAllFields(t *testing.T) {
	entries := []listEntry{
		{
			kind: "component",
			name: "Card",
			path: "components/card.gastro",
			fields: []codegen.StructField{
				{Name: "Title", Type: "string"},
				{Name: "Count", Type: "int"},
			},
		},
		{
			kind:   "page",
			name:   "Index",
			path:   "pages/index.gastro",
			fields: nil,
		},
	}

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	err := printListJSON(entries)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf strings.Builder
	io.Copy(&buf, r) //nolint:errcheck

	var got []listJSON
	if err := json.Unmarshal([]byte(buf.String()), &got); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}

	card := got[0]
	if card.Kind != "component" {
		t.Errorf("kind = %q, want %q", card.Kind, "component")
	}
	if card.Name != "Card" {
		t.Errorf("name = %q, want %q", card.Name, "Card")
	}
	if card.Path != "components/card.gastro" {
		t.Errorf("path = %q, want %q", card.Path, "components/card.gastro")
	}
	if len(card.Props) != 2 {
		t.Fatalf("expected 2 props, got %d", len(card.Props))
	}
	if card.Props[0].Name != "Title" || card.Props[0].Type != "string" {
		t.Errorf("props[0] = %+v, want {Title string}", card.Props[0])
	}

	page := got[1]
	if page.Kind != "page" {
		t.Errorf("page kind = %q, want %q", page.Kind, "page")
	}
	// Pages have no props but the field must be an empty array, not null.
	raw, _ := json.Marshal(page)
	if !strings.Contains(string(raw), `"props":[]`) {
		t.Errorf("expected props:[] in JSON, got: %s", raw)
	}
}

func TestFormatFields_WithFields(t *testing.T) {
	fields := []codegen.StructField{
		{Name: "Title", Type: "string"},
		{Name: "Count", Type: "int"},
	}
	got := formatFields(fields)
	if !strings.HasPrefix(got, " (") || !strings.HasSuffix(got, ")") {
		t.Errorf("formatFields returned unexpected format: %q", got)
	}
	if !strings.Contains(got, "Title string") {
		t.Errorf("expected 'Title string' in %q", got)
	}
	if !strings.Contains(got, "Count int") {
		t.Errorf("expected 'Count int' in %q", got)
	}
}
