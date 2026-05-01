package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

// listEntry holds the display information for a single .gastro file.
type listEntry struct {
	kind   string // "component" or "page"
	name   string // exported component/page name, e.g. "Card", "Index"
	path   string // relative source path, e.g. "components/card.gastro"
	fields []codegen.StructField
}

// listJSON is the JSON-serialisable shape emitted by gastro list --json.
// It is a strict superset of the human-readable output — every field that
// appears in the table is present here, plus the raw props array that agents
// can iterate without parsing the parenthesised string.
type listJSON struct {
	Kind  string          `json:"kind"`  // "component" or "page"
	Name  string          `json:"name"`  // exported name, e.g. "Card"
	Path  string          `json:"path"`  // relative source path
	Props []listJSONField `json:"props"` // always an array, never null
}

// listJSONField is one entry in the Props array.
type listJSONField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// runList discovers all .gastro files in the current project and prints each
// one with its name and Props fields (for components).
//
// With --json the output is a JSON array of objects:
//
//	[{"kind":"component","name":"Card","path":"components/card.gastro","props":[{"name":"Title","type":"string"}]},...]
//
// Without --json (default) the output is an aligned human-readable table:
//
//	[component]  Card (Title string, Body string)  components/card.gastro
//	[page]       Index                              pages/index.gastro
func runList() error {
	args := os.Args[2:]
	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true
		}
	}

	projectDir := "."

	var entries []listEntry

	for _, dir := range []string{"components", "pages"} {
		abs := filepath.Join(projectDir, dir)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			continue
		}

		files, err := collectGastroFiles(abs)
		if err != nil {
			return fmt.Errorf("listing %s: %w", dir, err)
		}

		for _, absPath := range files {
			relPath, err := filepath.Rel(projectDir, absPath)
			if err != nil {
				relPath = absPath
			}

			entry, err := buildListEntry(relPath, absPath)
			if err != nil {
				// Don't abort on a single bad file; just show the path.
				entry = listEntry{
					kind: dir[:len(dir)-1], // "components" -> "component"
					path: relPath,
					name: codegen.ExportedComponentName(codegen.HandlerFuncName(relPath)),
				}
			}
			entries = append(entries, entry)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			// components before pages
			return entries[i].kind < entries[j].kind
		}
		return entries[i].path < entries[j].path
	})

	if jsonMode {
		return printListJSON(entries)
	}
	return printListTable(entries)
}

// printListJSON writes a JSON array of listJSON objects to stdout.
// An empty project produces an empty array `[]`, never null.
func printListJSON(entries []listEntry) error {
	out := make([]listJSON, 0, len(entries))
	for _, e := range entries {
		fields := make([]listJSONField, 0, len(e.fields))
		for _, f := range e.fields {
			fields = append(fields, listJSONField{Name: f.Name, Type: f.Type})
		}
		out = append(out, listJSON{
			Kind:  e.kind,
			Name:  e.name,
			Path:  e.path,
			Props: fields,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// printListTable writes the aligned human-readable table to stdout.
func printListTable(entries []listEntry) error {
	if len(entries) == 0 {
		fmt.Println("gastro list: no .gastro files found")
		return nil
	}

	// Calculate column widths for alignment.
	maxKind := len("[component]")
	maxName := 0
	for _, e := range entries {
		label := "[" + e.kind + "]"
		if len(label) > maxKind {
			maxKind = len(label)
		}
		display := e.name + formatFields(e.fields)
		if len(display) > maxName {
			maxName = len(display)
		}
	}

	for _, e := range entries {
		label := "[" + e.kind + "]"
		display := e.name + formatFields(e.fields)
		fmt.Printf("%-*s  %-*s  %s\n", maxKind, label, maxName, display, e.path)
	}

	return nil
}

// buildListEntry parses a single .gastro file and returns a listEntry.
func buildListEntry(relPath, absPath string) (listEntry, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return listEntry{}, err
	}

	file, err := parser.Parse(relPath, string(content))
	if err != nil {
		return listEntry{}, err
	}

	info, err := codegen.AnalyzeFrontmatter(file.Frontmatter)
	if err != nil {
		return listEntry{}, err
	}

	funcName := codegen.HandlerFuncName(relPath)

	kind := "page"
	if strings.HasPrefix(relPath, "components/") {
		kind = "component"
	}

	// Derive display name by stripping the kind-specific prefix that
	// HandlerFuncName prepends ("component" or "page").
	name := codegen.ExportedComponentName(funcName)
	if kind == "page" {
		// ExportedComponentName only strips "component"; strip "page" for pages.
		name = strings.TrimPrefix(funcName, "page")
	}

	var fields []codegen.StructField
	if info.PropsTypeName != "" {
		_, hoisted := codegen.HoistTypeDeclarations(file.Frontmatter)
		fields = codegen.ParseStructFields(hoisted)
	}

	return listEntry{
		kind:   kind,
		name:   name,
		path:   relPath,
		fields: fields,
	}, nil
}

// formatFields returns a parenthesised, comma-separated props summary.
// Returns an empty string when there are no fields.
func formatFields(fields []codegen.StructField) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f.Name+" "+f.Type)
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
