# Drift fixture corpus

Each `.gastro` file in this directory exercises one shape of dict-key
validation. The companion test (`internal/codegen/drift_test.go`)
runs every fixture through *both* the codegen-side validator
(`ValidateDictKeysFromAST`, line-only output via `ValidateDictKeys`)
and the LSP-side diagnostic (`lsptemplate.Diagnose`), normalises the
results, and asserts they agree on which dict keys are valid.

This is the structural enforcement of Phase 2's "single source of
truth" decision: as long as the LSP keeps delegating to codegen, both
paths produce identical verdicts (with severity-1 LSP warnings being
the only intentional asymmetry — codegen's `EmitMissingProps` defaults
to off so `gastro generate --strict` doesn't block on partial dicts).

## Adding a new fixture

1. Create `<name>.gastro` describing the test case.
2. If the case is a **page**, add a sibling `<name>.layout.gastro`
   that defines the layout component so prop validation has a schema
   to cross-check against. The test loader treats `<name>.layout.gastro`
   as the imported component when present.
3. Re-run `go test ./internal/codegen/...`. The drift test
   auto-discovers every `.gastro` file in this directory; no
   registration step is needed.

Conventions:

- Component aliases match the layout file's PascalCase name (e.g.
  `Layout` for `<name>.layout.gastro`). Keep it simple — the corpus
  is for diagnostic equivalence, not for exercising aliasing.
- Fixtures should produce **at least one** diagnostic (the test
  doesn't filter no-op cases, but they're not informative). If you
  want a "clean" case to assert no false positives, add a
  `<name>-clean.gastro` and assert zero diagnostics in a dedicated
  test, not the corpus.
