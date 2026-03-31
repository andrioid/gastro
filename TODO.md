## TODO

## Backlog

- [ ] Live reload in dev mode: inject JS snippet into HTML responses, SSE endpoint `GET /__gastro/reload`, file-based signal (`.gastro/.reload`) written by CLI after regeneration, browser reconnects with backoff on disconnect
- [ ] `gastro dev` smart restart: wire `ClassifyChange`/`DetectChangedSection` (already implemented in `internal/watcher/`) into the CLI so template-only changes skip the rebuild+restart cycle (templates already load from disk in dev mode)
- [ ] `gastro new` scaffold command: generate a minimal project skeleton (pages/, components/, static/, main.go, go.mod)
- [ ] Unify page and component internals: a page is conceptually a component with HTTP context; refactor codegen to use a single render mechanism with a thin HTTP adapter for pages, reducing duplication in `handlerTmpl`/`componentTmpl`
- [x] LSP should flag component props when missing or invalid
- [x] LSP should show component signature
- [x] SSE: Type-safe component rendering via `gastro.Render` struct
- [x] Server-Side-Events response after initial render for pages. For use with DataStar and HTMX
  - [x] Generic SSE runtime helper (`pkg/gastro/sse.go`)
  - [x] Datastar sugar subpackage (`pkg/gastro/datastar/`)
  - [x] Documentation (`docs/sse.md`) and example app (`examples/sse/`)
- [x] Component composition -- components can now use other components via `use` declarations

## Done