# Gastro for Zed

Language support for [Gastro](https://github.com/andrioid/gastro) `.gastro`
files in the [Zed editor](https://zed.dev).

## How the LSP is launched

Unlike the VSCode and Neovim integrations, the Zed extension does **not**
read the `gastro` CLI from your `PATH`. Instead, it downloads a tagged
release binary from
[`github.com/andrioid/gastro/releases`](https://github.com/andrioid/gastro/releases)
matching your OS/architecture and runs it as the language server.

This means:

- You don't need to install gastro globally for the Zed extension to work.
- The way you install gastro for your own command-line use (`mise`,
  `go install`, or a per-project `go tool` directive in `go.mod`) has no
  effect on the Zed integration.
- The LSP version is pinned by the extension version, not by your project.

## Features

Inherits the full feature set of `gastro lsp`: syntax highlighting (via
the bundled tree-sitter grammar), completions, hover, diagnostics, go to
definition, and document formatting. See the project README for the
authoritative list.

## Source

The extension is a Rust WASM crate; see `src/lib.rs` for the binary
download and launch logic.
