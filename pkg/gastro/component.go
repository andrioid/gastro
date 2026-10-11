package gastro

import (
	"errors"
	"html/template"
	"net/http"
)

// Component is a typed handle to a component in a generated gastro
// package. Each generated package exports one Component value per file
// under components/ (e.g. gastroweb.BoardColumn, a
// Component[gastroweb.BoardColumnProps]); render it through the
// generated router:
//
//	html, err := router.Render(gastroweb.BoardColumn, gastroweb.BoardColumnProps{…})
//
// The typed props go straight to the component's render function, with no
// map[string]any round-trip. Passing props of another component's type is
// a compile error.
type Component[P any] struct {
	render func(router any, req *http.Request, props P) (template.HTML, error)
	req    *http.Request
}

// NewComponent wraps a generated typed render function. It is called by
// generated code only; application code uses the exported component
// values of its generated package.
func NewComponent[P any](render func(router any, req *http.Request, props P) (template.HTML, error)) Component[P] {
	return Component[P]{render: render}
}

// With returns a copy of the component bound to req, so request-aware
// template helpers (WithRequestFuncs) resolve against that request's
// state, including inside nested components. A nil req renders through
// the static FuncMap, like an unbound component.
//
//	html, err := router.Render(gastroweb.Card.With(r), gastroweb.CardProps{…})
func (c Component[P]) With(req *http.Request) Component[P] {
	c.req = req
	return c
}

// RenderComponent invokes c's render function against router. It backs
// the generated Router.Render method and frontmatter's gastro.Render(c,
// props); application Go code calls router.Render(c, props) instead.
func RenderComponent[P any](router any, c Component[P], props P) (template.HTML, error) {
	if c.render == nil {
		return "", errors.New("gastro: Render called with a zero Component; use a component value from the generated package")
	}
	return c.render(router, c.req, props)
}
