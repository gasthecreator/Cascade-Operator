package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCallWithAppRetrySucceedsFirstTry(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	status, err := callWithAppRetry(srv.URL, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (should not retry on success)", got)
	}
}

func TestCallWithAppRetryExhaustsAttemptsOnPersistentFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	status, err := callWithAppRetry(srv.URL, 3)
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (maxAttempts, this is the fan-out amplification)", got)
	}
}

func TestCallWithAppRetryStopsOnceHealthy(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	status, err := callWithAppRetry(srv.URL, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2 (stop retrying once a call succeeds)", got)
	}
}
