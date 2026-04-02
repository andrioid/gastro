# Gastro Architecture

This document explains how Gastro's codebase is structured, what each package
does, and how data flows through the system.

## Overview

Gastro has three main subsystems:

1. **Compiler pipeline** -- parses `.gastro` files and generates Go code
2. **Runtime library** -- imported by generated code at runtime
3. **Language server** -- provides editor intelligence for `.gastro` files

```
                    .gastro files
                         |
         +---------------+---------------+
         |                               |
    [Compiler]                      [LSP Server]
    cmd/gastro                      cmd/gastro-lsp
         |                               |
    internal/parser    <-- shared -->    internal/parser
    internal/codegen                    internal/lsp/shadow
    internal/router                    internal/lsp/proxy
    internal/compiler                  internal/lsp/sourcemap
    internal/watcher                   internal/lsp/template
         |                               |
    .gastro/ (generated)            gopls (subprocess)
         |
    pkg/gastro (runtime)
         |
    Running HTTP server
```

## Package Guide

### `internal/parser/`

**Purpose:** Parse `.gastro` files into their constituent parts.

**Key type:** `parser.File` -- contains frontmatter, template body, imports
(Go and component), and line numbers.

**Key function:** `parser.Parse(filename, content) (*File, error)`

The parser:
- Splits content at `---` delimiters
- Extracts `import` declarations (single and grouped)
- Extracts component imports (`.gastro` paths)
- Strips imports from the frontmatter body
- Records line numbers for source mapping

The parser does NOT validate Go syntax. It operates on string splitting and
line-by-line analysis. Go AST analysis happens in `codegen/`.

### `internal/codegen/`

**Purpose:** Analyse frontmatter and generate Go source code.

Three responsibilities, split across files:

#### `analyze.go`

**Key function:** `AnalyzeFrontmatter(frontmatter) (*FrontmatterInfo, error)`

Wraps the frontmatter in a valid Go file and parses it with `go/ast`. Extracts:
- All variable declarations (`:=` and `var`)
- Classification: uppercase (exported to template) vs lowercase (private)
- Detection of `gastro.Context()` calls (marks file as a page)
- Detection of `gastro.Props()` calls (marks file as a component)

The frontmatter is wrapped in `package __gastro / func __handler() { ... }`
so `go/parser` can handle it. Type declarations (`type Props struct{}`) are
hoisted to package level since they can't live inside a function body.

#### `generate.go`

**Key function:** `GenerateHandler(file, info) (string, error)`

Produces Go source code for a page handler or component render function. Uses
`text/template` to emit the generated code. The output includes:
- Package declaration
- Imports (user's + gastro runtime)
- Template variable with parsed `html/template`
- Handler function with frontmatter code, data map, and template execution
- Panic recovery wrapper

The `gastro.Context()` marker is rewritten to `gastroRuntime.NewContext(w, r)`.
The `gastro.Props()` marker is stripped (component generation is TODO).

#### `template.go`

**Key function:** `TransformTemplate(body, uses) (string, error)`

Transforms the template body:
- `{{ render ComponentName (dict "Prop" .expr) }}` becomes `{{ __gastro_ComponentName (dict "Prop" .expr) }}`
- `{{ wrap ComponentName (dict ...) }}...{{ end }}` becomes a template call with children
- `{{ .Children }}` passes through unchanged
- Standard `{{ }}` expressions pass through unchanged

Uses iterative string processing with regex for action matching. Processes
`render` actions first, then `wrap`/`end` actions with children.

### `internal/router/`

**Purpose:** Map `.gastro` file paths to HTTP routes.

**Key function:** `BuildRoutes(files) []Route`

Converts file paths to HTTP patterns:
- `pages/index.gastro` -> `GET /`
- `pages/about/index.gastro` -> `GET /about`
- `pages/blog/[slug].gastro` -> `GET /blog/{slug}`

Also provides `RouteToFuncName` which derives Go function names from file paths,
matching the naming in `codegen.handlerFuncName`.

### `internal/compiler/`

**Purpose:** Orchestrate the full compilation pipeline.

**Key function:** `Compile(projectDir, outputDir) error`

1. Discovers all `.gastro` files in `pages/` and `components/`
2. For each file: parse -> analyse -> transform template -> generate handler
3. Writes generated `.go` files and template `.html` files to output directory
4. Generates `routes.go` with the `Routes()` function and route registrations

### `internal/watcher/`

**Purpose:** File watching utilities for `gastro dev`.

Provides:
- `CollectGastroFiles(dir)` -- find all `.gastro` files
- `DetectChangedSection(old, new)` -- determine if frontmatter or template changed
- `ClassifyChange(file, section)` -- decide if change needs restart or reload
- `Debounce(duration, fn)` -- rate-limit rapid file changes

### `pkg/gastro/`

**Purpose:** Runtime library imported by generated code.

This is the only package that end users' compiled code depends on.

#### `context.go`

The `Context` type wraps `http.ResponseWriter` and `*http.Request`. Provides
`Param()`, `Query()`, `Redirect()`, `Error()`, and `Header()` methods.

#### `funcs.go`

`DefaultFuncs()` returns a `template.FuncMap` with 18 built-in helpers.
Functions that accept a piped value take it as the **last** parameter
(Go template convention). Example: `timeFormat(layout, t)` not
`timeFormat(t, layout)`.

#### `props.go`

`MapToStruct[T](map[string]any) (T, error)` converts a map (from template
`dict` calls) to a typed struct using reflection. Handles type coercion:
string->bool, string->int, float64->int, etc.

#### `recover.go`

`Recover(w, r)` is a deferred function that catches panics in handlers,
logs them, and returns 500.

#### `sse.go`

`NewSSE(w, r)` upgrades an `http.ResponseWriter` to a Server-Sent Events
stream. Provides `Send(eventType, data...)`, `IsClosed()`, and `Context()`.
Framework-agnostic -- works with Datastar, HTMX, or any SSE client.

#### `datastar/` (subpackage)

`pkg/gastro/datastar` wraps the generic SSE helper with Datastar-specific
convenience methods: `PatchElements()`, `PatchSignals()`, `RemoveElement()`.
See [sse.md](sse.md) for usage.

### `internal/lsp/`

**Purpose:** Language server internals.

#### `shadow/`

**`shadow.go`** -- `GenerateVirtualFile()` creates a virtual `.go` file from
`.gastro` frontmatter for the old (pre-workspace) code path.

**`workspace.go`** -- `Workspace` manages a temp directory that symlinks the
user's project and contains virtual `.go` files. Each `.gastro` file gets a
virtual `.go` file at the workspace root with a unique function name. The
workspace:
- Symlinks `go.mod`, `go.sum`, and source directories from the user's project
- Writes virtual files with `package main`, gastro runtime stubs, and the
  frontmatter code in a uniquely-named function
- Comments out `import` lines (including component imports) in the function
  body (they're placed as top-level declarations to avoid Go syntax errors)
- Provides source maps for position translation

#### `proxy/`

`GoplsProxy` manages a `gopls serve` subprocess. Communicates via JSON-RPC
over stdin/stdout. Handles:
- `Request()` -- send a request, wait for response (with 30s timeout)
- `Notify()` -- send a notification (no response expected)
- `readLoop()` -- background goroutine reading responses and notifications
- Notification callback for async events like `publishDiagnostics`

Also provides `MapPositionToVirtual/ToGastro` for translating LSP positions
between `.gastro` and virtual `.go` coordinates, and `Backoff` for
auto-restart with exponential backoff.

#### `sourcemap/`

`SourceMap` translates line numbers between `.gastro` files and virtual `.go`
files. The mapping is a linear offset: given the line where frontmatter starts
in each file, all positions shift by a constant.

#### `template/`

Template body intelligence (not related to gopls):
- `VariableCompletions()` -- suggests exported frontmatter variables
- `ComponentCompletions()` -- suggests imported component names
- `FuncMapCompletions()` -- suggests built-in template functions
- `Diagnose()` -- flags unknown variables and unknown components

### `cmd/gastro/`

The CLI binary. Three commands:
- `generate` -- run compiler, write to `.gastro/`
- `build` -- generate + `go build`
- `dev` -- watch mode with polling watcher, debounced rebuild, process restart

### `cmd/gastro-lsp/`

The LSP server binary. Handles JSON-RPC over stdin/stdout. On startup:
1. Receives `initialize` with the project root
2. Creates a shadow workspace
3. Attempts to spawn gopls pointed at the shadow workspace

**Graceful degradation:** If gopls is not in PATH, `gastro-lsp` logs a warning
and continues without Go intelligence. Template body features (variable,
component, and function completions) still work. Only frontmatter features
(Go completions, hover, diagnostics, go-to-definition) require gopls.

For each open `.gastro` file:
1. Generates a virtual `.go` file in the shadow workspace
2. Sends `didOpen` (first time) or `didChange` (updates) to gopls
3. Routes requests based on cursor position:
   - **Frontmatter region:** forwards to gopls with position mapping
   - **Template body:** returns own completions (variables, components, functions)

## Data Flow: Compilation

```
index.gastro
     |
     v
parser.Parse()
     |
     v
parser.File {
    Frontmatter: "ctx := gastro.Context()\nTitle := \"Hello\""
    TemplateBody: "<h1>{{ .Title }}</h1>"
    Imports: ["myapp/db"]
    ComponentImports: [{Name: "Card", Path: "components/card.gastro"}]
}
     |
     +---> codegen.AnalyzeFrontmatter()
     |         |
     |         v
     |     FrontmatterInfo {
     |         ExportedVars: [{Name: "Title"}]
     |         PrivateVars: [{Name: "ctx"}]
     |         IsPage: true
     |     }
     |
     +---> codegen.TransformTemplate()
     |         |
     |         v
     |     "<h1>{{ .Title }}</h1>"  (unchanged if no components)
     |
     +---> codegen.GenerateHandler()
               |
               v
           Generated Go source:
           func pageIndex(w http.ResponseWriter, r *http.Request) {
               defer gastroRuntime.Recover(w, r)
               ctx := gastroRuntime.NewContext(w, r)
               Title := "Hello"
               __data := map[string]any{"Title": Title}
               pageIndexTemplate.Execute(w, __data)
           }
```

## Data Flow: LSP Request

```
Editor sends completion request at line 3, col 10
     |
     v
gastro-lsp receives textDocument/completion
     |
     v
Parse .gastro file -> determine cursor is in frontmatter (line 3 < TemplateBodyLine)
     |
     v
forwardToGopls():
  1. Look up virtual file for this .gastro file
  2. Map position: gastro (3, 10) -> virtual (24, 10) via source map
  3. Send completion request to gopls with virtual file URI and mapped position
  4. Receive gopls response
  5. Return response to editor
```

## Key Design Constraints

1. **Generated code lives in `.gastro/`** -- gitignored, never hand-edited.
   All files in one flat Go package.

2. **The runtime package (`pkg/gastro/`) is the only dependency** of generated
   code. It must stay small and stable.

3. **The parser is deliberately simple** -- string splitting, not a full Go
   parser. Go AST analysis is deferred to `codegen/` where it's needed.

4. **The LSP proxies to gopls** rather than reimplementing Go intelligence.
   This is the same pattern used by Volar (Vue) and the Astro language server.

5. **File-based routing is convention, not configuration.** `pages/` is the
   fixed routing root. No config file.
