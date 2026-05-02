package gastro_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gastro "github.com/andrioid/gastro/pkg/gastro"
)

// TestDefaultErrorHandler_WritesUncommitted500: when the wrapped writer
// is fresh (no headers, no body), DefaultErrorHandler writes a 500.
func TestDefaultErrorHandler_WritesUncommitted500(t *testing.T) {
	rr := httptest.NewRecorder()
	w := gastro.NewPageWriter(rr)
	r := httptest.NewRequest("GET", "/", nil)

	gastro.DefaultErrorHandler(w, r, errors.New("boom"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Internal Server Error") {
		t.Errorf("body: got %q, want to contain 'Internal Server Error'", rr.Body.String())
	}
}

// TestDefaultErrorHandler_SilentAfterBodyWrite: once a body byte is on
// the wire, the handler logs only — writing 500 would interleave with
// whatever the template managed to flush.
func TestDefaultErrorHandler_SilentAfterBodyWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	w := gastro.NewPageWriter(rr)
	r := httptest.NewRequest("GET", "/", nil)

	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatalf("write: %v", err)
	}

	gastro.DefaultErrorHandler(w, r, errors.New("boom"))

	if rr.Code == http.StatusInternalServerError {
		t.Errorf("status: got 500, want it left alone (200 from the partial write)")
	}
	if got := rr.Body.String(); got != "partial" {
		t.Errorf("body: got %q, want %q (no 500 page appended)", got, "partial")
	}
}

// TestDefaultErrorHandler_SilentAfterHeaderCommit: WriteHeader alone
// commits the status; a follow-up 500 would log the stdlib's
// "superfluous response.WriteHeader call". The handler must skip.
func TestDefaultErrorHandler_SilentAfterHeaderCommit(t *testing.T) {
	rr := httptest.NewRecorder()
	w := gastro.NewPageWriter(rr)
	r := httptest.NewRequest("GET", "/", nil)

	w.WriteHeader(http.StatusCreated)

	gastro.DefaultErrorHandler(w, r, errors.New("boom"))

	if rr.Code != http.StatusCreated {
		t.Errorf("status: got %d, want 201 (preserved)", rr.Code)
	}
}

// TestDefaultErrorHandler_PlainResponseWriter: when w is not a
// gastro-owned wrapper (HeaderCommitted/BodyWritten both return false),
// the handler still writes 500. This keeps the contract usable from
// outside generated code.
func TestDefaultErrorHandler_PlainResponseWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	gastro.DefaultErrorHandler(rr, r, errors.New("boom"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500 on a plain ResponseWriter", rr.Code)
	}
}
