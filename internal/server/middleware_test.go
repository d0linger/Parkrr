package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStatusRecorderUnwrap verifies that http.ResponseController reaches the
// wrapped writer's optional interfaces (Flush, SetWriteDeadline, ...) through
// the access-log wrapper — without Unwrap, clearWriteDeadline silently fails.
func TestStatusRecorderUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner, status: http.StatusOK}
	if err := http.NewResponseController(rec).Flush(); err != nil {
		t.Fatalf("Flush through statusRecorder: %v", err)
	}
	if !inner.Flushed {
		t.Error("inner recorder should report flushed")
	}
}
