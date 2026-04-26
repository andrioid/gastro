# Response Compression

Go's `net/http` server does not compress responses by default. Gastro follows
the same philosophy as the standard library: it stays out of infrastructure
concerns and lets you choose the approach that fits your deployment.

## Option A: Reverse Proxy (recommended for production)

If you deploy behind nginx, Caddy, or a CDN like Cloudflare, compression is
handled for you with zero Go code. This is the most common production setup.

**Caddy** compresses by default -- no configuration needed.

**nginx** needs one directive:

```nginx
gzip on;
gzip_types text/html text/css application/javascript application/json image/svg+xml;
```

## Option B: Stdlib Gzip Middleware

If you serve directly without a reverse proxy, you can wrap the gastro
router's handler with a middleware that compresses responses using
`compress/gzip` from the standard library.

```go
package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"your/app/gastro"
)

func main() {
	router := gastro.New()
	http.ListenAndServe(":8080", GzipMiddleware(router.Handler()))
}

// GzipMiddleware compresses responses for clients that accept gzip.
// SSE streams and already-compressed formats are passed through unchanged.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz, _ := gzip.NewWriterLevel(w, gzip.DefaultCompression)
		defer gz.Close()

		gw := &gzipWriter{
			ResponseWriter: w,
			gz:             gz,
		}
		defer gw.finish()

		next.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	gz       *gzip.Writer
	sniffed  bool
	compress bool
}

// Unwrap lets http.NewResponseController reach the underlying writer.
// Required for SSE flushing to work through this middleware.
func (g *gzipWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.sniffed {
		g.decide()
	}
	if g.compress {
		g.ResponseWriter.Header().Del("Content-Length")
		g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
		g.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.sniffed {
		g.decide()
		if g.compress {
			g.ResponseWriter.Header().Del("Content-Length")
			g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
			g.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		}
	}
	if g.compress {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) decide() {
	g.sniffed = true
	ct := g.ResponseWriter.Header().Get("Content-Type")
	g.compress = shouldCompress(ct)
}

func (g *gzipWriter) finish() {
	if g.compress {
		g.gz.Close()
	}
}

// shouldCompress returns true for text-based content types that benefit from
// compression. Already-compressed formats (images, fonts, video) and SSE
// streams are skipped.
func shouldCompress(contentType string) bool {
	ct := strings.ToLower(contentType)
	for _, prefix := range []string{
		"text/",
		"application/json",
		"application/javascript",
		"application/xml",
		"application/xhtml",
		"image/svg+xml",
	} {
		if strings.HasPrefix(ct, prefix) {
			// SSE must not be compressed -- it needs per-event flushing.
			if strings.HasPrefix(ct, "text/event-stream") {
				return false
			}
			return true
		}
	}
	return false
}
```

## Option C: Third-Party Libraries

For better performance, brotli support, or fewer lines of code, consider:

- [`klauspost/compress/gzhttp`](https://github.com/klauspost/compress) --
  drop-in handler wrapper with automatic content-type detection:

  ```go
  router := gastro.New()
  http.ListenAndServe(":8080", gzhttp.GzipHandler(router.Handler()))
  ```

## Caveats

**SSE streams must not be compressed.** Compression buffers data until the
compressor decides to flush, which delays events and breaks real-time behavior.
The middleware example above skips `text/event-stream` responses for this
reason.

**Already-compressed formats waste CPU.** JPEG, PNG, WebP, WOFF2, MP4, and
similar formats are already compressed. Running gzip over them produces output
that is the same size or larger.

**Set `Vary: Accept-Encoding`** so that caches (CDN, browser) store separate
versions for compressed and uncompressed responses.

**Composition with dev-mode middleware.** The compression middleware wraps
`router.Handler()` from the outside, which means it composes correctly with
the dev-mode `DevReloader.Middleware` that the router applies internally.
No special handling is needed.
