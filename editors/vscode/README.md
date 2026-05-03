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

The extension warns when the LSP version doesn't match the extension version, which can cause features to silently not work. The warning text adapts based on how the LSP was launched: if it's pinned via `go tool` in your project's `go.mod`, the message points you at `go get -tool ...@latest`; otherwise it points you at `go install`.

## How the LSP is launched

The extension picks the gastro LSP binary in this order:

1. **`gastro.lspPath` setting** -- if set, the extension launches that exact binary (with no extra arguments).
2. **`go tool gastro lsp`** -- if the workspace's `go.mod` declares the gastro CLI as a tool dependency (`tool github.com/andrioid/gastro/cmd/gastro`), the extension launches the project-pinned binary via `go tool`. The version is determined by your `go.mod`. The `gastro new` scaffold sets this up automatically.
3. **`gastro` from PATH** -- the legacy default. Works for any global install (`go install` or `mise`).

The chosen launch source is logged in the **Gastro Language Server** output channel at startup, alongside the LSP server's own startup version log line.

## Requirements

- **`gastro` binary**, available via any of:
  - **Per-project (recommended for teams):** add `tool github.com/andrioid/gastro/cmd/gastro` to your `go.mod` (`go get -tool github.com/andrioid/gastro/cmd/gastro`). The extension auto-detects this. The `gastro new` scaffold sets it up for you.
  - **Global with mise:** `mise use github:andrioid/gastro@latest`
  - **Global with go install:** `go install github.com/andrioid/gastro/cmd/gastro@latest`
  - Or set `gastro.lspPath` to an explicit binary path.
- **`gopls`** (optional, recommended) for Go intelligence in the frontmatter
  - Install: `go install golang.org/x/tools/gopls@latest`

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `gastro.lspPath` | `""` | Absolute path to the gastro binary. Leave empty to use auto-detection (`go tool` from workspace `go.mod`, falling back to `gastro` on PATH). |
| `gastro-lsp.trace.server` | `"off"` | Traces the communication between VS Code and the Gastro Language Server. Set to `"verbose"` for debugging. |
