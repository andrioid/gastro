package codegen_test

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

// flagged runs ReferencesPerRequestScope on a parsed expression and returns
// whether it was flagged plus the offending detail.
func flagged(t *testing.T, exprSrc string) (bool, string) {
	t.Helper()
	expr, err := parser.ParseExpr(exprSrc)
	if err != nil {
		t.Fatalf("parsing %q: %v", exprSrc, err)
	}
	hit, ref := codegen.ReferencesPerRequestScope(expr)
	if !hit {
		return false, ""
	}
	return true, ref.Detail
}

// flaggedBlock parses a function body wrapped in `package p; func fixture()
// { ... }` and runs the check on the file. Used for tests that need
// statement-level context (closures, range loops, etc.).
func flaggedBlock(t *testing.T, body string) (bool, string) {
	t.Helper()
	src := "package p\nfunc fixture() {\n" + body + "\n}"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parsing block: %v\nsrc:\n%s", err, src)
	}
	hit, ref := codegen.ReferencesPerRequestScope(file)
	if !hit {
		return false, ""
	}
	return true, ref.Detail
}

func TestRefs_BareR_Errors(t *testing.T) {
	hit, detail := flagged(t, "r.URL.Path")
	if !hit {
		t.Fatalf("expected r.URL.Path to be flagged")
	}
	if detail != "r.URL" {
		t.Errorf("detail = %q, want %q", detail, "r.URL")
	}
}

func TestRefs_BareW_Errors(t *testing.T) {
	hit, detail := flagged(t, "w.Header()")
	if !hit {
		t.Fatalf("expected w.Header() to be flagged")
	}
	if detail != "w.Header" {
		t.Errorf("detail = %q, want %q", detail, "w.Header")
	}
}

func TestRefs_GastroProps_Errors(t *testing.T) {
	hit, detail := flagged(t, "gastro.Props().Name")
	if !hit {
		t.Fatalf("expected gastro.Props() to be flagged")
	}
	if detail != "gastro.Props()" {
		t.Errorf("detail = %q, want %q", detail, "gastro.Props()")
	}
}

func TestRefs_GastroContext_Errors(t *testing.T) {
	hit, detail := flagged(t, "gastro.Context()")
	if !hit {
		t.Fatalf("expected gastro.Context() to be flagged")
	}
	if detail != "gastro.Context()" {
		t.Errorf("detail = %q, want %q", detail, "gastro.Context()")
	}
}

func TestRefs_GastroFromGeneric_Errors(t *testing.T) {
	// gastro.From[*sql.DB](r.Context()) parses as an IndexExpr-wrapped
	// CallExpr. Either the outer gastro.From check or the inner r.Context
	// reference is acceptable as the trigger.
	hit, detail := flagged(t, "gastro.From[*sql.DB](r.Context())")
	if !hit {
		t.Fatalf("expected gastro.From[*sql.DB](...) to be flagged")
	}
	if detail != "gastro.From()" && detail != "r.Context" {
		t.Errorf("detail = %q, want %q or %q", detail, "gastro.From()", "r.Context")
	}
}

func TestRefs_GastroChildren_Errors(t *testing.T) {
	hit, detail := flagged(t, "gastro.Children()")
	if !hit {
		t.Fatalf("expected gastro.Children() to be flagged")
	}
	if detail != "gastro.Children()" {
		t.Errorf("detail = %q, want %q", detail, "gastro.Children()")
	}
}

func TestRefs_PureExpr_OK(t *testing.T) {
	hit, detail := flagged(t, `regexp.MustCompile("^[a-z]+$")`)
	if hit {
		t.Errorf("expected regexp.MustCompile to be safe, but flagged: %q", detail)
	}
}

func TestRefs_StdlibCall_OK(t *testing.T) {
	hit, detail := flagged(t, `os.Getenv("DATABASE_URL")`)
	if hit {
		t.Errorf("expected os.Getenv to be safe, but flagged: %q", detail)
	}
}

func TestRefs_ClosureCapturingR_Errors(t *testing.T) {
	hit, detail := flagged(t, `func() string { return r.URL.Path }`)
	if !hit {
		t.Fatalf("expected closure capturing r to be flagged")
	}
	if detail != "r.URL" {
		t.Errorf("detail = %q, want %q", detail, "r.URL")
	}
}

func TestRefs_ClosureNotCapturing_OK(t *testing.T) {
	hit, detail := flagged(t, `func(s string) string { return s }`)
	if hit {
		t.Errorf("expected pure closure to be safe, but flagged: %q", detail)
	}
}

func TestRefs_ParamNamedR_NotFlagged(t *testing.T) {
	// Closure with its own `r` parameter — the inner `r` is bound by the
	// param list, not the page handler's request. Free-variable analysis
	// must walk scopes correctly.
	hit, detail := flagged(t, `func(r io.Reader) error { return r.Close() }`)
	if hit {
		t.Errorf("expected closure with bound r to be safe, but flagged: %q", detail)
	}
}

func TestRefs_HandlerFuncWrap_OK(t *testing.T) {
	// Common stdlib pattern: hoisting a hand-rolled http.HandlerFunc.
	// The inner w/r are bound by the closure's parameter list.
	src := `http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_ = r.URL
	})`
	hit, detail := flagged(t, src)
	if hit {
		t.Errorf("expected http.HandlerFunc wrap to be safe, but flagged: %q", detail)
	}
}

func TestRefs_NestedClosure_PropagatesUpward(t *testing.T) {
	// Closure that takes `r` as a param, but its body captures the outer
	// `w` (which IS the page handler's writer, not a param). Must be
	// flagged.
	hit, _ := flaggedBlock(t, `_ = func(r io.Reader) {
		w.Header().Set("X", "y")
		_ = r
	}`)
	if !hit {
		t.Errorf("expected outer-w capture inside closure to be flagged")
	}
}

func TestRefs_ResultNameNamedR_NotFlagged(t *testing.T) {
	// Named return: the result `r` is bound. Avoids false positives on
	// patterns like `func newReader() (r io.Reader, err error) { ... }`.
	hit, detail := flagged(t, `func() (r io.Reader, err error) { return nil, nil }`)
	if hit {
		t.Errorf("expected named-result r to be safe, but flagged: %q", detail)
	}
}

func TestRefs_AssignStmtBindsLHS(t *testing.T) {
	// Inside a closure body, a := assignment should bind its LHS so
	// subsequent reads aren't false-positives.
	hit, _ := flaggedBlock(t, `_ = func() {
		r := newReader()
		_ = r
	}`)
	if hit {
		t.Errorf("expected := r local to be safe, but flagged")
	}
}

func TestRefs_RangeBindsKeyValue(t *testing.T) {
	hit, _ := flaggedBlock(t, `_ = func() {
		for r, w := range pairs {
			_, _ = r, w
		}
	}`)
	if hit {
		t.Errorf("expected range-bound r/w to be safe, but flagged")
	}
}
