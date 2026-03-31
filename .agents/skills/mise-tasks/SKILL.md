---
name: mise-tasks
description: Create and manage mise file tasks in the scripts/ directory
---

## Context

This project uses [mise](https://mise.jdx.dev) for task running. Tasks are defined as
executable scripts in the `scripts/` directory (configured via `[task_config]` in `mise.toml`).

## File Task Convention

- Tasks live in `scripts/` as executable bash scripts (no file extension)
- Task grouping uses subdirectories: `scripts/build/vscode` becomes `mise run build:vscode`
- Each script must have a shebang and `#MISE` metadata comments

### Template

```bash
#!/usr/bin/env bash
#MISE description="Short description of what this task does"
#MISE sources=["optional/glob/**/*.ext"]
#MISE outputs=["optional/output/path"]
#MISE depends=["optional:dependency"]

set -euo pipefail

# task implementation here
```

### Metadata comments

All `#MISE` directives go at the top of the file, after the shebang:

- `description` (required) - shown in `mise tasks` output
- `sources` - file globs; if set with `outputs`, mise skips re-runs when sources haven't changed
- `outputs` - output files for staleness checking
- `depends` - tasks that must complete first
- `alias` - short name for `mise run <alias>`
- `dir` - override working directory (default: project root)

### Task Grouping

Subdirectories under `scripts/` create colon-separated task names automatically.
Use this to organise related tasks by domain or action:

```
scripts/
├── build/
│   ├── vscode        # mise run build:vscode
│   └── cli           # mise run build:cli
├── link/
│   └── vscode        # mise run link:vscode
└── test/
    ├── unit          # mise run test:unit
    └── integration   # mise run test:integration
```

Group by what the task *does* (build, test, link, deploy) rather than what it
targets. This keeps the task list scannable in `mise tasks` output since tasks
are sorted alphabetically by name.

### Default Tasks

A file named `_default` inside a group directory becomes the bare group command.
This is useful when a group has an obvious "main" action:

```
scripts/
└── test/
    ├── _default      # mise run test     (runs this)
    ├── unit          # mise run test:unit
    └── integration   # mise run test:integration
```

The `_default` task typically either runs all sub-tasks via `depends`, or
implements the most common case directly:

```bash
#!/usr/bin/env bash
#MISE description="Run all tests"
#MISE depends=["test:unit", "test:integration"]

set -euo pipefail
```

### Naming rules

- Use lowercase, no extensions
- Group related tasks in subdirectories (e.g. `scripts/build/`, `scripts/link/`)
- The colon-separated task name mirrors the directory structure
- Use `_default` for the group's primary/aggregate action

### Checklist

1. File must be executable (`chmod +x`)
2. Must start with `#!/usr/bin/env bash`
3. Must include `set -euo pipefail`
4. Must have at least `#MISE description="..."`
5. Verify with `mise tasks` that the task appears
6. Do NOT run the task with `mise run` unless it supports a `--dry-run` flag
