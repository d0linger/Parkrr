package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLifecycleHandlerDrainsAndRejectsNewTraffic(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := newLifecycleHandler(nil)
	h.Activate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))

	requestDone := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/test", nil))
		close(requestDone)
	}()
	<-started

	drainDone := make(chan error, 1)
	go func() { drainDone <- h.EnterMaintenance(context.Background(), "job") }()
	select {
	case err := <-drainDone:
		t.Fatalf("drain returned while a request was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/test", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("new request status = %d, want 503", rec.Code)
	}

	close(release)
	<-requestDone
	if err := <-drainDone; err != nil {
		t.Fatalf("drain: %v", err)
	}
}

func TestLifecycleHandlerMaintenanceHealthAndReadiness(t *testing.T) {
	h := newLifecycleHandler(nil)
	if err := h.EnterMaintenance(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}

	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
	ready := httptest.NewRecorder()
	h.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", ready.Code)
	}
}
