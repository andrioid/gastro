package gastro

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testCardProps struct{ Title string }

type testRouter struct{ prefix string }

func testCard() Component[testCardProps] {
	return NewComponent(func(router any, req *http.Request, p testCardProps) (template.HTML, error) {
		rt := router.(*testRouter)
		suffix := ""
		if req != nil {
			suffix = " " + req.URL.Path
		}
		return template.HTML(rt.prefix + p.Title + suffix), nil
	})
}

func TestRenderComponent_PassesTypedProps(t *testing.T) {
	html, err := RenderComponent(&testRouter{prefix: "card:"}, testCard(), testCardProps{Title: "hi"})
	if err != nil {
		t.Fatalf("RenderComponent: %v", err)
	}
	if html != "card:hi" {
		t.Errorf("html = %q, want %q", html, "card:hi")
	}
}

func TestComponentWith_BindsRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/board", nil)
	base := testCard()
	html, err := RenderComponent(&testRouter{}, base.With(req), testCardProps{Title: "x"})
	if err != nil {
		t.Fatalf("RenderComponent: %v", err)
	}
	if html != "x /board" {
		t.Errorf("bound html = %q, want %q", html, "x /board")
	}
	// With returns a copy; the original stays unbound.
	html, err = RenderComponent(&testRouter{}, base, testCardProps{Title: "x"})
	if err != nil {
		t.Fatalf("RenderComponent: %v", err)
	}
	if html != "x" {
		t.Errorf("unbound html = %q, want %q", html, "x")
	}
}

func TestRenderComponent_ZeroComponentErrors(t *testing.T) {
	var zero Component[testCardProps]
	_, err := RenderComponent(&testRouter{}, zero, testCardProps{})
	if err == nil || !strings.Contains(err.Error(), "zero Component") {
		t.Fatalf("expected zero Component error, got %v", err)
	}
}
