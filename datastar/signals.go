package datastar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	sdk "github.com/starfederation/datastar-go/datastar"
)

// PatchSignalsOption configures [SSE.PatchSignals].
type PatchSignalsOption func(*patchSignalsConfig)

type patchSignalsConfig struct {
	onlyIfMissing bool
}

// WithOnlyIfMissing only sets signals the client does not already have.
func WithOnlyIfMissing(onlyIfMissing bool) PatchSignalsOption {
	return func(c *patchSignalsConfig) { c.onlyIfMissing = onlyIfMissing }
}

func (c *patchSignalsConfig) sdkOptions() []sdk.PatchSignalsOption {
	if !c.onlyIfMissing {
		return nil
	}
	return []sdk.PatchSignalsOption{sdk.WithOnlyIfMissing(true)}
}

// PatchSignals sends a datastar-patch-signals event carrying signals, a
// JSON merge patch (RFC 7396) applied to the client's signal store. Setting
// a signal to null removes it. CRLF and CR line endings are normalised to LF.
func (s *SSE) PatchSignals(signals []byte, opts ...PatchSignalsOption) error {
	var cfg patchSignalsConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if bytes.IndexByte(signals, '\r') >= 0 {
		signals = []byte(normalizeNewlines(string(signals)))
	}
	return s.gen.PatchSignals(signals, cfg.sdkOptions()...)
}

// MarshalAndPatchSignals JSON-encodes v and sends it with [SSE.PatchSignals].
func (s *SSE) MarshalAndPatchSignals(v any, opts ...PatchSignalsOption) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("datastar: marshal signals: %w", err)
	}
	return s.PatchSignals(b, opts...)
}

// ReadSignals decodes the signals Datastar sent with r into v: from the
// "datastar" query parameter for GET and DELETE requests, from the JSON body
// otherwise. A GET or DELETE without signals leaves v untouched.
//
// Call it before [NewSSE]; the stream takes over the response.
func ReadSignals(r *http.Request, v any) error {
	if err := sdk.ReadSignals(r, v); err != nil {
		return fmt.Errorf("datastar: read signals: %w", err)
	}
	return nil
}
