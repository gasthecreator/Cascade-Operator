package depsvc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthyByDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(NewMux("test"))
	t.Cleanup(srv.Close)

	if status := mustGet(t, srv.URL+"/"); status != http.StatusOK {
		t.Errorf("status = %d, want 200 (healthy by default)", status)
	}
}

func TestFailReturns500UntilHealed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(NewMux("test"))
	t.Cleanup(srv.Close)

	mustGet(t, srv.URL+"/control/fail")
	if status := mustGet(t, srv.URL+"/"); status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 while failing", status)
	}
	mustGet(t, srv.URL+"/control/heal")
	if status := mustGet(t, srv.URL+"/"); status != http.StatusOK {
		t.Errorf("status = %d, want 200 after heal", status)
	}
}

// TestSlowEveryFifthRequestErrorsTheRest200 pins the exact ratio the
// latency/error-cascade k6 script depends on: every request while slow
// sleeps slowLatency (so p99 crosses the CRD sample's 500ms threshold
// regardless of which requests error), and exactly 1 in slowErrorEvery
// (5) comes back 500 — a fixed, deterministic 20% error rate rather than
// a randomized one, so this test (and the k6 script's own expectations)
// never flakes on which requests happened to "roll" a failure.
func TestSlowEveryFifthRequestErrorsTheRest200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(NewMux("test"))
	t.Cleanup(srv.Close)

	mustGet(t, srv.URL+"/control/slow")

	var errors, ok int
	const n = 10 // 2 full cycles of slowErrorEvery=5, exact 20% expected
	for i := range n {
		switch status := mustGet(t, srv.URL+"/"); status {
		case http.StatusOK:
			ok++
		case http.StatusInternalServerError:
			errors++
		default:
			t.Errorf("request %d: unexpected status %d", i, status)
		}
	}
	if want := n / slowErrorEvery; errors != want {
		t.Errorf("errors = %d, want %d (exactly 1 in every %d while slow)", errors, want, slowErrorEvery)
	}
	if want := n - n/slowErrorEvery; ok != want {
		t.Errorf("ok = %d, want %d", ok, want)
	}
}

func TestHealResetsBothFailingAndSlow(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(NewMux("test"))
	t.Cleanup(srv.Close)

	mustGet(t, srv.URL+"/control/fail")
	mustGet(t, srv.URL+"/control/slow")
	mustGet(t, srv.URL+"/control/heal")

	if status := mustGet(t, srv.URL+"/"); status != http.StatusOK {
		t.Errorf("status = %d, want 200 (heal must clear both failing and slow)", status)
	}
}

func mustGet(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx,gosec // test helper, url is always the local httptest server
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}
