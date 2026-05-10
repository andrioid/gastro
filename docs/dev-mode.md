# Dev Mode

Gastro's dev mode gives you a fast inner-loop: edit a `.gastro` file, see the
change in the browser within a second, without restarting the server for
template-only edits.

Gastro ships **two dev-loop commands**, one per project shape:

- **`gastro dev`** — framework mode. No flags, no configuration. Use this for
  projects scaffolded with `gastro new`.
- **`gastro watch`** — library mode. You supply your own `--build` and
  `--run` commands; gastro handles file watching, smart classification, and
  process supervision. Use this for projects where gastro is added to an
  existing Go service.

The runtime is identical between modes. What differs is which command you run
in development.

## Quick start (framework mode)

For standalone projects created with `gastro new`, just run:

```sh
gastro dev
```

`gastro dev` watches `components/`, `pages/`, and `static/`, regenerates code
as needed, and serves the app on `:4242` (or `$PORT`). It takes no flags by
design — the scaffold's batteries-included experience is the only thing it
knows. If you find yourself wanting to customise the build or run command,
that's the signal to switch to `gastro watch`.

## Quick start (library mode)

For existing Go projects with gastro embedded, run:

```sh
gastro watch --run 'go run ./cmd/myapp'
```

This watches `.gastro` and `*.go` files under the project root, regenerates
and restarts the binary on change, and signals the browser to reload. For
faster iteration, split the build out so `go build`'s incremental cache helps:

```sh
gastro watch \
  --build 'go build -o tmp/app ./cmd/myapp' \
  --run 'tmp/app'
```

`--build` is repeatable, so you can chain a CSS pipeline or anything else
before the Go build:

```sh
gastro watch \
  --build 'tailwindcss -i in.css -o internal/web/static/styles.css' \
  --build 'go build -o tmp/app ./cmd/myapp' \
  --run 'tmp/app'
```

Full flag reference: `gastro watch --help`. The full library-mode walkthrough
including component-only and `gastro.New(...)` integration patterns is in
[Getting Started — Library Mode](getting-started-library.md).

### Build-failure resilience

When a build fails (typo in a `.go` file, broken template), the
**previously-running binary stays alive** so you can keep clicking through your
app while you fix the error. The failure surfaces both in the terminal and as
a `console.warn` in the browser DevTools (delivered via the existing live-reload
SSE channel). When the next build succeeds, the binary is replaced and the
browser receives a normal reload event.

### What `gastro watch` is and isn't

We built `gastro watch` so you don't need to install `air`, `wgo`, or
`watchexec` just to get a hot-reload loop for a gastro-in-Go-project setup.
It's a small, focused, gastro-aware version of what `air` does for the slice
of use cases gastro projects actually have.

It is **not** a general-purpose Go file watcher. The watch surface is
hardcoded to:

- `.gastro`, `.md` (and other files referenced via `//gastro:embed`), and `static/**`,
  rooted at the gastro project root (`--project` / `GASTRO_PROJECT` / cwd).
- `*.go` files under the **enclosing Go module root** — by default
  `gastro watch` walks up from the project root looking for a `go.mod` and
  uses that directory as the Go-watch root. This means an edit to
  `cmd/myapp/main.go` is picked up even when your gastro project lives
  at e.g. `internal/web/`. If no `go.mod` is found before hitting a
  `.git/` directory or `$HOME`, the Go-watch root falls back to the
  project root (the v1 behaviour).

Override or pin the Go-watch root explicitly with `--watch-root PATH`. The
startup banner shows which directory was selected and how:

```
gastro: watching *.go under /Users/me/myapp (go.mod)
```

Any directory named `.gastro`, `vendor`, `node_modules`, `.git`, or `tmp`
is skipped wherever it appears in the watched tree. Add project-specific
excludes with `--exclude PATH` (matched as a path prefix relative to the
Go-watch root). If you need a more flexible runner, use the
[composition recipe](#composing-with-other-runners) below.

> **Build commands and `--project`.** `--build` and `--run` execute with
> cwd set to the gastro project root, not the Go module root. If your
> gastro project lives in a subdirectory and your build refers to a
> package in the parent (e.g. `./cmd/myapp`), use a relative path that
> works from the subdirectory: `go build -o tmp/app ../cmd/myapp`.

## How it works

Both `gastro dev` and `gastro watch` are built on the same three pieces, which
you can also wire up manually for [composed setups](#composing-with-other-runners):

1. **Source watcher** — polls `.gastro` files (and `*.go` files under
   `gastro watch`); regenerates with `gastro generate` on every detected change.
2. **Signal files** — after each successful `gastro generate`, the CLI touches
   `.gastro/.reload`. After a failed `gastro watch` build, it writes the failure
   message to `.gastro/.build-error`. Anything that touches these files triggers
   the corresponding browser event (reload / `console.warn`).
3. **DevReloader SSE endpoint** — the generated router mounts
   `GET /__gastro/reload`; the browser subscribes and reacts to two event
   kinds: `reload` (page refresh) and `build-error` (console warning). The
   injected `<script>` handles reconnection automatically.

The signal files are the seam between the watcher / build pipeline and the
running server. Any source watcher can drive them:

```sh
# Manually signal a browser reload:
touch .gastro/.reload
```

## Composing with other runners

If you already have a runner you love (`watchexec`, `entr`, a Makefile-based
system, an existing supervisor) and want gastro to plug into it rather than
own the dev loop, drive the pieces yourself.

The building blocks are:

- `gastro generate` — regenerate the `.gastro/` tree from sources. Idempotent;
  the underlying file writes are skip-if-equal, so calling it on every change
  doesn't churn mod-times.
- `touch .gastro/.reload` — trigger a browser reload via SSE.
- Your own watcher — e.g. `watchexec`, `entr`, `find ... -newer`, a CI loop.

Example with `watchexec`:

```sh
watchexec -e gastro -- sh -c 'gastro generate && touch .gastro/.reload'
```

This pattern is the right tool when:

- You already have a runner integrated with your dev environment
- You need a watch surface gastro doesn't cover (arbitrary file extensions,
  glob patterns, multiple project roots in one process)
- You want to compose gastro generation into a larger pipeline
  (`make dev`, a custom `air` config, a remote-dev container's hot-reload)

If you just want a one-command hot-reload loop for a gastro-in-Go-project
setup, use `gastro watch` instead — see the [library-mode quick
start](#quick-start-library-mode) above.

## Embedded-package projects

If your Go module has a structure like:

```
cmd/myapp/main.go         ← process entry point
internal/web/             ← gastro project root
  components/
  pages/
  static/
  .gastro/
```

you have two reasonable choices:

- **Recommended:** use `gastro watch --run 'go run ./cmd/myapp'` from the repo
  root with `GASTRO_PROJECT=internal/web` set in your shell or `mise.toml`.
  One command, no extra runner, and you get smart classification +
  build-failure resilience. See the [library-mode getting started
  guide](getting-started-library.md) for the full walkthrough.
- **For composed setups:** wire `gastro generate` into your existing runner
  via the [composition recipe](#composing-with-other-runners) above. Use this
  when you want gastro to be one piece of a larger pipeline rather than the
  dev-loop driver.

Both choices use the same env vars to bridge the cwd / project-root mismatch:

- **`GASTRO_PROJECT`** — tells the `gastro` CLI _and_ the `gastro lsp`
  language server where the gastro project lives. Both will operate as if
  they were invoked from that directory, so you can run `gastro generate`,
  `gastro check`, `gastro list`, `gastro fmt`, etc. from anywhere in your
  repo without `cd`.
- **`GASTRO_DEV_ROOT`** — tells your already-built _runtime_ where to find
  `.gastro/templates/` and `static/` at request time, when the server
  process is launched from somewhere other than the gastro project root.

### `GASTRO_PROJECT`

Set this once at the top of your `mise.toml` (or in `.envrc`) and every
gastro CLI command picks it up:

```toml
# mise.toml at the repo root
[env]
GASTRO_PROJECT = "{{ config_root }}/internal/web"

[tasks."verify:generated"]
run = "gastro check"      # no `dir =` needed

[tasks."generate:web"]
run = "gastro generate"    # no `dir =` needed
```

If the value is missing or points at a path that doesn't exist, the CLI exits
with a clear error.

The LSP also honours `GASTRO_PROJECT` when set: it pins all `.gastro` files
to that root, which is useful if your editor's structural heuristic picks
the wrong root (rare, but possible for unusual layouts). When unset, the LSP
falls back to a structural heuristic that walks up from each file looking for
a `pages/` or `components/` ancestor — this works zero-config for the common
case including nested-project setups like git-pm.

Note that `GASTRO_PROJECT` does **not** propagate from `mise.toml` `[env]`
into VS Code unless you launch VS Code from a mise-activated shell. The LSP
heuristic exists precisely so that nested projects work without needing the
env var to be set in the editor's environment.

### `GASTRO_DEV_ROOT`

When the server process starts from a directory other than the gastro project
root, set `GASTRO_DEV_ROOT` to point at the gastro project root. Gastro uses it
to resolve `.gastro/templates/` and `static/` in dev mode:

```sh
# Start the server from cmd/myapp with GASTRO_DEV=1 and GASTRO_DEV_ROOT pointing
# at the embedded gastro project.
GASTRO_DEV=1 GASTRO_DEV_ROOT=/path/to/internal/web go run ./cmd/myapp
```

Without `GASTRO_DEV_ROOT`, gastro falls back to the current working directory,
which causes `open .gastro/templates/*.html: no such file or directory` errors
when the process is not started from the gastro project root.

### Example `mise` dev tasks

With `GASTRO_PROJECT` set in `[env]`, pure-gastro tasks lose their `dir =`
lines. Tasks that mix in `tailwindcss`, `touch .gastro/.reload`, or
`watchexec` paths still need an explicit `dir` because those tools resolve
paths relative to cwd:

```toml
# mise.toml (repo root)

[env]
GASTRO_PROJECT = "{{ config_root }}/internal/web"

[tasks."dev:gastro"]
description = "Watch .gastro sources and regenerate on change"
dir = "{{ config_root }}/internal/web"
run = "watchexec -e gastro -- sh -c 'gastro generate && touch .gastro/.reload'"

[tasks."dev:server"]
description = "Run the app in dev mode"
dir = "{{ config_root }}/internal/web"
run = "GASTRO_DEV=1 go run ../../cmd/myapp --repo ../.."

[tasks.dev]
description = "Start the full dev loop"
depends = ["dev:gastro", "dev:server"]
```

The `dir =` on `dev:server` is still needed because `GASTRO_DEV_ROOT` tells
gastro's _runtime_ where to find templates, but it doesn't change cwd for
`go run`. (You could also set `GASTRO_DEV_ROOT={{ config_root }}/internal/web`
in `[env]` and drop the `dir`.)

If your process can always be started from the gastro project root, neither
`GASTRO_DEV_ROOT` nor the `cd` dance is necessary.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `GASTRO_PROJECT` | `""` (cwd) | Absolute or relative path to the gastro project root. When set, the `gastro` CLI changes to this directory before running project-level subcommands (`generate`, `build`, `dev`, `list`, `check`, and `fmt` with no targets). The LSP honours it as a global pin for all `.gastro` files. Invalid values cause the CLI to exit with an error and the LSP to log a warning and fall back to its heuristic. |
| `GASTRO_DEV` | `""` | Set to `1` to enable dev mode (live templates, SSE reload) |
| `GASTRO_DEV_ROOT` | `.` (cwd) | Absolute path to the gastro project root; used to locate `.gastro/templates/` and `static/` when the server process runs from a different directory |
| `PORT` | `4242` | Port for `gastro dev` (standalone projects only) |

## Smart rebuild vs reload

Both `gastro dev` and `gastro watch` classify each file change to avoid
unnecessary rebuilds:

| Change type | Result |
|---|---|
| Template body only (`.gastro` file, template section) | `gastro generate` + signal `.gastro/.reload` (no restart) |
| Frontmatter, new file, or deleted file | `gastro generate` + full rebuild + restart |
| `static/` file | `gastro generate` + signal `.gastro/.reload` |
| Referenced `.md` file | `gastro generate` + signal `.gastro/.reload` |
| `*.go` file (under `gastro watch` only) | `gastro generate` + full rebuild + restart |

Template-only changes are fast — the rebuilt Go binary is not needed because
dev mode loads templates from disk on every request (no embed FS in dev
mode).

Smart classification only applies to `.gastro` files. All `*.go` changes
(under `gastro watch`) are treated as restart-class — partial Go reloads
are not a thing the runtime can do safely.
