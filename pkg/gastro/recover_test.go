package gastro

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecover_NoPanicDoesNothing(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	// No panic in flight; Recover should be a no-op.
	Recover(rec, r)

	if rec.Code != http.StatusOK { // default
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestRecover_PlainWriterWritesError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oops", nil)

	func() {
		defer Recover(rec, r)
		panic("boom")
	}()

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("body = %q, want \"Internal Server Error\"", rec.Body.String())
	}
}

func TestRecover_GastroWriterAfterBodyWriteLogsOnly(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)
	r := httptest.NewRequest(http.MethodGet, "/oops", nil)

	func() {
		defer Recover(w, r)
		w.Write([]byte("partial response"))
		panic("boom after body")
	}()

	// Body is already partially written; Recover must not append the 500
	// page or change the status.
	if got := rec.Body.String(); got != "partial response" {
		t.Errorf("body = %q, want %q (Recover overwrote partial response)", got, "partial response")
	}
	if rec.Code != http.StatusOK { // default for ResponseRecorder
		t.Errorf("status = %d, want default %d", rec.Code, http.StatusOK)
	}
}

func TestRecover_GastroWriterAfterHeaderOnlyLogsOnly(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)
	r := httptest.NewRequest(http.MethodGet, "/oops", nil)

	func() {
		defer Recover(w, r)
		w.WriteHeader(http.StatusCreated)
		panic("boom after header")
	}()

	// WriteHeader has already been called; appending a 500 via http.Error
	// would emit a "superfluous WriteHeader" warning and corrupt the
	// status. Recover must log only.
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 (preserved)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty (no body was written)", rec.Body.String())
	}
}

func TestRecover_GastroWriterBeforeAnyWriteWritesError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)
	r := httptest.NewRequest(http.MethodGet, "/oops", nil)

	func() {
		defer Recover(w, r)
		// Touch nothing; panic immediately.
		panic("boom before write")
	}()

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("body = %q, want \"Internal Server Error\"", rec.Body.String())
	}
}
