package router_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/router"
)

func TestBuildRoutes_IndexFile(t *testing.T) {
	files := []string{"pages/index.gastro"}

	routes := router.BuildRoutes(files)

	assertRoute(t, routes, "/{$}", "pages/index.gastro")
}

func TestBuildRoutes_NestedIndex(t *testing.T) {
	files := []string{
		"pages/index.gastro",
		"pages/about/index.gastro",
	}

	routes := router.BuildRoutes(files)

	assertRoute(t, routes, "/{$}", "pages/index.gastro")
	assertRoute(t, routes, "/about", "pages/about/index.gastro")
}

func TestBuildRoutes_DynamicParam(t *testing.T) {
	files := []string{"pages/blog/[slug].gastro"}

	routes := router.BuildRoutes(files)

	assertRoute(t, routes, "/blog/{slug}", "pages/blog/[slug].gastro")
}

func TestBuildRoutes_DeepNesting(t *testing.T) {
	files := []string{
		"pages/blog/index.gastro",
		"pages/blog/[slug].gastro",
		"pages/blog/[slug]/comments.gastro",
	}

	routes := router.BuildRoutes(files)

	assertRoute(t, routes, "/blog", "pages/blog/index.gastro")
	assertRoute(t, routes, "/blog/{slug}", "pages/blog/[slug].gastro")
	assertRoute(t, routes, "/blog/{slug}/comments", "pages/blog/[slug]/comments.gastro")
}

func TestBuildRoutes_MultipleParams(t *testing.T) {
	files := []string{"pages/[category]/[id].gastro"}

	routes := router.BuildRoutes(files)

	assertRoute(t, routes, "/{category}/{id}", "pages/[category]/[id].gastro")
}

func TestBuildRoutes_EmptyReturnsNoRoutes(t *testing.T) {
	routes := router.BuildRoutes(nil)

	if len(routes) != 0 {
		t.Errorf("expected 0 routes for nil input, got %d", len(routes))
	}
}

func TestRouteToFuncName(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"pages/index.gastro", "pageIndex"},
		{"pages/about/index.gastro", "pageAboutIndex"},
		{"pages/blog/[slug].gastro", "pageBlogSlug"},
		{"pages/blog/[slug]/comments.gastro", "pageBlogSlugComments"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := router.RouteToFuncName(tt.file)
			if got != tt.want {
				t.Errorf("RouteToFuncName(%q): got %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

func assertRoute(t *testing.T, routes []router.Route, pattern, file string) {
	t.Helper()
	for _, r := range routes {
		if r.Pattern == pattern && r.File == file {
			return
		}
	}
	t.Errorf("expected route {pattern: %q, file: %q}, not found in %v", pattern, file, routes)
}
