package csrf_test

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gastro-csrf-example/internal/csrf"
)

// passthrough is the handler the middleware wraps in every test. It
// echoes the token from the request context so tests can assert it
// flowed through.
var passthrough = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("token=" + csrf.TokenFromCtx(r.Context())))
})

// TestSafeMethodIssuesCookieAndPasses: GET mints a token if none is
// present, writes the cookie, and passes the request through.
func TestSafeMethodIssuesCookieAndPasses(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	csrf.Middleware(passthrough).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "token=") || len(body) <= len("token=") {
		t.Errorf("expected non-empty token in body; got %q", body)
	}

	cookies := rr.Result().Cookies()
	var ck *http.Cookie
	for _, c := range cookies {
		if c.Name == csrf.CookieName {
			ck = c
		}
	}
	if ck == nil {
		t.Fatalf("expected %s cookie, got cookies=%v", csrf.CookieName, cookies)
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", ck.SameSite)
	}
	if ck.Value != strings.TrimPrefix(body, "token=") {
		t.Errorf("cookie value does not match context token; cookie=%q body=%q", ck.Value, body)
	}
}

// TestSafeMethodReusesExistingCookie: when the request already carries
// a CSRF cookie, the middleware leaves it alone and propagates that
// token (not a freshly minted one).
func TestSafeMethodReusesExistingCookie(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "preset-token"})
	csrf.Middleware(passthrough).ServeHTTP(rr, req)

	if got := rr.Body.String(); got != "token=preset-token" {
		t.Errorf("expected preset token to flow through; got %q", got)
	}
	// No new cookie should be issued.
	for _, c := range rr.Result().Cookies() {
		if c.Name == csrf.CookieName {
			t.Errorf("middleware re-issued cookie when one was present: %v", c)
		}
	}
}

// TestUnsafeMethodWithoutTokenRejected: POST with no matching token
// fails with 403.
func TestUnsafeMethodWithoutTokenRejected(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "abc"})
	csrf.Middleware(passthrough).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

// TestUnsafeMethodWithFormTokenAccepted: POST with matching form
// token is accepted.
func TestUnsafeMethodWithFormTokenAccepted(t *testing.T) {
	body := url.Values{csrf.FormField: {"abc"}}.Encode()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "abc"})
	csrf.Middleware(passthrough).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "token=abc" {
		t.Errorf("token did not propagate via context; got %q", rr.Body.String())
	}
}

// TestUnsafeMethodWithHeaderTokenAccepted: AJAX clients use the
// X-CSRF-Token header instead of the form field. The header wins
// when both are present (matches gorilla/csrf).
func TestUnsafeMethodWithHeaderTokenAccepted(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(csrf.HeaderName, "abc")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "abc"})
	csrf.Middleware(passthrough).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// TestUnsafeMethodMismatchRejected: cookie and submitted token differ
// → 403. Demonstrates that the double-submit pattern catches the
// canonical CSRF attack (attacker controls neither side).
func TestUnsafeMethodMismatchRejected(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(csrf.HeaderName, "abc")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "different"})
	csrf.Middleware(passthrough).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

// TestRequestFuncs_ProbeSafe: invoking the RequestFuncs binder with a
// fresh probe-style request (no installed token) must not panic, and
// the returned helpers must produce empty / blank output. This pins
// the WithRequestFuncs probe contract from main.go's perspective.
func TestRequestFuncs_ProbeSafe(t *testing.T) {
	probe := httptest.NewRequest(http.MethodGet, "/__gastro_probe", nil)
	fm := csrf.RequestFuncs(probe)
	if fm["csrfToken"] == nil || fm["csrfField"] == nil {
		t.Fatal("RequestFuncs must return csrfToken and csrfField")
	}
	if got := fm["csrfToken"].(func() string)(); got != "" {
		t.Errorf("probe csrfToken: got %q, want empty", got)
	}
	if got := fm["csrfField"].(func() template.HTML)(); got != template.HTML(`<input type="hidden" name="`+csrf.FormField+`" value="">`) {
		t.Errorf("probe csrfField: got %q", got)
	}
}

// TestRequestFuncs_CarriesTokenFromContext: when invoked with a
// request carrying the token in its context, the helpers return the
// real value.
func TestRequestFuncs_CarriesTokenFromContext(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "tkn-123"})
	var captured *http.Request
	csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
	})).ServeHTTP(rr, req)

	fm := csrf.RequestFuncs(captured)
	got := fm["csrfToken"].(func() string)()
	if got != "tkn-123" {
		t.Errorf("csrfToken: got %q, want tkn-123", got)
	}
}
