# Decisions

- **2026-03-31** (m+git@andri.dk) SSE support implemented as a generic `pkg/gastro/sse.go` helper (framework-agnostic) with a separate `pkg/gastro/datastar/` subpackage for Datastar-specific sugar. No external dependencies added -- SSE protocol is ~90 lines. No compiler/codegen changes needed; SSE endpoints are plain Go handlers registered alongside gastro routes.
- **2026-03-31** (m+git@andri.dk) Component composition enabled -- components can now `use` other components. Fixed `componentTmpl` in `generate.go` to wire up FuncMap for `use` declarations, mirroring the existing `handlerTmpl` pattern. Parser and template transformer already supported this; only code generation was missing.
