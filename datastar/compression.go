package datastar

import (
	sdk "github.com/starfederation/datastar-go/datastar"
)

// CompressionOption configures [WithCompression].
type CompressionOption = sdk.CompressionOption

// BrotliOption configures [WithBrotli].
type BrotliOption = sdk.BrotliOption

// GzipOption configures [WithGzip].
type GzipOption = sdk.GzipOption

// WithCompression compresses the stream with an encoding negotiated from the
// request's Accept-Encoding header and sets Content-Encoding accordingly.
// Without compressor options the SDK offers Brotli, zstd, gzip and deflate,
// preferring them in that order (server priority).
//
// Long-lived streams that repeatedly morph large fragments compress very
// well, because the compressor's window spans events.
func WithCompression(opts ...CompressionOption) SSEOption {
	return sdk.WithCompression(opts...)
}

// WithBrotli offers Brotli compression.
func WithBrotli(opts ...BrotliOption) CompressionOption { return sdk.WithBrotli(opts...) }

// WithBrotliLevel sets the Brotli quality (0–11).
func WithBrotliLevel(level int) BrotliOption { return sdk.WithBrotliLevel(level) }

// WithGzip offers gzip compression.
func WithGzip(opts ...GzipOption) CompressionOption { return sdk.WithGzip(opts...) }

// WithGzipLevel sets the gzip level (compress/gzip constants).
func WithGzipLevel(level int) GzipOption { return sdk.WithGzipLevel(level) }

// WithServerPriority picks the first offered compressor the client accepts
// (the default).
func WithServerPriority() CompressionOption { return sdk.WithServerPriority() }

// WithClientPriority picks the first encoding in the client's
// Accept-Encoding list that has an offered compressor.
func WithClientPriority() CompressionOption { return sdk.WithClientPriority() }

// WithForced always uses the first offered compressor, regardless of
// Accept-Encoding.
func WithForced() CompressionOption { return sdk.WithForced() }
