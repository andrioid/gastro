// Package lspdemo wires the embedded demo .gastro file into the
// homepage live-LSP section. The .gastro source is //go:embed-ed at
// build time so the prod binary has no runtime dependency on the
// filesystem; the package exposes the raw source plus a stable URI to
// drive textDocument/didOpen requests against the shared LSP client.
//
// The renderer (frontmatter + body views, hoverable identifier spans,
// diagnostic squiggles) is added in a follow-up step. This file
// currently only owns the source-of-truth: the embedded snippet and
// its identity.
package lspdemo

import _ "embed"

//go:embed example.gastro
var source string

// Source returns the embedded demo .gastro source verbatim. Callers
// pass this to lspclient.Client.OpenFile as the textDocument text.
func Source() string {
	return source
}

// Filename is the basename used everywhere the demo file is named
// (panel titles, URI, hover requests). Kept centralised so the
// "greeting.gastro" string only exists once.
const Filename = "greeting.gastro"
