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
watcher and server separately:

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

```toml
# mise.toml (project root)

[tasks."dev:gastro"]
description = "Watch .gastro sources and regenerate on change"
run = """
  cd internal/web
  watchexec -e gastro -- sh -c 'gastro generate && touch .gastro/.reload'
"""

[tasks."dev:server"]
description = "Run the app in dev mode"
run = """
  cd internal/web
  GASTRO_DEV=1 go run ../../cmd/myapp --repo ../..
"""

[tasks.dev]
description = "Start the full dev loop"
depends = ["dev:gastro", "dev:server"]
```

The `cd internal/web` is required because `GASTRO_DEV_ROOT` tells gastro's
_runtime_ where to find templates, but `gastro generate` must still be run from
the gastro project root so it can find `components/`, `pages/`, and `static/`.

If your process can always be started from the gastro project root, the
`cd` + `GASTRO_DEV_ROOT` dance is unnecessary.

## Environment variables

| Variable | Default | Description |
|---|---|---|
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
