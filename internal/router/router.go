package router

import (
	"strings"
)

// Route maps an HTTP pattern to a .gastro page file.
//
// Track B (docs/history/frictions-plan.md §4.2): patterns no longer carry an
// HTTP method prefix. Each .gastro file under pages/ is registered for
// every method; the page's frontmatter branches on r.Method. This is
// the headline mechanic that lets one .gastro file handle both GET
// (template render) and POST (e.g. an SSE patch) without inventing new
// authoring concepts.
type Route struct {
	Pattern  string // e.g. "/blog/{slug}"
	File     string // e.g. "pages/blog/[slug].gastro"
	FuncName string // e.g. "pageBlogSlug"
}

// BuildRoutes converts a list of .gastro page file paths into HTTP routes.
// File paths are relative (e.g. "pages/index.gastro").
func BuildRoutes(files []string) []Route {
	routes := make([]Route, 0, len(files))

	for _, file := range files {
		pattern := fileToPattern(file)
		funcName := RouteToFuncName(file)
		routes = append(routes, Route{
			Pattern:  pattern,
			File:     file,
			FuncName: funcName,
		})
	}

	return routes
}

// fileToPattern converts a page file path to an HTTP route pattern.
// e.g. "pages/blog/[slug].gastro" -> "/blog/{slug}"
//
// Patterns are method-less per Track B; the page's frontmatter is
// expected to branch on r.Method. Pre-Track-B patterns were
// "GET /<route>" — callers still using WithOverride must drop the
// method prefix from their pattern strings.
func fileToPattern(file string) string {
	// Strip "pages/" prefix and ".gastro" suffix
	route := strings.TrimPrefix(file, "pages/")
	route = strings.TrimSuffix(route, ".gastro")

	// index -> directory root
	route = strings.TrimSuffix(route, "/index")
	if route == "index" {
		route = ""
	}

	// Convert [param] to {param}
	route = convertParams(route)

	// Use {$} for the root path to get exact matching.
	// Without it, "/" is a subtree pattern that matches all paths.
	if route == "" {
		return "/{$}"
	}
	return "/" + route
}

// convertParams replaces [param] with {param} in route segments.
func convertParams(route string) string {
	segments := strings.Split(route, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
			// [slug] -> {slug}
			param := seg[1 : len(seg)-1]
			segments[i] = "{" + param + "}"
		}
	}
	return strings.Join(segments, "/")
}

// RouteToFuncName derives a Go function name from a page file path.
// e.g. "pages/index.gastro" -> "pageIndex"
// e.g. "pages/blog/[slug].gastro" -> "pageBlogSlug"
func RouteToFuncName(file string) string {
	name := file
	name = strings.TrimPrefix(name, "pages/")
	name = strings.TrimSuffix(name, ".gastro")
	name = strings.ReplaceAll(name, "[", "")
	name = strings.ReplaceAll(name, "]", "")

	// Split on / and - to create camelCase segments
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '-'
	})

	var result strings.Builder
	// First segment is always "page"
	result.WriteString("page")

	for _, part := range parts {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}

	return result.String()
}
