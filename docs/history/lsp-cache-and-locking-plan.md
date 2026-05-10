# Plan: tighten LSP cache semantics, scope-skip visibility, and dataMu discipline

**Date:** 2026-05-09
**Status:** Executed. All three phases implemented and verified.
  - Phase 1 (cache invariants): `cacheEntry[T]` type, positive-only caching, `sawSuppressionLines` removed.
  - Phase 2 (walker skip log): `WalkReport` type, per-`runTemplateDiagnostics` log line with 5s dedup.
  - Phase 3 (dataMu audit): lock-protected helpers for all shared maps, `inst.mu`, race tests.
  - Verification: `go test -race ./...` clean, `go vet ./...` clean, audit shadow 0 diagnostics.
**Author:** agent + m+git@andri.dk
**Related:**
- Follow-up to commit `063b132` (`fix(lsp): re-trigger template diagnostics once gopls is ready`)
- Implements R3, R4, R5 from `tmp/lsp-drift-investigation.html`

---

## Why

The investigation behind `063b132` surfaced three structural issues in
the LSP server beyond the immediate dashboard typo bug. They are
related, but each is independently shippable:

- **R3 (cache semantics).** Several caches collapse three states —
  *"we have a positive answer"*, *"we have evidence the answer is
  empty"*, *"we don't yet know"* — into two states (key-present vs
  key-absent). The dashboard bug rode on this: `queryVariableTypes`
  cached a transient empty result and served it back as if it were
  authoritative. The fix landed in `063b132` was a side-channel
  ("had suppression lines"). Sibling caches (`fieldCache`,
  `typeFieldCache`, `componentPropsCache`) have similarly implicit
  contracts — sometimes accidentally safe, sometimes not. Documenting
  + enforcing one invariant kills a class of regressions.
- **R4 (silent skip visibility).** `walker.checkField` correctly bails
  out when `scope.allowedFields == nil` to avoid false positives, but
  emits no signal. That made R1+R2+R3 of the original investigation
  invisible for an unknown amount of time. Without surface telemetry
  the next regression of this class will also ship silently.
- **R5 (dataMu discipline).** `documents`, `goplsDiags`, `templateDiags`,
  `templateDiagsStale`, `templateDiagsRetries` are all accessed under
  `dataMu`. `typeCache`, `fieldCache`, `typeFieldCache`,
  `componentPropsCache`, and `inst.goplsOpenFiles` are not — they're
  read and written from multiple goroutines (main message loop, gopls
  reader goroutine, the new stale-retry goroutine added in `063b132`)
  without synchronisation. The race detector hasn't flagged it
  because real timelines mostly serialise via the message loop, but
  with the new background re-run goroutine the surface area grew.

---

## Scope and non-goals

In scope:
- Documented invariants for every cache map on `*server` and
  `*projectInstance`.
- A small typed wrapper (or sentinel convention) that makes the
  invariant impossible to violate accidentally.
- A single per-URI "type info missing" log line emitted from the walker
  when it falls back to silent-skip mode.
- An audit pass that adds `dataMu` coverage (or per-instance locking)
  for every shared mutable map currently accessed without it.
- A `-race` regression test that exercises the previously-racy paths
  (cache reads from completion/hover concurrent with `didChange`'s
  cache delete).

Out of scope:
- Changing the actual cache strategy (TTLs, LRU, generation counters).
  The current "invalidate on didChange" policy stays.
- Restructuring how diagnostics are scheduled (the `templateDiagsStale`
  retry mechanism from `063b132` stays as-is).
- Anything in `internal/lsp/proxy/` or the gopls subprocess wiring.
- Performance work. We're tightening invariants, not chasing latency.

---

## Current state (audited)

### Caches and their actual contracts

| Map | Owner | Write sites | Read sites | "Empty" semantics | Locked by |
| --- | --- | --- | --- | --- | --- |
| `typeCache[uri][var] = type` | `*server` | `queryVariableTypes` (after fix in `063b132`: only caches non-empty when suppression lines existed) | `queryVariableTypes`, `getCachedFields` | tri-state, side-channel via `sawSuppressionLines` | **none** |
| `fieldCache[uri][var] = []fieldInfo` | `*server` | `getCachedFields` (only on non-nil result) | `getCachedFields` | implicit two-state (nil result skipped) | **none** |
| `typeFieldCache[uri][type] = []FieldEntry` | `*server` | `resolveFieldsViaChain` (only on non-nil probe result) | `resolveFieldsViaChain` | implicit two-state | **none** |
| `componentPropsCache[path] = []StructField` | `*projectInstance` | `resolveAllComponentProps`, `cachedComponentProps` | same + `invalidateComponentPropsCache` | **explicit tri-state** — stores `nil` to mean "tried, no Props" | **none** |
| `goplsOpenFiles[virtualURI] = version` | `*projectInstance` | `syncToGopls`, `probeFieldsViaChain`, `chainedFieldDefinition`, `restoreVirtualFile` | same | not applicable | **none** |
| `documents[uri] = content` | `*server` | handlers | handlers, completion, hover, definition, formatting, diagnostics goroutines | not applicable | `dataMu` ✓ |
| `templateDiags[uri]`, `goplsDiags[uri]` | `*server` | diagnostics, gopls notification handler | publish-merged | not applicable | `dataMu` ✓ |
| `templateDiagsStale[uri]`, `templateDiagsRetries[uri]` | `*server` | diagnostics, gopls notification handler | same | not applicable | `dataMu` ✓ |

### Walker silent-skip sites

`internal/lsp/template/walk.go`:

- `checkField` line ~263: `if scope.allowedFields == nil { return // no type info — skip silently }`
- `resolveRangeScope` and `resolveWithScope` produce `inner` scopes with
  `allowedFields == nil` whenever the typeMap or resolver doesn't yield
  a usable answer. That nil-ness is what `checkField` later sees.

There is no metric, log, or diagnostic emitted in any of those paths.

### Concurrency reality

Three goroutines touch shared maps today:

1. **Main message loop** — driven by the LSP client over stdin. Calls
   `handleDidOpen`, `handleDidChange`, `handleCompletion`, `handleHover`,
   `handleDefinition`, etc. These read `documents`, `typeCache`,
   `fieldCache`, `typeFieldCache`, `componentPropsCache` and mutate them.
2. **gopls reader goroutine** — `proxy.Proxy` dispatches incoming
   gopls messages on this goroutine. `handleGoplsNotification` runs
   here; it acquires `dataMu` for the duration of its work.
3. **Stale-retry goroutine** (added in `063b132`) — spawned when
   `runTemplateDiagnostics` finishes with `stale=true && goplsReady`.
   Sleeps 250ms, reads `documents` under `dataMu.RLock()`, then calls
   `runTemplateDiagnostics` recursively. The recursive call hits
   `queryVariableTypes` (touches `typeCache`) and the resolver (touches
   `typeFieldCache`) without holding `dataMu`.

Real-world races possible today:

- (1) and (3) both call `queryVariableTypes`, contending on `typeCache`.
- (1)'s `handleDidChange` runs `delete(s.typeCache, uri)` while (3) is
  reading or writing the same map.
- (2) and (3) can both modify `inst.goplsOpenFiles` (via probe and
  syncToGopls / restoreVirtualFile).

That nothing has crashed yet means the timing is forgiving, not safe.

---

## Plan

Three independent, testable changes. Land in order; each leaves the
tree green on its own.

### Phase 1 — Cache invariant + typed wrapper (R3)

**Goal.** Every cache map on `*server` and `*projectInstance` follows
one rule: *the map only ever holds positive evidence. Absence means
"ask again."* Negative caching, where we want it for performance, uses
an explicit sentinel value with a documented meaning.

**Implementation.**

1. Introduce a tiny generic helper in `internal/lsp/server/cache.go`:

   ```go
   // cacheEntry distinguishes "we have a real answer" from
   // "we tried, the answer was nothing". Absence from the cache always
   // means "we don't yet know". Positive is the common path; Negative
   // is opted into per cache and documented at the declaration.
   type cacheEntry[T any] struct {
       value    T
       negative bool // true when we cached a definitive empty answer
   }
   ```

   Used only where negative caching is intentional. The default for new
   caches is "store positive only".

2. Walk every cache and pick a contract:

   - `typeCache`: positive-only. Drop the `sawSuppressionLines`
     side-channel introduced in `063b132`; the new contract makes it
     redundant.
   - `fieldCache`: positive-only. Already de facto positive-only; add a
     comment.
   - `typeFieldCache`: positive-only. Same.
   - `componentPropsCache`: tri-state today. Convert to
     `cacheEntry[[]codegen.StructField]`. Stores Negative for
     "component file has no Props"; absence still means "haven't tried".
     The negative entry is purged by the existing
     `invalidateComponentPropsCache` on file change.

3. Adjust callers. The diff on each call site is small — replace the
   bare-map lookups with a helper or with explicit `if entry, ok := ...; ok && !entry.negative`.

4. Add a unit test per cache that asserts the contract:

   ```go
   func TestTypeCache_DoesNotPersistTransientEmpty(t *testing.T) {
       // simulate gopls returning empty hover, ensure no entry is cached
   }
   func TestComponentPropsCache_NegativeEntryHonoured(t *testing.T) {
       // simulate "component has no Props", ensure subsequent calls hit
       // cache instead of re-parsing
   }
   ```

**Acceptance.**

- Every cache field on `*server` / `*projectInstance` has a
  Godoc-level comment stating "stores positive results only" or
  "stores positive + negative (sentinel: ...)".
- The `sawSuppressionLines` heuristic in `queryVariableTypes` is
  removed. `queryVariableTypes` simply does not write to `typeCache`
  when the result is empty.
- Existing diagnostic, completion, hover, definition behaviour is
  byte-identical for the canonical examples (audited via
  `cmd/auditshadow`).

**Risks.**

- Removing the `sawSuppressionLines` shortcut means *every* empty
  result becomes a cache miss next time, not just the gopls-not-ready
  case. For files with truly zero exports this is one extra hover
  per LSP request — negligible.

### Phase 2 — Walker emits a single per-URI skip signal (R4)

**Goal.** When the walker bails out because it had no type info for a
range/with scope, emit one log line per `(URI, run)` so the next time
the type pipeline regresses we have a breadcrumb.

**Implementation.**

1. Add a `skippedScopes int` counter (or a `skippedScopeNames []string`)
   to the `walker` struct. Increment in `checkField` when bailing on
   `nil allowedFields`, and in `resolveRangeScope` /
   `resolveWithScope` when they return an `inner` with no
   `allowedFields`.
2. Expose a method `(*walker) SkipReport() (count int, sample []string)`
   used by `WalkDiagnostics`'s caller.
3. In `runTemplateDiagnostics` (server side), if the walker reports any
   skipped scopes:
   - Log once at `log.Printf` level: `template diagnostics: %d
     range/with scope(s) skipped for %s — gopls type info unavailable
     (sample: %v)`.
   - Do **not** emit an editor diagnostic. We don't want noise; we
     want server-side signal.
4. New unit test asserting that a fixture with `{{ range .X }}{{ .Y }}
   {{ end }}` and no resolver produces a non-zero skip count.

**Acceptance.**

- A new debug log line shows up exactly once per `runTemplateDiagnostics`
  call when scopes are skipped, never otherwise.
- No editor-visible behaviour change (no new diagnostics, no severity
  changes).

**Risks.**

- Log volume during normal operation. Mitigation: only log when count
  > 0; and the diagnostic re-run mechanism from `063b132` already
  stops once `goplsReady` is true and the resolver returns real data.

### Phase 3 — `dataMu` audit + closure (R5)

**Goal.** Every shared mutable map is reachable only from inside
`dataMu` (or a documented `inst`-level mutex). The race detector finds
nothing on a representative concurrent workload.

**Implementation.**

1. **Survey.** Grep every read/write of:
   - `s.typeCache`, `s.fieldCache`, `s.typeFieldCache`,
     `s.documents`, `s.goplsDiags`, `s.templateDiags`,
     `s.templateDiagsStale`, `s.templateDiagsRetries`,
     `inst.componentPropsCache`, `inst.goplsOpenFiles`,
     `inst.components`, `inst.goplsReady`.
   - For each, mark whether the access happens with `dataMu` held
     (write or read), with another lock, or unlocked.
2. **Decide one strategy.** Options:
   - Option A — *expand `dataMu`*. All `*server` maps under `dataMu`;
     all `*projectInstance` maps under a new `inst.mu sync.RWMutex`.
     Largest blast radius, cleanest invariant.
   - Option B — *push helpers*. Add `s.getTypeCached(uri, var)` /
     `s.setTypeCached(...)` helpers in `cache.go` that take the lock
     internally. Smaller surface area.
   - **Recommended: Option B**, with `inst.mu` added for the
     `componentPropsCache` / `components` / `goplsOpenFiles` /
     `goplsReady` cluster.
3. **Implement** the helpers and migrate all call sites. Tag each map
   field with a comment naming its lock:
   ```go
   // typeCache is keyed by gastroURI; protected by dataMu.
   typeCache map[string]map[string]string
   ```
4. **Test.** New `cache_race_test.go` in `internal/lsp/server/`:
   - Spawns N goroutines that alternate between
     `queryVariableTypes(uri)` and `delete(typeCache, uri)`-equivalent
     code paths against a stub gopls.
   - Run with `-race`. Must come back clean.
   - Cover the same shape for `fieldCache`, `typeFieldCache`,
     `componentPropsCache`, `goplsOpenFiles`.

**Acceptance.**

- `go test -race ./internal/lsp/...` passes on a workload that
  intentionally interleaves `didChange` (cache invalidation) with
  completion/hover/definition (cache reads).
- Every shared map field carries a Godoc comment naming its lock.

**Risks.**

- Lock contention. `dataMu` is currently held for short critical
  sections; expanding usage shouldn't change that, but if helpers end
  up calling gopls under the lock we'd block other handlers. The
  helpers must release the lock before doing any I/O.
- Deadlock potential when helpers acquire `inst.mu` while holding
  `dataMu`. The plan: never nest. If a code path needs both, document
  the order and assert it in tests.

---

## Verification (combined, run at end of Phase 3)

1. `go test -race -timeout 300s ./...` — full suite stays green.
2. `go vet ./...`.
3. `mise run audit` — shadow workspace audit on every example, must
   stay diagnostic-clean.
4. New tests added in each phase pass under `-race -count=5`.
5. Manual smoke: open `examples/dashboard/components/dashboard.gastro`
   in an editor with the LSP attached, confirm:
   - typo on `.Agent` (single-letter typo) flagged within ~1s.
   - go-to-definition on `.Agent.Name` (a chained sub-field) jumps to
     the `mock.Agent` declaration.
   - hover on the same chain shows `string`.
6. Targeted re-run of the original repro:
   `TestLSP_RangeOverLocalTypeDiagnostic` and
   `TestLSP_ChainedFieldDefinition_InsideRange` still pass.

---

## Estimated effort

| Phase | Code | Tests | Reviewing |
| --- | --- | --- | --- |
| 1 — cache invariants | ~150 lines | ~80 lines | medium (touches many call sites) |
| 2 — walker skip log | ~40 lines | ~30 lines | small |
| 3 — dataMu audit | ~120 lines | ~120 lines (race tests) | medium-high |

Total: approximately one focused day. Phases are independently
revertable.

---

## Pre-existing issues to surface separately (not in this plan)

These were spotted during the audit but don't belong here. Tracked for
follow-up:

1. The early TODO comment in `docs/contributing.md` (`The LSP has a
   known issue: gopls proxy completions and diagnostics are not yet
   working reliably`) is now misleading — the issue described is the
   one fixed by `063b132` and earlier commits. Update once this plan
   lands.
2. `internal/lsp/server/server.go` references `.opencode/plans/lsp-debugging.md`
   which no longer matches the current debugging story.

---

## Open questions

1. **Should `cacheEntry[T]` live in `internal/lsp/server/cache.go` or
   in a smaller internal package?** Recommend: keep it in `server/`
   for now. Promote to a shared package only if a second consumer
   appears.
2. **Phase 3 — is `inst.mu` overkill?** Alternative: serialise all
   `*projectInstance` access through `dataMu`. Simpler but couples
   per-project state to the global lock. Recommend the per-instance
   mutex; the contention model is per-project anyway.
3. **Walker skip-log granularity.** Per `runTemplateDiagnostics` call
   (chatty during typing) vs per URI per session (quieter, but loses
   re-run information). Recommend: per call, behind a guard that
   suppresses repeats with the same `(URI, count, sample)` tuple
   within a short window (e.g. 5s).
