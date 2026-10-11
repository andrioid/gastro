package datastar

import (
	sdk "github.com/starfederation/datastar-go/datastar"
)

// ExecuteScriptOption configures [SSE.ExecuteScript], [SSE.Redirect] and
// [SSE.ConsoleLog].
type ExecuteScriptOption func(*executeScriptConfig)

type executeScriptConfig struct {
	autoRemove *bool
	attributes []string
}

// WithAutoRemove controls whether the injected <script> removes itself after
// running (default true).
func WithAutoRemove(autoRemove bool) ExecuteScriptOption {
	return func(c *executeScriptConfig) { c.autoRemove = &autoRemove }
}

// WithAttributes adds raw attributes (e.g. `type="module"`) to the injected
// <script> tag. They are not escaped; never pass untrusted input.
func WithAttributes(attributes ...string) ExecuteScriptOption {
	return func(c *executeScriptConfig) { c.attributes = attributes }
}

func scriptOptions(opts []ExecuteScriptOption) []sdk.ExecuteScriptOption {
	var cfg executeScriptConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	out := make([]sdk.ExecuteScriptOption, 0, 2)
	if cfg.autoRemove != nil {
		out = append(out, sdk.WithExecuteScriptAutoRemove(*cfg.autoRemove))
	}
	if len(cfg.attributes) > 0 {
		attrs := make([]string, len(cfg.attributes))
		for i, a := range cfg.attributes {
			attrs[i] = normalizeNewlines(a)
		}
		out = append(out, sdk.WithExecuteScriptAttributes(attrs...))
	}
	return out
}

// ExecuteScript runs JavaScript in the browser by appending a <script> to
// <body>. CRLF and CR line endings are normalised to LF.
func (s *SSE) ExecuteScript(script string, opts ...ExecuteScriptOption) error {
	return s.gen.ExecuteScript(normalizeNewlines(script), scriptOptions(opts)...)
}

// Redirect navigates the browser to url.
func (s *SSE) Redirect(url string, opts ...ExecuteScriptOption) error {
	return s.gen.Redirect(url, scriptOptions(opts)...)
}

// ConsoleLog logs msg to the browser console.
func (s *SSE) ConsoleLog(msg string, opts ...ExecuteScriptOption) error {
	return s.gen.ConsoleLog(msg, scriptOptions(opts)...)
}

// DispatchCustomEventOption configures [SSE.DispatchCustomEvent].
type DispatchCustomEventOption func(*dispatchConfig)

type dispatchConfig struct {
	selector                      string
	bubbles, cancelable, composed *bool
}

// WithDispatchSelector dispatches the event on every element matching
// selector instead of on document.
func WithDispatchSelector(selector string) DispatchCustomEventOption {
	return func(c *dispatchConfig) { c.selector = selector }
}

// WithDispatchBubbles sets the event's bubbles flag (default true).
func WithDispatchBubbles(bubbles bool) DispatchCustomEventOption {
	return func(c *dispatchConfig) { c.bubbles = &bubbles }
}

// WithDispatchCancelable sets the event's cancelable flag (default true).
func WithDispatchCancelable(cancelable bool) DispatchCustomEventOption {
	return func(c *dispatchConfig) { c.cancelable = &cancelable }
}

// WithDispatchComposed sets the event's composed flag (default true).
func WithDispatchComposed(composed bool) DispatchCustomEventOption {
	return func(c *dispatchConfig) { c.composed = &composed }
}

// DispatchCustomEvent dispatches a browser CustomEvent named name whose
// detail is the JSON encoding of detail.
func (s *SSE) DispatchCustomEvent(name string, detail any, opts ...DispatchCustomEventOption) error {
	var cfg dispatchConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	sdkOpts := make([]sdk.DispatchCustomEventOption, 0, 4)
	if cfg.selector != "" {
		sdkOpts = append(sdkOpts, sdk.WithDispatchCustomEventSelector(cfg.selector))
	}
	if cfg.bubbles != nil {
		sdkOpts = append(sdkOpts, sdk.WithDispatchCustomEventBubbles(*cfg.bubbles))
	}
	if cfg.cancelable != nil {
		sdkOpts = append(sdkOpts, sdk.WithDispatchCustomEventCancelable(*cfg.cancelable))
	}
	if cfg.composed != nil {
		sdkOpts = append(sdkOpts, sdk.WithDispatchCustomEventComposed(*cfg.composed))
	}
	return s.gen.DispatchCustomEvent(name, detail, sdkOpts...)
}
