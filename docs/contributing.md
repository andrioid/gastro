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

# Example blog -- the example go.mod has a `tool` directive plus a local
# `replace` pointing at the working tree, so `go tool gastro` runs the
# in-progress CLI without a separate build step.
cd examples/blog
go tool gastro generate
go build -o myblog .
```

All four examples (`blog`, `dashboard`, `gastro`, `sse`) declare gastro
as a `tool` in their `go.mod`. You don't need a global `gastro` on your
`PATH` to develop in them; `go tool gastro <cmd>` is the canonical
invocation for example work.

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
| `internal/watcher/` | Change classification + file collectors |
| `internal/devloop/` | Watch loop shared by `gastro dev` and `gastro watch` |
| `internal/lsp/` | Language server internals |
| `pkg/gastro/` | Runtime library (imported by generated code) |
| `tree-sitter-gastro/` | Syntax highlighting grammar |
| `editors/` | Editor extensions |
| `examples/` | Example projects |

## Project shapes (“framework” vs “library”)

Gastro intentionally supports two project shapes as peer stories:

- **Framework mode** — a project scaffolded by `gastro new`. Entry point is
  `gastro dev`, which takes no flags by design.
- **Library mode** — gastro added to an existing Go service via
  `go get -tool github.com/andrioid/gastro/cmd/gastro`. Entry point is
  `gastro watch --run …`, which the user configures.

In user-facing copy (docs, CLI hints, error messages) prefer the words
**framework** and **library** over earlier candidates like “standalone” or
“embedded”. Internal package and identifier names stay neutral
(`internal/devloop`, not `internal/frameworkloop`) so they don't need to
change when the documentary framing evolves.

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

#### Auditing the shadow workspace

The shadow workspace generates a virtual `.go` file per `.gastro` source by
calling `codegen.GenerateHandler` directly (R6) — the same source
`gastro generate` writes to `.gastro/<file>.go`. To check that the shadow
output type-checks against the real runtime end-to-end, run:

```sh
mise run audit                       # default: examples/gastro
go run ./cmd/auditshadow ~/path/to/your/project
```

The harness walks every `.gastro` under the target, drives each through
`shadow.Workspace.UpdateFile`, runs `go build` against the generated
package, and reports any non-import diagnostics. A green run means
gopls would be quiet on those files. Use this before/after touching
anything in `internal/lsp/shadow/` or `internal/codegen/generate.go`.

#### Hoist-aware LSP smoke check

When you change anything that touches frontmatter hoisting
(`internal/codegen/hoist.go`, `internal/codegen/rewrite_refs.go`,
`internal/codegen/freevars.go`, or the `MangleHoisted` plumbing in
`internal/codegen/generate.go`), the LSP shadow needs an extra
spot-check:

1. `mise run audit` — `cmd/auditshadow` parity gate, must stay green.
2. Open `examples/blog/pages/blog/index.gastro` in an editor with the
   gastro LSP attached.
3. Add a hoistable decl at the top of the frontmatter, e.g.:
   ```go
   var slugRE = regexp.MustCompile(`^[a-z]+$`)
   ```
4. Hover over `slugRE` and confirm the popup shows the
   user-written name (`var slugRE *regexp.Regexp`), **not** the
   mangled `__page_blog_index_slugRE`. Mangled names leaking into
   hover text means the shadow is incorrectly running with
   `MangleHoisted=true` — the opt-out invariant has regressed.
5. Trigger completion mid-frontmatter and confirm the same: no
   `__page_` / `__component_` prefix in completion labels.

These checks are not in CI because they require an editor session.
The automated regression guard for the same invariant lives in
`internal/lsp/shadow/shadow_hoist_test.go`.

#### Frontmatter-var type-resolution smoke check

When you change anything that touches `_ = X` suppression-line
emission, the codegen handler/component templates, or
`queryVariableTypes`/`queryFieldsFromGopls` in
`internal/lsp/server/gopls.go`, run this manual smoke check:

1. `mise run audit` — must stay green.
2. Open `examples/blog/pages/blog/index.gastro` in an editor with the
   gastro LSP attached.
3. Hover on `{{ .Posts }}`. The popup must show the type
   `[]db.Post` (or similar) in a code block, not just the
   description "frontmatter variable". Missing type means
   `queryVariableTypes` returned empty — likely the `_ = Posts`
   suppression line stopped being emitted.
4. Inside `{{ range .Posts }}`, type `{{ .` and trigger completion.
   You should see `Title`, `Slug`, `Author` etc. — the fields of
   `db.Post`. No completions means the probe-injection in
   `queryFieldsFromGopls` couldn't anchor on a `_ = Posts` line.

The automated regression guards live at
`cmd/gastro/lsp_integration_test.go::TestLSP_TemplateHover` (asserts
the type string is present, not just the description) and
`TestLSP_TemplateRangeFieldCompletion` (asserts range-scoped field
completion runs end-to-end).

#### LSP binary refresh when hacking on gastro

The LSP is a long-running process. When you change gastro source the
running LSP keeps the *old* code in memory until it is restarted, even
if rebuilds happen on disk. The full picture, depending on how the
editor launches `gastro`:

| Launch path                       | Build refresh                              | Process refresh           |
| --------------------------------- | ------------------------------------------ | ------------------------- |
| `go tool gastro lsp` (recommended) | Automatic via Go's content-addressed cache | Restart LSP in editor     |
| `gastro` from PATH                | Manual: `mise run install:gastro`          | Restart LSP in editor     |

**Recommended setup for hacking on gastro from a downstream project:**

1. In the downstream's `go.mod`, add a `replace` pointing at your local
   gastro checkout and a `tool` directive for the gastro CLI:

   ```
   replace github.com/andrioid/gastro => /path/to/gastro
   tool github.com/andrioid/gastro/cmd/gastro
   ```

2. Use the in-tree VS Code extension (or rebuild + install with
   `mise run install:vscode`). Versions 0.1.20+ detect the `tool`
   directive and launch via `go tool gastro lsp`, which routes
   through the `replace` and rebuilds your local source via the Go
   build cache. No `go install` step.

3. After editing gastro source, restart the LSP in your editor:
   - VS Code: `Cmd+Shift+P` → *Developer: Reload Window* (or the
     gastro-specific restart command if your build exposes one).
   - Neovim: `:LspRestart` or `:e` on the file.

If the editor extension you have predates `go tool` resolution, it
falls back to `gastro` from PATH. In that case the binary on PATH must
match the active mise/Go context where the editor launched: a binary
installed by `mise run install:gastro` from the gastro repo lands in
gastro's `mise.toml`-pinned Go bin directory, which is *not necessarily
the same* as a downstream project's active Go bin directory. The
`go tool` flow sidesteps this entirely; reach for `mise run install:vscode`
if you find yourself debugging "wrong binary on PATH" mismatches.

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
