package csp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gastro-csp-example/internal/csp"
)

// TestMiddleware_NoncePerRequest: two requests through the middleware
// see different nonces. Defends the freshness contract — predictable
// nonces are useless for CSP.
func TestMiddleware_NoncePerRequest(t *testing.T) {
	var nonces []string
	h := csp.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonces = append(nonces, csp.NonceFromCtx(r.Context()))
	}))
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(rr, req)
	}
	seen := map[string]bool{}
	for _, n := range nonces {
		if n == "" {
			t.Fatal("nonce should not be empty")
		}
		if seen[n] {
			t.Errorf("nonce repeated: %q (saw %v)", n, nonces)
		}
		seen[n] = true
	}
}

// TestMiddleware_HeaderAdvertisesNonce: the CSP response header
// includes the same nonce the context carries. This is the cross-
// surface coordination requirement — if these two disagree, the
// browser refuses to run inline scripts.
func TestMiddleware_HeaderAdvertisesNonce(t *testing.T) {
	var ctxNonce string
	h := csp.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxNonce = csp.NonceFromCtx(r.Context())
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	hdr := rr.Header().Get("Content-Security-Policy")
	if hdr == "" {
		t.Fatal("Content-Security-Policy header was not set")
	}
	want := "nonce-" + ctxNonce
	if !strings.Contains(hdr, want) {
		t.Errorf("header should contain %q; got %q", want, hdr)
	}
}

// TestNonceFromCtx_EmptyContextIsProbeSafe: NonceFromCtx on a
// context without a nonce returns the empty string instead of
// panicking. Required for the WithRequestFuncs probe path (gastro
// calls binders at New() with a synthetic empty context).
func TestNonceFromCtx_EmptyContextIsProbeSafe(t *testing.T) {
	if got := csp.NonceFromCtx(context.Background()); got != "" {
		t.Errorf("expected empty nonce on empty ctx; got %q", got)
	}
}

// TestRequestFuncs_HelperReturnsNonce: the helper returned by
// RequestFuncs resolves to the nonce attached to the supplied
// request's context.
func TestRequestFuncs_HelperReturnsNonce(t *testing.T) {
	var captured *http.Request
	csp.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	fm := csp.RequestFuncs(captured)
	got := fm["cspNonce"].(func() string)()
	if got == "" {
		t.Error("cspNonce helper returned empty")
	}
	if got != csp.NonceFromCtx(captured.Context()) {
		t.Errorf("helper nonce %q != context nonce %q", got, csp.NonceFromCtx(captured.Context()))
	}
}
