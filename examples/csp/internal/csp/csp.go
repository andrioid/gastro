// Package csp implements Content-Security-Policy nonce middleware
// for inline scripts.
//
// The Content-Security-Policy header tells the browser which scripts
// are allowed to execute. The strict-with-nonce flavour lets you keep
// inline <script> tags AS LONG AS they carry a nonce attribute whose
// value matches the one declared in the CSP header for this request.
// Nonces must be fresh per request and unguessable to an attacker.
//
// This package exists to demonstrate the third axis of the
// WithRequestFuncs contract: **helper-to-middleware coordination**.
// The middleware generates the nonce AND writes it into the response
// header; the template helper reads the same nonce out of the request
// context for the inline <script nonce="..."> attribute. The two
// sides must agree — if they don't, the browser refuses to run the
// script. The example proves that gastro's per-request FuncMap path
// gets this right.
package csp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// nonceLen is the byte length of the generated nonces. 18 bytes
// base64-encoded is 24 chars — short enough to keep CSP headers
// reasonable but more than enough entropy to defeat brute force.
const nonceLen = 18

type ctxKey struct{}

// NonceFromCtx returns the per-request CSP nonce attached by Middleware,
// or an empty string when none is attached. The empty-string return
// keeps the WithRequestFuncs probe (which passes an empty context)
// safe: the binder's helper still works, it just returns an empty
// nonce.
func NonceFromCtx(ctx context.Context) string {
	if s, ok := ctx.Value(ctxKey{}).(string); ok {
		return s
	}
	return ""
}

// Middleware mints a fresh nonce for every request, writes the CSP
// header advertising it, and attaches the same nonce to the request
// context for downstream template helpers to read.
//
// Header shape: a strict-but-pragmatic policy with 'self' for scripts
// (so /static/*.js still loads) plus 'nonce-XXXX' for inline tags. A
// real-world policy would harden this further; the recipe is the
// MECHANISM, not the exact policy.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := mustMintNonce()
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'nonce-"+nonce+"'; "+
				"object-src 'none'; "+
				"base-uri 'self'",
		)
		ctx := context.WithValue(r.Context(), ctxKey{}, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestFuncs is the WithRequestFuncs binder: pulls the nonce out of
// the context and returns a one-helper FuncMap. Sized for the
// architectural punchline — adopters write THREE lines of glue:
//
//	gastro.WithRequestFuncs(csp.RequestFuncs)
//
// and templates use {{ cspNonce }} on inline <script> tags.
func RequestFuncs(r *http.Request) map[string]any {
	nonce := NonceFromCtx(r.Context())
	return map[string]any{
		"cspNonce": func() string { return nonce },
	}
}

func mustMintNonce() string {
	buf := make([]byte, nonceLen)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure means we cannot honour CSP guarantees;
		// keep failing loud rather than silently regressing security.
		panic("csp: crypto/rand.Read: " + err.Error())
	}
	return base64.RawStdEncoding.EncodeToString(buf)
}
