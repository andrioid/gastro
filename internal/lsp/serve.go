package lsp

import "github.com/andrioid/gastro/internal/lsp/server"

// Serve starts the gastro LSP server on stdin/stdout.
// The version string is reported in the initialize response.
func Serve(version string) {
	server.Run(version)
}
