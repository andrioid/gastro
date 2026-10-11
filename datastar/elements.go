package datastar

import (
	sdk "github.com/starfederation/datastar-go/datastar"
)

// PatchMode controls how patched elements are applied to the DOM.
type PatchMode = sdk.ElementPatchMode

// Patch modes. ModeOuter (morph the element with the matching id) is the
// default and is omitted from the wire.
const (
	ModeOuter   = sdk.ElementPatchModeOuter
	ModeInner   = sdk.ElementPatchModeInner
	ModeReplace = sdk.ElementPatchModeReplace
	ModePrepend = sdk.ElementPatchModePrepend
	ModeAppend  = sdk.ElementPatchModeAppend
	ModeBefore  = sdk.ElementPatchModeBefore
	ModeAfter   = sdk.ElementPatchModeAfter
	ModeRemove  = sdk.ElementPatchModeRemove
)

// Namespace is the XML namespace patched elements are created in.
type Namespace = sdk.Namespace

// Namespaces. NamespaceHTML is the default and is omitted from the wire.
const (
	NamespaceHTML   = sdk.NamespaceHTML
	NamespaceSVG    = sdk.NamespaceSVG
	NamespaceMathML = sdk.NamespaceMathML
)

// PatchElementOption configures [SSE.PatchElements].
type PatchElementOption func(*patchElementConfig)

type patchElementConfig struct {
	selector               string
	mode                   PatchMode
	namespace              Namespace
	useViewTransitions     bool
	viewTransitionSelector string
}

// WithSelector targets the elements matching a CSS selector instead of
// matching by id.
func WithSelector(selector string) PatchElementOption {
	return func(c *patchElementConfig) { c.selector = selector }
}

// WithMode sets the patch mode (default [ModeOuter]).
func WithMode(mode PatchMode) PatchElementOption {
	return func(c *patchElementConfig) { c.mode = mode }
}

// WithNamespace creates the patched elements in the given namespace, e.g.
// [NamespaceSVG] for SVG fragments.
func WithNamespace(ns Namespace) PatchElementOption {
	return func(c *patchElementConfig) { c.namespace = ns }
}

// WithUseViewTransitions wraps the patch in a View Transition where the
// browser supports it.
func WithUseViewTransitions(use bool) PatchElementOption {
	return func(c *patchElementConfig) { c.useViewTransitions = use }
}

// WithViewTransitionSelector limits the view transition to elements matching
// selector. It only takes effect together with WithUseViewTransitions(true).
func WithViewTransitionSelector(selector string) PatchElementOption {
	return func(c *patchElementConfig) { c.viewTransitionSelector = selector }
}

func (c *patchElementConfig) sdkOptions() []sdk.PatchElementOption {
	opts := make([]sdk.PatchElementOption, 0, 5)
	if c.selector != "" {
		opts = append(opts, sdk.WithSelector(singleLine(c.selector)))
	}
	if c.mode != "" {
		opts = append(opts, sdk.WithMode(PatchMode(singleLine(string(c.mode)))))
	}
	if c.namespace != "" {
		opts = append(opts, sdk.WithNamespace(Namespace(singleLine(string(c.namespace)))))
	}
	if c.useViewTransitions {
		opts = append(opts, sdk.WithUseViewTransitions(true))
	}
	if c.viewTransitionSelector != "" {
		opts = append(opts, sdk.WithViewTransitionSelector(singleLine(c.viewTransitionSelector)))
	}
	return opts
}

// PatchElements sends a datastar-patch-elements event. With the default
// options, every top-level element in html must carry an id; Datastar
// morphs it into the element with the same id. CRLF and CR line endings
// in html are normalised to LF.
func (s *SSE) PatchElements(html string, opts ...PatchElementOption) error {
	var cfg patchElementConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return s.gen.PatchElements(normalizeNewlines(html), cfg.sdkOptions()...)
}

// RemoveElement removes the elements matching selector.
func (s *SSE) RemoveElement(selector string) error {
	return s.PatchElements("", WithSelector(selector), WithMode(ModeRemove))
}
