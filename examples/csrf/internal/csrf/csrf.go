// Package csrf is the example's hand-rolled CSRF protection. It
// implements the double-submit cookie pattern: each session gets a
// random token via a cookie, and every state-changing request must
// echo that token back in either the `_csrf` form field or the
// `X-CSRF-Token` header. The server verifies the two values match
// before passing the request through.
//
// This package exists to demonstrate the WithRequestFuncs contract in
// a domain other than i18n. Real apps should use gorilla/csrf or a
// similar battle-tested library — Gastro doesn't care which.
//
// Mixed-return-type binder: the WithRequestFuncs binder in main.go
// returns two helpers from this package — csrfToken (string) and
// csrfField (template.HTML). Both are scoped to the same request via
// closure capture. Demonstrates that one binder can serve helpers
// with different return types.
package csrf

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
)

// CookieName is the cookie that carries the user's CSRF token between
// requests. Documented as a constant so adopters can match it from
// other code (e.g. JavaScript that reads the cookie for AJAX).
const CookieName = "gastro_csrf"

// FormField is the form input name expected by Verify. AJAX clients
// can send the token via the HeaderName header instead.
const FormField = "_csrf"

// HeaderName is the HTTP header AJAX clients use to send the CSRF
// token. Common convention.
const HeaderName = "X-CSRF-Token"

// tokenLen is the byte length of the generated tokens. 32 bytes
// base64-encoded is 44 chars — small enough for a header, large
// enough that brute-forcing is infeasible.
const tokenLen = 32

// ctxKey is the unexported context key for the per-request token.
type ctxKey struct{}

// TokenFromCtx returns the CSRF token attached to ctx by Middleware,
// or an empty string when none is attached. The empty-string return
// makes the WithRequestFuncs probe (which passes an empty context) a
// no-op — the binder closures over TokenFromCtx don't panic; they
// just produce empty placeholder helpers.
func TokenFromCtx(ctx context.Context) string {
	if s, ok := ctx.Value(ctxKey{}).(string); ok {
		return s
	}
	return ""
}

// Middleware installs the double-submit cookie + verifier. Behaviour:
//
//   - GET / HEAD / OPTIONS: pass through. Reuse the existing cookie
//     if present; otherwise mint a fresh token and write it to a
//     SameSite=Lax, HttpOnly=false cookie. (HttpOnly is false so the
//     value is readable by JavaScript that wants to send it via the
//     X-CSRF-Token header; the double-submit pattern relies on the
//     attacker not being able to make the browser send arbitrary
//     headers cross-origin, which is a separate axis from cookie
//     scriptability.)
//
//   - POST / PUT / PATCH / DELETE: extract the cookie token and the
//     supplied token (header first, then form). Reject (403) if either
//     is missing or the two don't match.
//
// On success the request flows through with the token attached to
// the context so templates and handlers can access it via
// TokenFromCtx.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieToken, _ := readCookie(r)
		if cookieToken == "" {
			cookieToken = mustMintToken()
			http.SetCookie(w, &http.Cookie{
				Name:     CookieName,
				Value:    cookieToken,
				Path:     "/",
				SameSite: http.SameSiteLaxMode,
			})
		}

		if isSafeMethod(r.Method) {
			ctx := context.WithValue(r.Context(), ctxKey{}, cookieToken)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// State-changing request: verify the submitted token matches.
		submitted := r.Header.Get(HeaderName)
		if submitted == "" {
			// Parse the form lazily — this consumes r.Body, so
			// downstream handlers that read it again will see EOF.
			// Acceptable for this example; production middleware
			// might buffer-and-replay or require the AJAX path.
			_ = r.ParseForm()
			submitted = r.FormValue(FormField)
		}

		if submitted == "" || submitted != cookieToken {
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKey{}, cookieToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestFuncs is the WithRequestFuncs binder for adopters that want
// to wire csrf directly. It's intentionally exported so main.go can
// pass it without re-implementing the closure shape:
//
//	gastro.WithRequestFuncs(csrf.RequestFuncs)
//
// Returns:
//   - csrfToken (string) — the raw token for AJAX use cases.
//   - csrfField (template.HTML) — a ready-made <input type="hidden">.
func RequestFuncs(r *http.Request) template.FuncMap {
	token := TokenFromCtx(r.Context())
	return template.FuncMap{
		"csrfToken": func() string { return token },
		"csrfField": func() template.HTML {
			return template.HTML(
				`<input type="hidden" name="` + FormField + `" value="` + token + `">`,
			)
		},
	}
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func readCookie(r *http.Request) (string, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", err
	}
	if c.Value == "" {
		return "", errors.New("empty csrf cookie")
	}
	return c.Value, nil
}

func mustMintToken() string {
	buf := make([]byte, tokenLen)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is catastrophic; the alternative is
		// reusing predictable tokens, which defeats CSRF protection
		// entirely.
		panic("csrf: crypto/rand.Read: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
