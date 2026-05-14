package i18n_test

import (
	"context"
	"embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gastro-i18n-example/internal/i18n"
)

//go:embed testdata/i18n/*.po
var testPO embed.FS

const testDir = "testdata/i18n"

// TestLocalizer_T_KnownAndFallback: T returns translations for known
// msgids and falls back to the msgid itself for missing entries.
func TestLocalizer_T_KnownAndFallback(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "de")
	rr := httptest.NewRecorder()
	cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.FromCtx(r.Context())
		if got := l.T("Welcome"); got != "Willkommen" {
			t.Errorf("T(Welcome): got %q, want Willkommen", got)
		}
		// Unknown msgid → returns the msgid itself.
		if got := l.T("Unknown phrase"); got != "Unknown phrase" {
			t.Errorf("T(unknown): got %q, want fallback", got)
		}
	})).ServeHTTP(rr, req)
}

// TestLocalizer_TN_PluralSelection: TN picks the right plural form for
// n==1 vs everything else, and falls back to English when no entry.
func TestLocalizer_TN_PluralSelection(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "de")
	rr := httptest.NewRecorder()
	cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.FromCtx(r.Context())
		if got := l.TN("%d item", "%d items", 1); got != "%d Element" {
			t.Errorf("TN(de, 1): got %q, want %%d Element", got)
		}
		if got := l.TN("%d item", "%d items", 5); got != "%d Elemente" {
			t.Errorf("TN(de, 5): got %q, want %%d Elemente", got)
		}
		// Fallback: unknown plural pair returns the English form.
		if got := l.TN("dog", "dogs", 2); got != "dogs" {
			t.Errorf("TN(unknown): got %q, want fallback", got)
		}
	})).ServeHTTP(rr, req)
}

// TestLocalizer_TC_ContextDistinguishes: same msgid maps to different
// translations depending on msgctxt.
func TestLocalizer_TC_ContextDistinguishes(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "de")
	rr := httptest.NewRecorder()
	cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.FromCtx(r.Context())
		if got := l.TC("button", "Open"); got != "Öffnen" {
			t.Errorf("TC(button, Open): got %q, want Öffnen", got)
		}
		if got := l.TC("adjective", "Open"); got != "Geöffnet" {
			t.Errorf("TC(adjective, Open): got %q, want Geöffnet", got)
		}
	})).ServeHTTP(rr, req)
}

// TestFromCtx_EmptyContextIsProbeSafe: per the WithRequestFuncs binder
// contract (docs/helpers.md §"The binder contract"), FromCtx must
// tolerate a context with no installed Localizer (the gastro.New()
// probe path). It returns a null localizer that echoes msgids back.
func TestFromCtx_EmptyContextIsProbeSafe(t *testing.T) {
	l := i18n.FromCtx(context.Background())
	if got := l.T("Welcome"); got != "Welcome" {
		t.Errorf("FromCtx on empty ctx: T should echo msgid; got %q", got)
	}
	if got := l.TN("apple", "apples", 3); got != "apples" {
		t.Errorf("FromCtx on empty ctx: TN should fall back; got %q", got)
	}
	if got := l.TC("button", "Open"); got != "Open" {
		t.Errorf("FromCtx on empty ctx: TC should echo msgid; got %q", got)
	}
}

// TestMiddleware_AcceptLanguageSelection: locale chosen from
// Accept-Language when no path-prefix or cookie is present.
func TestMiddleware_AcceptLanguageSelection(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		accept string
		want   string
	}{
		{"de", "de"},
		{"en", "en"},
		{"fr,de;q=0.9", "de"}, // first matching tag wins (fr unknown, de hit)
		{"", "en"},            // fallback
		{"zh", "en"},          // unknown → fallback
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		if tc.accept != "" {
			req.Header.Set("Accept-Language", tc.accept)
		}
		rr := httptest.NewRecorder()
		cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := i18n.FromCtx(r.Context())
			if l.Locale != tc.want {
				t.Errorf("accept=%q: locale=%q, want %q", tc.accept, l.Locale, tc.want)
			}
		})).ServeHTTP(rr, req)
	}
}

// TestMiddleware_PathPrefixWins: a path-prefix locale beats Accept-Language.
func TestMiddleware_PathPrefixWins(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/de/about", nil)
	req.Header.Set("Accept-Language", "en")
	rr := httptest.NewRecorder()
	cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.FromCtx(r.Context())
		if l.Locale != "de" {
			t.Errorf("path-prefix should beat Accept-Language; got %q", l.Locale)
		}
	})).ServeHTTP(rr, req)
}

// TestMiddleware_CookieBeatsAcceptLanguage: a gastro_locale cookie
// overrides the Accept-Language header when no path-prefix is present.
func TestMiddleware_CookieBeatsAcceptLanguage(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "de")
	req.AddCookie(&http.Cookie{Name: "gastro_locale", Value: "en"})
	rr := httptest.NewRecorder()
	cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.FromCtx(r.Context())
		if l.Locale != "en" {
			t.Errorf("cookie should beat Accept-Language; got %q", l.Locale)
		}
	})).ServeHTTP(rr, req)
}

// TestParsePO_HandlesEscapesAndContinuations: the PO parser handles
// common cases: backslash escapes, adjacent-string continuation lines,
// blank-line record separation.
func TestParsePO_HandlesEscapesAndContinuations(t *testing.T) {
	cat, err := i18n.Load(testPO, testDir, []string{"en", "de"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "en")
	rr := httptest.NewRecorder()
	cat.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.FromCtx(r.Context())
		// "Welcome" is in en.po as an identity entry.
		if got := l.T("Welcome"); got != "Welcome" {
			t.Errorf("en T(Welcome): got %q", got)
		}
	})).ServeHTTP(rr, req)

	// And a sanity check that locales() reports registration order.
	got := cat.Locales()
	if len(got) != 2 || got[0] != "en" || got[1] != "de" {
		t.Errorf("Locales() returned %v, want [en de]", got)
	}
}

// TestLoad_MissingLocaleErrors: an unknown locale in the requested list
// errors out rather than silently dropping the locale.
func TestLoad_MissingLocaleErrors(t *testing.T) {
	_, err := i18n.Load(testPO, testDir, []string{"en", "ja"}, "en")
	if err == nil {
		t.Fatal("expected error for missing locale ja.po")
	}
	if !strings.Contains(err.Error(), "ja.po") {
		t.Errorf("error should name the missing locale file; got %v", err)
	}
}
