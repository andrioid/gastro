# Contributing to Gastro

## Prerequisites

- Go 1.26+
- [mise](https://mise.jdx.dev/) for managed tooling

```sh
git clone https://github.com/andrioid/gastro
cd gastro
mise trust && mise install
```

## Development Workflow

### Running Tests

```sh
# All tests (with race detector)
mise run test

# Or directly:
go test ./... -race -timeout 120s

# Specific package
go test ./internal/parser/ -race -v

# With gopls-dependent tests (requires mise activation)
eval "$(mise activate zsh)"
go test ./... -race -timeout 120s
```

Always use `-race` to catch data races. The `mise run test` task includes it by
default.

### Building

```sh
# CLI
go build -o gastro ./cmd/gastro/

# LSP server
go build -o gastro-lsp ./cmd/gastro-lsp/

# Example blog
cd examples/blog
../../gastro generate
go build -o myblog .
```

### Linting

```sh
go vet ./...
```

## TDD Approach

All changes must follow red/green/refactor:

1. **Red:** Write a failing test that describes the behaviour you want.
2. **Green:** Write the minimum code to make the test pass.
3. **Refactor:** Clean up while keeping tests green.

No feature is complete without test coverage.

### Test Conventions

- **Tests are for behaviour, not implementation.** If a refactor doesn't change
  observable behaviour, no tests should break.
- **Table-driven tests** for parsers and transformers.
- **Integration tests** for the CLI and LSP (spawn subprocess, verify output).
- **`testdata/` directories** live next to the package they test.
- Test files use the `_test` package suffix (e.g., `package parser_test`).

## Code Style

- Write idiomatic Go.
- Prefer functional-style composition over inheritance.
- Return early, avoid nested if/else chains.
- Comments explain why, not what.
- Dead code must be deleted.
- No barrel files. No `../` imports. No `any` when a concrete type works.

## Project Structure

See [architecture.md](architecture.md) for a detailed package guide.

Quick orientation:

| Directory | Purpose |
|-----------|---------|
| `cmd/gastro/` | CLI binary |
| `cmd/gastro-lsp/` | LSP server binary |
| `internal/parser/` | `.gastro` file parser |
| `internal/codegen/` | Go code generation |
| `internal/router/` | File-based routing |
| `internal/compiler/` | Orchestrator |
| `internal/watcher/` | File watching |
| `internal/lsp/` | Language server internals |
| `pkg/gastro/` | Runtime library (imported by generated code) |
| `tree-sitter-gastro/` | Syntax highlighting grammar |
| `editors/` | Editor extensions |
| `examples/` | Example projects |

## Making Changes

### Adding a new built-in template function

1. Add the function to `pkg/gastro/funcs.go` in `DefaultFuncs()`.
2. Write a test in `pkg/gastro/funcs_test.go`.
3. If the function takes the piped value as input, it must be the **last**
   parameter (Go template pipe convention).
4. Update the FuncMap table in `docs/design.md`.

### Adding a new CLI command

1. Add the command case in `cmd/gastro/main.go`.
2. Add it to `printUsage()`.
3. If it needs new internal logic, add it to the appropriate `internal/` package
   with tests first.

### Modifying the parser

1. Write a failing test in `internal/parser/parser_test.go` first.
2. The parser returns a `*parser.File` struct -- add fields there if needed.
3. Update downstream consumers (codegen, compiler, LSP shadow).

### Working on Editor Extensions

**VS Code extension (`editors/vscode/`):**

```sh
cd editors/vscode
npm install   # must run before the extension works
```

The extension depends on `vscode-languageclient`. Without `npm install`, VS
Code will fail to activate the extension with a module-not-found error.

To test changes, symlink the extension directory into VS Code's extensions:

```sh
ln -s "$(pwd)" ~/.vscode/extensions/gastro-vscode
```

Then reload VS Code (`Cmd+Shift+P` > "Developer: Reload Window").

**Neovim plugin (`editors/neovim/`):**

```sh
mise run link:neovim
```

This symlinks the plugin to `~/.config/nvim/after/plugin/gastro.lua`. The LSP
starts automatically for `.gastro` files. To customize the LSP command, add
`require("gastro").setup({ cmd = "/path/to/gastro-lsp" })` to your Neovim
config. Changes take effect on next Neovim restart.

**Zed extension (`editors/zed/`):**

The Zed extension is a Rust WASM crate that auto-downloads `gastro-lsp` from
GitHub releases. To install for development:

1. Open Zed's command palette and run "zed: install dev extension"
2. Select the `editors/zed/` directory

Zed compiles the Rust code to WASM automatically. Use `zed --foreground` to
see extension logs.

**All editors require `gastro-lsp` and `gopls` in PATH.** If using mise,
`mise install` in the project root provides both.

### Working on the LSP

The LSP has a known issue: gopls proxy completions and diagnostics are not yet
working reliably. See `.opencode/plans/lsp-debugging.md` for the investigation
plan and hypotheses.

Key files:
- `cmd/gastro-lsp/main.go` -- LSP server, message routing, gopls integration
- `internal/lsp/shadow/workspace.go` -- Virtual `.go` file generation
- `internal/lsp/proxy/proxy.go` -- gopls subprocess management
- `internal/lsp/sourcemap/sourcemap.go` -- Position mapping
- `internal/lsp/template/completions.go` -- Template body intelligence

Integration tests for the LSP are in `cmd/gastro-lsp/lsp_integration_test.go`.
They spawn gastro-lsp as a subprocess and communicate via JSON-RPC over
stdin/stdout.

## Commit Messages

- Summarise the change in 1-2 sentences.
- Focus on the "why" rather than the "what".
- Use present tense ("add", "fix", "update", not "added", "fixed").
