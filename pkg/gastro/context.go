package gastro

import "net/http"

// Context provides access to the HTTP request and response for page handlers.
type Context struct {
	w http.ResponseWriter
	r *http.Request
}

// NewContext creates a new Context from an HTTP request/response pair.
func NewContext(w http.ResponseWriter, r *http.Request) *Context {
	return &Context{w: w, r: r}
}

// Request returns the underlying *http.Request.
func (c *Context) Request() *http.Request {
	return c.r
}

// Param returns a URL path parameter by name (from [param] route segments).
func (c *Context) Param(name string) string {
	return c.r.PathValue(name)
}

// Query returns a query string parameter by name.
func (c *Context) Query(name string) string {
	return c.r.URL.Query().Get(name)
}

// Redirect sends an HTTP redirect response. The caller must return after this.
func (c *Context) Redirect(url string, code int) {
	http.Redirect(c.w, c.r, url, code)
}

// Error sends an HTTP error response. The caller must return after this.
func (c *Context) Error(code int, msg string) {
	http.Error(c.w, msg, code)
}

// Header sets a response header.
func (c *Context) Header(key, val string) {
	c.w.Header().Set(key, val)
}

// SSE upgrades the response to a Server-Sent Events stream.
// The caller should use the returned SSE to send events, then return
// from the handler to close the connection.
func (c *Context) SSE() *SSE {
	return NewSSE(c.w, c.r)
}
