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
# CLI (includes LSP server via `gastro lsp`)
go build -o gastro ./cmd/gastro/

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
| `cmd/gastro/` | CLI binary (includes LSP via `gastro lsp`) |
| `internal/lsp/server/` | LSP server |
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
npm install   # install dependencies (including esbuild)
npm run build # bundle extension.js into dist/extension.js
```

The extension is bundled with esbuild. `dist/extension.js` is the entry point
loaded by VS Code — the raw `extension.js` source is not used directly.

For iterative development, use watch mode to rebuild on changes:

```sh
npm run watch
```

To test changes, symlink the extension directory into VS Code's extensions
(or use `mise run link:vscode` which also runs the build):

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
`require("gastro").setup({ cmd = { "/path/to/gastro", "lsp" } })` to your Neovim
config. Changes take effect on next Neovim restart.

**Zed extension (`editors/zed/`):**

The Zed extension is a Rust WASM crate that auto-downloads `gastro` from
GitHub releases. To install for development:

1. Open Zed's command palette and run "zed: install dev extension"
2. Select the `editors/zed/` directory

Zed compiles the Rust code to WASM automatically. Use `zed --foreground` to
see extension logs.

**All editors require `gastro` and `gopls` in PATH.** If using mise,
`mise install` in the project root provides both.

### Working on the LSP

The LSP has a known issue: gopls proxy completions and diagnostics are not yet
working reliably. See `.opencode/plans/lsp-debugging.md` for the investigation
plan and hypotheses.

Key files:
- `internal/lsp/server/` -- LSP server, message routing, gopls integration
- `internal/lsp/shadow/workspace.go` -- Virtual `.go` file generation
- `internal/lsp/proxy/proxy.go` -- gopls subprocess management
- `internal/lsp/sourcemap/sourcemap.go` -- Position mapping
- `internal/lsp/template/completions.go` -- Template body intelligence

Integration tests for the LSP are in `cmd/gastro/lsp_integration_test.go`.
They spawn `gastro lsp` as a subprocess and communicate via JSON-RPC over
stdin/stdout.

#### Project root resolution

`findProjectRoot` (in `internal/lsp/server/util.go`) decides which directory
each `.gastro` file belongs to. The order is:

1. **`GASTRO_PROJECT` env var.** If set to an existing directory, every file
   gets pinned to it. Invalid values are ignored with a one-time warning.
2. **Structural heuristic.** Walk up from the file. The first ancestor named
   `pages` or `components` wins (its parent is the project root). This is
   what makes nested-project setups like `git-pm/internal/web/` work
   zero-config.
3. **Enclosing `go.mod`.** If we never see a structural marker, the directory
   containing `go.mod` is used. This preserves the original behavior for
   flat layouts.
4. **Fallback.** The editor's workspace root, used when there's no `go.mod`
   anywhere up the tree.

When adding a feature that depends on the project root (component discovery,
shadow workspace setup, etc.), prefer `instanceForURI(uri)` over
recomputing roots yourself — it caches one `projectInstance` per discovered
root and handles the multi-project case correctly.

## Deprecation Policy

When a public-facing API is being replaced rather than removed outright,
use a build-time deprecation warning bridge instead of a hard break.

1. Keep the old surface working for **at least two minor releases** after
   the warning lands. Pre-1.0 churn is allowed (`DECISIONS.md`,
   2026-04-26), but a warning gives adopters time to migrate without
   stalling unrelated upgrades.
2. Emit a warning from the same channel that surfaces other compile-time
   issues (`internal/codegen` warnings flow through `gastro generate`,
   `gastro build`, and `gastro check`; `gastro dev` prints them to the
   console). The warning text must name the replacement explicitly, e.g.
   ``"gastro.Context() is deprecated; use ambient `r *http.Request` and
   `w http.ResponseWriter` (see docs/pages.md)"``.
3. The warning is **non-blocking by default** but is promoted to an error
   under `--strict` (or any subcommand that defaults to strict, like
   `gastro generate` and `gastro check`). This matches the convention
   established for `(dict ...)` validation in `DECISIONS.md` (2026-04-30).
4. Update **all docs and examples** to the new pattern in the same
   release that introduces the warning. The deprecation window is for
   *external* adopters; the in-repo code should be the canonical example
   of the new shape from day one.
5. Record both the warning and the planned removal in a dated
   `DECISIONS.md` entry. The removal PR (two minor releases later)
   references that entry and adds its own.

## Commit Messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/)
to automate versioning and changelog generation via
[release-please](https://github.com/googleapis/release-please).

### Format

```
type(scope): short description

Optional body explaining *why*, not *what*.
```

### Types

| Type | When to use |
|------|-------------|
| `feat` | New user-facing functionality |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `test` | Adding or updating tests |
| `chore` | Build, CI, dependency updates |
| `ci` | CI/CD workflow changes |

### Scope (optional but encouraged)

Use the package or area being changed: `parser`, `codegen`, `lsp`, `router`,
`cli`, `vscode`, `neovim`, `zed`, `ci`.

### Examples

```
feat(lsp): add component auto-import on completion
fix(codegen): handle duplicate child template names
docs: add SSE guide
chore(ci): replace softprops action with gh CLI
refactor(parser): extract backtick tracking into helper
```

### Breaking changes

Add `!` after the type/scope, or include `BREAKING CHANGE:` in the commit body.
release-please will bump the minor version (while on v0.x) or the major version
(v1+).

```
feat(codegen)!: replace <Component> syntax with template actions
```
