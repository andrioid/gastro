// Package datastar provides Datastar-specific helpers on top of gastro's
// generic SSE support. It formats events using Datastar's SSE protocol
// (datastar-patch-elements, datastar-patch-signals) so users don't have
// to manually construct the data lines.
//
// See https://data-star.dev/reference/sse_events for the protocol spec.
package datastar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/andrioid/gastro/pkg/gastro"
)

// SSE wraps gastro.SSE with Datastar-specific convenience methods.
type SSE struct {
	*gastro.SSE
}

// NewSSE upgrades an http.ResponseWriter to a Datastar SSE stream.
func NewSSE(w http.ResponseWriter, r *http.Request) *SSE {
	return &SSE{SSE: gastro.NewSSE(w, r)}
}

// PatchMode controls how elements are patched into the DOM.
type PatchMode string

const (
	ModeOuter   PatchMode = "outer"
	ModeInner   PatchMode = "inner"
	ModeReplace PatchMode = "replace"
	ModePrepend PatchMode = "prepend"
	ModeAppend  PatchMode = "append"
	ModeBefore  PatchMode = "before"
	ModeAfter   PatchMode = "after"
	ModeRemove  PatchMode = "remove"
)

type patchConfig struct {
	selector string
	mode     PatchMode
}

// PatchOption configures a PatchElements call.
type PatchOption func(*patchConfig)

// WithSelector sets a CSS selector for the target element.
func WithSelector(sel string) PatchOption {
	return func(c *patchConfig) { c.selector = sel }
}

// WithMode sets the patch mode (defaults to "outer").
func WithMode(mode PatchMode) PatchOption {
	return func(c *patchConfig) { c.mode = mode }
}

// PatchElements sends a datastar-patch-elements event.
// The html string should contain elements with id attributes for morphing.
func (s *SSE) PatchElements(html string, opts ...PatchOption) error {
	cfg := patchConfig{mode: ModeOuter}
	for _, opt := range opts {
		opt(&cfg)
	}

	var dataLines []string

	if cfg.selector != "" {
		dataLines = append(dataLines, "selector "+cfg.selector)
	}
	if cfg.mode != ModeOuter {
		dataLines = append(dataLines, "mode "+string(cfg.mode))
	}

	if html != "" {
		// Normalize \r\n and bare \r to \n before splitting so that
		// carriage returns in user content cannot break out of the
		// "data: elements ..." SSE line and inject new SSE fields.
		html = strings.ReplaceAll(html, "\r\n", "\n")
		html = strings.ReplaceAll(html, "\r", "\n")
		for _, line := range strings.Split(html, "\n") {
			dataLines = append(dataLines, "elements "+line)
		}
	}

	return s.Send("datastar-patch-elements", dataLines...)
}

// PatchSignals sends a datastar-patch-signals event.
// The signals value is JSON-encoded before sending.
func (s *SSE) PatchSignals(signals any) error {
	b, err := json.Marshal(signals)
	if err != nil {
		return fmt.Errorf("datastar: marshal signals: %w", err)
	}

	return s.Send("datastar-patch-signals", "signals "+string(b))
}

// RemoveElement sends a datastar-patch-elements event with mode "remove"
// targeting the given CSS selector.
func (s *SSE) RemoveElement(selector string) error {
	return s.PatchElements("",
		WithSelector(selector),
		WithMode(ModeRemove),
	)
}
