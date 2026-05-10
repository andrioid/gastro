# `gastro watch` Go-watch Scope — Plan (v1)

> Status: **✅ shipped 2026-05-03 in `cc177ed`** (`feat(watch): auto-detect Go module root for *.go watching`).
> Author: 2026-05-03, surfaced during the git-pm migration analysis.
> Builds on: `tmp/two-stories-plan-v2.md` (v2.1, shipped same day).
> Related friction: git-pm `tmp/feedback-from-git-pm-project.md` §B7
>   (specifically "B7a + the new gap surfaced post-migration").
>
> **Deltas from plan-as-written:**
> - Flag named `--watch-root` (plan said `--watch-go-from`).
> - Q2 fallback is **silent**, not warn-and-fall-back as the plan leaned.
> - Q6 (10k-file startup warning) **not implemented**. The `.git` / `$HOME`
>   walk-up boundary that did ship likely obviates it; revisit if a real
>   monorepo hits the case.
> - Loose ends not verified: `docs/getting-started-library.md` paragraph
>   and DECISIONS.md entry (Phase 5d) — `dev-mode.md` was updated.

## 0. What this plan covers

`gastro watch` (shipped today) watches `*.go` files anywhere under
`--project` / `GASTRO_PROJECT` so library-mode users editing
`internal/web/handlers/foo.go` get auto-restart. But the watch root is
**bound to the gastro project root**, which doesn't match real
embedded-package layouts where Go code lives at multiple levels of the
module tree:

```
git-pm/                       ← go.mod root
  cmd/pm/main.go              ← edits NOT watched today
  internal/git/*.go           ← edits NOT watched today
  internal/pmcore/*.go        ← edits NOT watched today
  internal/web/               ← --project (gastro tree)
    handlers/*.go             ← edits ARE watched today
    pages/                    ← .gastro auto-routed
    components/               ← .gastro components
```

This plan decouples the **`.gastro` watch root** (where pages/components/
static live) from the **Go watch root** (where `*.go` is monitored), so
library-mode users get auto-restart for any change inside their Go
module without having to compose `gastro watch` with a second runner.

The fix is small (one method's plumbing in `internal/devloop`), but the
semantics warrant a plan because the exclude-set behaviour needs to be
right for nested `.gastro/` trees and the auto-detect heuristic has
edge cases worth pinning down before writing code.

## 1. Goals

1. A library-mode user editing **any `*.go` file inside their Go
   module** sees `gastro watch` restart their binary.
2. The `.gastro` / `.md` / `static/**` watch surface stays anchored at
   the gastro project root (no behaviour change there).
3. The hardcoded exclude set (`.gastro/`, `vendor/`, `node_modules/`,
   `.git/`, `tmp/`) **continues to work for nested trees** —
   `internal/web/.gastro/` must still be excluded when the Go walk
   root is the module root, not the gastro project root.
4. Default behaviour resolves the right thing **without flags**, so
   the migration story for git-pm-shaped projects is "just point
   `gastro watch` at your gastro project; we figure out the rest."
5. Power users still get an escape hatch via flag if the auto-detect
   picks the wrong scope.

## 2. Anti-goals

- **Watching files outside the user's Go module.** A monorepo with two
  Go modules under one repo root should see only the relevant module's
  `*.go` files. Walking from the repo root would pull in unrelated
  modules' files and pollute restarts.
- **Re-architecting `gastro dev` (framework mode).** Framework projects
  have a single Go file (`main.go` from the scaffold) at the project
  root, and the scaffold's `gastro dev` rebuilds-and-restarts via `go
  build .` on `.gastro` frontmatter changes. Whether `gastro dev`
  should also watch `*.go` is a separate consideration (see §10).
- **Re-introducing process-group concerns for build commands.** Out of
  scope; the build-cancellation risk #2 from the two-stories plan
  stands as-is.
- **Adding a third watch dimension.** Two roots (gastro project, Go
  module) is enough. We don't add separate roots for static/, markdown,
  etc.
- **A `tools.go`-style approach** that imports the codegen as a Go
  package. That's a different friction (A1 in the git-pm inventory)
  and is not blocked by this plan.

## 3. Open questions

These need a decision before implementation starts. I have a leaning on
each one but flagging them explicitly.

| # | Question | Leaning |
|---|---|---|
| Q1 | **Auto-detect, flag, or both?** Walk up from `--project` looking for the nearest `go.mod`, or expect the user to pass `--watch-go-from PATH`, or default to auto-detect with a flag override? | Both: auto-detect default + `--watch-go-from PATH` override. Zero-config for the canonical case; explicit control when the heuristic is wrong. |
| Q2 | **What if no `go.mod` is found** walking up from `--project`? Fall back silently to `--project`? Error? Warn-and-fall-back? | Warn-and-fall-back. Matches Phase 3's empty-pages warning pattern. |
| Q3 | **Should the `--exclude` flag be relative to the gastro project root or the Go watch root?** Today they're conflated; if the roots diverge, this is ambiguous. | Relative to the **Go watch root**, since `--exclude` only affects Go-file watching. Document explicitly. |
| Q4 | **Should the auto-detected Go root respect `replace ... => ../something` in `go.mod`?** A library project might `replace github.com/andrioid/gastro => ../gastro`; should we also watch the replaced path's `*.go`? | No. Watch only the user's own module. Replaced paths usually point at vendored or in-development deps the user doesn't want auto-restarting on. |
| Q5 | **Should `gastro dev` (framework mode) inherit this change?** Today `WatchGoFiles=false` for `gastro dev`. Should the auto-detected Go-watch root replace that, so framework projects also restart on `main.go` edits? | **Out of scope for this plan.** Defer to a separate decision. The two-stories plan deliberately kept `gastro dev` minimal; expanding its watch surface should be its own conversation. |
| Q6 | **Is the performance cost of walking a whole module tree on every poll tick acceptable?** For most repos it's a few thousand files, fine. For a massive monorepo it could be ~100k files. | Cap with a startup file-count check + warning if the count exceeds a threshold (~10k). Document `--watch-go-from` as the escape hatch. Optimisation (incremental walks, fsnotify) is a follow-up. |

## 4. Resolved decisions

(None yet. Will be filled in after Q1–Q6 above are answered.)

## 5. Work plan

Sized **M** end-to-end, dominated by the test sweep. Single phase; no
inter-phase dependencies.

### 5a. Decouple Go-watch root in `internal/devloop`

**Files:**
- Modified: `internal/devloop/devloop.go` — `Config` gains a
  `GoWatchRoot string` field (empty = use auto-detect).
- Modified: `internal/devloop/watcher.go` — `watcherState.runLoop`
  splits the previously-conflated `root` into `gastroRoot` (used for
  pages/components/static/markdown) and `goRoot` (used for `*.go`).
- Modified: `internal/devloop/watcher.go` — `collectGoFiles` and
  `seedGoFiles` take the `goRoot` argument explicitly.
- New: `internal/devloop/gomod.go` — `findModuleRoot(start string)
  (string, error)` walks up from `start` until it finds a `go.mod`;
  returns the directory or an error if none found before reaching
  the filesystem root.

**Behaviour:**
- `Config.GoWatchRoot` empty + `WatchGoFiles=true` → run
  `findModuleRoot(ProjectRoot)`; on success, use that as the
  Go-watch root; on failure, log a warning and fall back to
  `ProjectRoot` (Q2 leaning).
- `Config.GoWatchRoot` set + `WatchGoFiles=true` → use that path
  verbatim (no auto-detect). Resolves user-supplied
  `--watch-go-from`.
- `Config.GoWatchRoot` set + `WatchGoFiles=false` → no-op (the
  field is ignored when Go-file watching is disabled).

**Exclude handling (Q3):** the existing `defaultGoExcludes` and
`Config.ExtraExcludes` are matched as prefixes against the walk
output relative to the Go-watch root, not the gastro project root.
This means:

- `vendor/` at the module root excludes
  `<module-root>/vendor/...` ✓
- `.gastro/` excludes any directory whose path component is
  `.gastro` (so `<module-root>/internal/web/.gastro/...` is
  caught even when the gastro tree is nested) — this requires
  the exclude check to match path **components**, not just
  string prefixes. Today the implementation is prefix-only;
  this plan changes it to component-wise.
- `--exclude internal/scratch/` from the CLI is matched relative
  to the Go-watch root, so users who passed paths relative to
  the gastro project root may need to update them. Document the
  break.

**Tests** in `internal/devloop/`:

- `TestFindModuleRoot_DirectAncestor` — gastro project at
  `<tmp>/internal/web/`, `go.mod` at `<tmp>/`; expect `<tmp>/`.
- `TestFindModuleRoot_SameDir` — gastro project IS the module
  root; expect that same dir.
- `TestFindModuleRoot_NoModule` — no `go.mod` anywhere up the
  tree; expect a sentinel error or empty string (decision Q2).
- `TestFindModuleRoot_StopsAtFirstAncestor` — nested modules
  (a `go.mod` at `<tmp>/internal/` AND `<tmp>/`); expect the
  inner one (matches Go's module-resolution rule).
- `TestRun_GoWatchRoot_Auto` — set `WatchGoFiles=true` with no
  `GoWatchRoot`; project at `<tmp>/internal/web/`,
  `cmd/myapp/main.go` at `<tmp>/cmd/myapp/`; expect edits to
  `cmd/myapp/main.go` to trigger a restart.
- `TestRun_GoWatchRoot_Explicit` — set `GoWatchRoot` to a
  specific path; expect edits within it watched, edits outside
  it ignored.
- `TestRun_NestedGastroExcluded` — a project at
  `<tmp>/internal/web/`, `go.mod` at `<tmp>/`. Edits to
  `<tmp>/internal/web/.gastro/foo.go` (a generated file) MUST
  NOT trigger a restart even though `.go` and the `.gastro/`
  exclude is component-matched.
- `TestRun_GoWatchRoot_NoModule_Warns` — gastro project in a
  dir with no `go.mod` ancestor; expect a warning to stderr
  and fall-back to ProjectRoot (assuming Q2 leaning).
- `TestRun_GoWatchRoot_Replace_Ignored` — module has a
  `replace ... => ../something` directive; edits to the
  replaced path do NOT trigger a restart (Q4 leaning).
- `TestRun_GoWatchRoot_FileCountWarning` — synthetic project
  with >10k `*.go` files; expect a startup warning suggesting
  `--watch-go-from` (Q6 leaning).

**Acceptance:** `mise run test` (`-race`) green; existing
`TestRun_WatchGoFiles_*` tests in `devloop_test.go` continue to
pass unchanged (they don't exercise the new path because
`GoWatchRoot` defaults to empty and they never set up `go.mod`
files, so the auto-detect fall-back kicks in).

### 5b. Surface in `gastro watch`

**Files:**
- Modified: `cmd/gastro/watch.go` — `parseWatchArgs` gains
  `--watch-go-from PATH`; `runWatchLoop` plumbs it into
  `devloop.Config.GoWatchRoot`.
- Modified: `cmd/gastro/watch.go` — `watchUsage` constant gains
  the new flag entry.

**Tests** in `cmd/gastro/`:

- New sub-case in `TestParseWatchArgs`: `--watch-go-from PATH`
  parses, both long and `--watch-go-from=PATH` forms, missing
  value reports flag name.

**No new integration tests** beyond what 5a covers — the
integration tests in `watch_integration_test.go` use a single
`go.mod` at the project root, which is exactly the auto-detect
sweet spot. Adding a synthetic embedded-package layout for
integration tests is doable but would mostly retest what 5a's
unit tests cover. Document this trade in the test file.

### 5c. Documentation

**Files:**
- Modified: `docs/getting-started-library.md` — add a paragraph
  to the "The dev loop" section explaining the auto-detect and
  the `--watch-go-from` escape hatch. Show the canonical git-pm-
  shaped `gastro watch` invocation explicitly.
- Modified: `docs/dev-mode.md` — update the "Smart rebuild vs
  reload" table footnote about Go-file watching scope; add a
  "Go-watch root resolution" subsection explaining the
  auto-detect + flag override.
- Modified: `cmd/gastro/watch.go` `watchUsage` const — already
  edited in 5b but worth calling out separately because it's
  user-facing.

**No DECISIONS.md entry yet** — it ships as part of phase 5d
below.

### 5d. Verification + DECISIONS.md

Same shape as the two-stories plan's Phase 6: full verification
suite (`mise run test`, `mise run lint`, `bash scripts/verify-bootstrap`,
`gastro check` × 4 examples, manual smoke against `examples/blog` for
no-regression, manual smoke against a hand-crafted git-pm-shaped
fixture for the new path) plus a DECISIONS.md entry recording:

- The Go-watch-root decoupling
- Auto-detect heuristic (nearest-`go.mod` walking up)
- The `--watch-go-from` flag
- Q5 explicit deferral (`gastro dev` not changed)
- Q6 explicit deferral (perf optimisation if file count exceeds
  threshold)
- Strategic-stance link to the two-stories plan's anti-goal #6
  ("never recommend a specific third-party runner")

## 6. Acceptance criteria

A user reading docs alone (no code spelunking) can:

1. Drop `gastro watch` into a git-pm-shaped project (gastro tree at
   `internal/web/`, `cmd/<app>/main.go` at the module root) without
   any flag beyond `--project` / `GASTRO_PROJECT`, and have edits
   to **any `*.go` file in the module** trigger a restart.
2. See a clear startup line listing what's watched
   (`gastro: watching .gastro tree: <path>` / `gastro: watching Go
   sources from module root: <path>`) so the resolved scope is
   self-documenting.
3. Override the auto-detect via `--watch-go-from` for unusual
   layouts (multi-module repos, gastro tree outside its Go
   module, etc.) without reading the source.
4. Edit a generated file in a nested `.gastro/` (e.g.
   `internal/web/.gastro/routes.go`) without triggering a restart
   — the exclude set works component-wise, not just at the walk
   root.

## 7. Risks

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| 1 | **Component-wise exclude matching breaks existing `--exclude foo/` users.** Today's prefix-match treats `foo/` as "exclude paths starting with `foo/`". Component-match treats it as "exclude paths whose first component is `foo`". For most users these coincide; the divergence is rare. | Low | Document in the DECISIONS entry. The `gastro watch` flag is two days old; no in-the-wild adopters yet. |
| 2 | **Auto-detect picks the wrong `go.mod`** in a monorepo where `internal/web/` is its own module separate from `cmd/pm/`. | Low | The `--watch-go-from` flag is the escape. Document the heuristic explicitly so users understand what's happening. |
| 3 | **Performance regression** on monorepos. Walking a 50-package module tree every 500ms is 25× more file stats than walking just `internal/web/`. | Medium | Q6 startup warning at 10k files; document the flag escape. Real fix (fsnotify or incremental walks) is a follow-up plan. |
| 4 | **Symlink loops in the module tree** (a `tmp/checkout-of-itself` situation). `filepath.Walk` doesn't follow symlinks by default, but if a user has a real circular dir-tree this could loop. | Very low | `filepath.Walk` skips symlinks — the existing behaviour holds. No new code needed. |
| 5 | **Auto-detect interaction with `GOFLAGS=-modfile=alt.mod`** (uncommon, but real). The walker looks for literal `go.mod`, not whatever `GOFLAGS` is pointing at. | Very low | Document. Users with non-default modfile paths can use `--watch-go-from`. |
| 6 | **The `replace ... => ../something` exclusion (Q4)** misses a real use case where a user is co-developing two modules and DOES want both watched. | Low | Document. The escape is explicit `--watch-go-from` with the parent of both modules, plus `--exclude` for the unwanted half. |

## 8. Pre-existing issues identified during planning

(None new. The git-pm friction inventory in
`tmp/feedback-from-git-pm-project.md` is the source for what's in
scope; A0/A1/A2/A3-A8 and B1-B6 explicitly stay on git-pm's list and
are not addressed here.)

## 9. Out of scope

- **`gastro dev` watching `*.go`** (Q5). Separate decision; the
  framework mode's "no flags, scaffold-default" stance interacts with
  this in non-obvious ways.
- **Replaced-module watching** (Q4 lean). Co-development of two
  modules out of scope; users compose with their own runner.
- **fsnotify / incremental walks.** Today's polling-watcher model
  scales linearly with file count; that's fine for the typical case
  and the Q6 startup warning catches the pathological case. Real
  perf work is a separate plan.
- **Multi-module monorepo "watch all modules"** mode. The plan
  watches one module (the nearest ancestor of `--project`).
- **Windows support.** Carries the same anti-goal as `gastro watch`
  v1: Unix-only.

## 10. Estimate

| Phase | Size | Notes |
|---|---|---|
| 5a | M | Devloop plumbing + 9 unit tests; component-wise exclude is the trickiest piece |
| 5b | XS | Flag plumbing + 1 sub-test |
| 5c | S | Two doc files updated; new subsection in dev-mode.md |
| 5d | XS | DECISIONS entry + verification |

Total: roughly **0.5–1 day** of focused work, dominated by the unit
test sweep in 5a.

## 11. Implementation hints (for whoever picks this up)

A few things I'd save the next session:

1. **`watcher.go`'s exclude check** is currently in `collectGoFiles`:
   ```go
   for _, ex := range excludes {
       if rel == strings.TrimSuffix(ex, "/") || strings.HasPrefix(rel+"/", ex) {
           ...
       }
   }
   ```
   Switch to component-wise: split `rel` by `/`, check if any
   component matches the exclude (after stripping its trailing slash).
   `vendor/` matches `vendor`, `internal/vendor`, and
   `internal/web/vendor/foo/bar.go` (last one is the new behaviour
   that fixes nested `.gastro/`).

2. **`findModuleRoot` is ~15 lines** — walk up from `start`, calling
   `os.Stat(filepath.Join(dir, "go.mod"))` until found or `dir`
   reaches its own parent (filesystem root). No need for
   `golang.org/x/mod/modfile` parsing — just file existence.

3. **The "warning at 10k files" check** belongs in
   `newWatcherState` (not the poll loop) so it fires once at
   startup. Use the count of `*.go` files returned by the initial
   `collectGoFiles` call.

4. **The startup log line** suggested in §6 acceptance #2 should
   probably go in `warnEmptySources` (Phase 3 of the two-stories
   plan) to keep all the "what am I watching?" lines in one place.
   Rename it `logWatchSurface` or similar.

5. **Test fixture for `findModuleRoot_StopsAtFirstAncestor`** needs
   nested `go.mod` files in a tempdir. `t.TempDir()` works for this
   one (no `go build` involved); the integration tests' `tmp/test-
   projects/` workaround is only for tests that actually exercise
   the Go toolchain.

## 12. Migration impact for git-pm

(Concrete proof-point that motivated this plan; left here so the
next session can verify against the same target.)

After this ships, git-pm's `mise.toml` becomes:

```toml
[env]
GASTRO_PROJECT  = "{{ config_root }}/internal/web"
GASTRO_DEV_ROOT = "{{ config_root }}/internal/web"

[tasks.dev]
description = "Dev loop with hot reload"
dir = "{{ config_root }}/internal/web"
run = """
gastro watch \
  --build 'go build -o ../../tmp/pm ../../cmd/pm' \
  --run   '../../tmp/pm --repo ../.. web'
"""
```

…and edits to `cmd/pm/*.go`, `internal/git/*.go`,
`internal/pmcore/*.go`, `internal/web/handlers/*.go` (etc.) all
trigger restarts. The `--build` step and the `--run` paths are still
relative-to-`internal/web/` because `gastro watch` chdir's there —
that's the same dance git-pm does today and is preserved verbatim.

What's left on git-pm's friction list after this lands:
- A0 (dict key checking) — biggest correctness gap, separate plan
- A1 (CLI version pin) — partially addressed by `go tool gastro`;
  git-pm should adopt `go get -tool github.com/andrioid/gastro/cmd/gastro`
- A2 (committed `.gastro/` tree) — Phase 1's skip-if-equal already
  reduced churn; the underlying "`go install` doesn't run `go
  generate`" friction stands
- A3–A8, B1–B6 — unchanged, on their own follow-up tracks

---

## Iteration scratchpad

(Empty. Resolve Q1–Q6 in §3 here as decisions are made.)
