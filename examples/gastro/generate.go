// Build-artifact rebuild directives for the gastro-website example.
//
// `go generate ./...` from this directory will:
//
//  1. Compile Tailwind CSS source (tailwind.css) into the embedded
//     stylesheet at static/styles.css. Output is minified for parity
//     with what ships in the binary.
//  2. Run `gastro generate` to regenerate the .gastro/ package tree
//     against any .gastro template changes.
//
// `go generate` runs directives top-to-bottom within a single file, so
// the freshly-built CSS is on disk before gastro generate's //go:embed
// snapshot of static/ is taken.
//
// Both commands must be on PATH. mise satisfies this once contributors
// run `mise install` from the repo root (see mise.toml).
//
// CI gate: `go generate ./... && git diff --exit-code` from this
// directory catches drift in either the CSS or the .gastro/ tree.

//go:generate tailwindcss -i tailwind.css -o static/styles.css --minify
//go:generate gastro generate

package main
