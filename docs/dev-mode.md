# Dev Mode

Gastro's dev mode gives you a fast inner-loop: edit a `.gastro` file, see the
change in the browser within a second, without restarting the server for
template-only edits.

## Quick start

For standalone projects created with `gastro new`, just run:

```sh
gastro dev
```

`gastro dev` watches `components/`, `pages/`, and `static/`, regenerates code
as needed, and serves the app on `:4242` (or `$PORT`).

## How it works

The dev loop is composed of three independent pieces that you can also wire up
manually for embedded-package projects (see below):

1. **Source watcher** — polls `.gastro` files; regenerates with `gastro generate`
   on every detected change.
2. **Signal file** — after each successful `gastro generate`, the CLI touches
   `.gastro/.reload`. Anything that touches this file triggers a browser reload.
3. **DevReloader SSE endpoint** — the generated router mounts
   `GET /__gastro/reload`; the browser subscribes and reloads on each `reload`
   event. The injected `<script>` handles reconnection automatically.

The signal file is the seam between the watcher and the running server. You can
use it from any source watcher (e.g. `watchexec`, `entr`, a custom Makefile):

```sh
# Manually signal a browser reload:
touch .gastro/.reload
```

This lets embedded-package projects — where `gastro dev` can't compile and exec
the binary directly — glue `gastro generate` into their own watcher without
reimplementing the SSE broadcast layer.

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

you cannot run `gastro dev` from `internal/web/` because it will try to build
a dev server from a `main.go` that doesn't exist there. Instead, compose the
watcher and server separately.

Two env vars work together to make this ergonomic:

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

`gastro dev` classifies each file change to avoid unnecessary rebuilds:

| Change type | Result |
|---|---|
| Template body only (`.gastro` file, template section) | `gastro generate` + signal `.gastro/.reload` (no restart) |
| Frontmatter, new file, or deleted file | `gastro generate` + full rebuild + restart |
| `static/` file | `gastro generate` + signal `.gastro/.reload` |
| Referenced `.md` file | `gastro generate` + signal `.gastro/.reload` |

Template-only changes are fast — the rebuilt Go binary is not needed because
`gastro dev` loads templates from disk on every request (no embed FS in dev
mode).
