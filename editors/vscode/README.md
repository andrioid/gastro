# Gastro for Visual Studio Code

Language support for [Gastro](https://github.com/andrioid/gastro) `.gastro` files.

## Features

### Syntax Highlighting

Go frontmatter and HTML/template body are highlighted with full embedded language support.

### Completions

- **Frontmatter**: Go completions powered by gopls (variables, types, imports, functions)
- **Template variables**: `{{ .Title }}` completions based on frontmatter exports
- **Component names**: completing a component inserts a full `(dict ...)` skeleton with tabstops for each prop
- **Component auto-import**: un-imported components from `components/` are offered with automatic import insertion
- **Component props**: inside a component call, props are suggested with type information
- **Template functions**: built-in and custom template functions (`upper`, `lower`, `dict`, `timeFormat`, etc.)

### Hover Information

- Variable types from frontmatter
- Function signatures
- Component Props struct fields

### Go to Definition

Jump to component source files from component calls in templates.

### Diagnostics

- Unknown template variables
- Unknown components (not imported)
- Invalid component props (unknown or missing)
- Go frontmatter errors via gopls
- Double-dot syntax errors (`..Title` instead of `.Title`)

### Document Formatting

Format `.gastro` files using the built-in formatter (same as `gastro fmt`). Works with VS Code's "Format Document" command and `editor.formatOnSave`.

### Version Check

The extension warns when the installed `gastro` binary version doesn't match the extension version, which can cause features to silently not work.

## Requirements

- **`gastro` binary** on your PATH (or configured via the `gastro.lspPath` setting)
  - Install: `go install github.com/andrioid/gastro/cmd/gastro@latest`
  - Or: `mise use github:andrioid/gastro@latest`
- **`gopls`** (optional, recommended) for Go intelligence in the frontmatter
  - Install: `go install golang.org/x/tools/gopls@latest`

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `gastro.lspPath` | `""` | Absolute path to the gastro binary. Leave empty to find it on PATH. |
| `gastro-lsp.trace.server` | `"off"` | Traces the communication between VS Code and the Gastro Language Server. Set to `"verbose"` for debugging. |
