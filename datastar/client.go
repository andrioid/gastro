package datastar

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"html/template"
	"net/http"
	"time"
)

// ClientVersion is the version of the embedded Datastar browser client. It
// speaks the same (v1) protocol as the SDK this package wraps.
const ClientVersion = "1.0.4"

// clientJS is the official Datastar bundle, byte-for-byte from
// https://github.com/starfederation/datastar/blob/v1.0.4/bundles/datastar.js
// (MIT, see client/LICENSE.md).
//
//go:embed client/datastar.js
var clientJS []byte

var clientHash = sha256.Sum256(clientJS)

// ClientPath is the URL path of the embedded client. It embeds a prefix of
// the client's SHA-256, so it changes whenever the client does and can be
// cached forever.
var ClientPath = clientPath(clientJS)

// ClientPattern is the http.ServeMux pattern for [ClientHandler]:
//
//	mux.Handle(datastar.ClientPattern, datastar.ClientHandler())
var ClientPattern = http.MethodGet + " " + ClientPath

func clientPath(js []byte) string {
	sum := sha256.Sum256(js)
	return "/_gastro/datastar/" + hex.EncodeToString(sum[:8]) + ".js"
}

// ClientHandler serves the embedded Datastar client with an immutable,
// year-long Cache-Control. Mount it at [ClientPattern].
func ClientHandler() http.Handler {
	etag := `"` + hex.EncodeToString(clientHash[:]) + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		h.Set("ETag", etag)
		http.ServeContent(w, r, "datastar.js", time.Time{}, bytes.NewReader(clientJS))
	})
}

// scriptTag is computed once; ClientPath contains only hex and fixed text.
var scriptTag = template.HTML(`<script type="module" src="` + ClientPath + `"></script>`)

// Funcs returns template helpers for pages using Datastar:
//
//   - datastarScript renders the <script type="module"> tag loading the
//     embedded client from [ClientPath].
//
// Register them with gastro.WithFuncs(datastar.Funcs()).
func Funcs() template.FuncMap {
	return template.FuncMap{
		"datastarScript": func() template.HTML { return scriptTag },
	}
}
