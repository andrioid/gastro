## TODO

## Backlog

- [ ] LSP should flag component props when missing or invalid
- [ ] LSP should show component signature
- [ ] SSE: Type-safe component rendering via `gastro.Render` struct (see `.opencode/plans/sse-render.md`)
- [x] Server-Side-Events response after initial render for pages. For use with DataStar and HTMX
  - [x] Generic SSE runtime helper (`pkg/gastro/sse.go`)
  - [x] Datastar sugar subpackage (`pkg/gastro/datastar/`)
  - [x] Documentation (`docs/sse.md`) and example app (`examples/sse/`)
- [x] Component composition -- components can now use other components via `use` declarations

## Done