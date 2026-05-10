// Build-artifact rebuild directives for the gastro-website example.
//
// `go generate ./...` from this directory will:
//
//  1. Compile Tailwind CSS source (tailwind.css) into the embedded
//     stylesheet at static/styles.css. Output is minified for parity
//     with what ships in the binary.
//  2. Run `go tool gastro generate` to regenerate the .gastro/ package
//     tree against any .gastro template changes. The `go tool gastro`
//     form resolves through the `tool` directive in go.mod, so we
//     don't need a `gastro` binary on PATH (CI has the mise-managed
//     tailwindcss binary on PATH but doesn't separately install
//     gastro — the tool directive covers that case).
//
// `go generate` runs directives top-to-bottom within a single file, so
// the freshly-built CSS is on disk before gastro generate's //go:embed
// snapshot of static/ is taken.
//
// CI gate: `go generate ./... && git diff --exit-code` from this
// directory catches Tailwind CSS drift. The .gastro/ tree is
// gitignored, so codegen drift is covered separately by `gastro check`
// where it runs.

//go:generate tailwindcss -i tailwind.css -o static/styles.css --minify
//go:generate go tool gastro generate

package main
