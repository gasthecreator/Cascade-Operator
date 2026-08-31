// Package depsvc is the shared implementation behind the demo topology's
// two leaf dependencies (payments, inventory). It is intentionally minimal:
// a "/" handler that answers healthy until told otherwise, and control
// endpoints to flip that state at runtime — this exists to let the
// evidence-gathering slices (PLAN.md §2.7) turn a dependency's failure (or
// slowness) on and off from the outside (curl, or a k6 script) without
// redeploying anything or reaching for Istio fault injection.
package depsvc

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// slowLatency and slowErrorEvery are the k6 latency/error-cascade script's
// induced values, not tunable at runtime — keeping this a fixed constant
// pair (like the mitigation package's own trip constants) rather than a
// query-string-configurable knob keeps the demo's story simple: "slow
// mode" always means the same thing. slowLatency (800ms) clears the CRD
// sample's default latencyP99Ms (500) by a wide margin so p99 crosses
// threshold even with normal request jitter, the same "comfortably above,
// not right at the line" margin fanOutMultiplier/retryStormMultiplier's
// demo values already use relative to their own thresholds. Every request
// sleeps while slow (not a fraction of them) because histogram_quantile's
// p99 only crosses 500ms if the vast majority of the window's samples are
// already above it — a partial-latency fraction would need to be very
// large to move p99 at all, and "some requests fail outright" is already
// the error-rate half of the signal, not the latency half.
const (
	slowLatency    = 800 * time.Millisecond
	slowErrorEvery = 5 // every 5th request errors while slow: 20% error rate, 4x the 0.05 default threshold
)

// NewMux builds the routing this service exposes: "/" answers according to
// current toggle state, "/control/{fail,slow,heal}" flip that state,
// "/healthz" is the k8s probe. Split out from Run so tests can exercise the
// real handler over httptest instead of a duplicated fake — Run itself is
// just this plus http.ListenAndServe.
func NewMux(name string) *http.ServeMux {
	var failing atomic.Bool
	var slow atomic.Bool
	var slowRequestCount atomic.Uint64

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		if slow.Load() {
			time.Sleep(slowLatency)
			if slowRequestCount.Add(1)%slowErrorEvery == 0 {
				http.Error(w, fmt.Sprintf("%s: slow and failing\n", name), http.StatusInternalServerError)
				return
			}
		}
		if failing.Load() {
			http.Error(w, fmt.Sprintf("%s: failing\n", name), http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(w, "%s: ok\n", name)
	})
	mux.HandleFunc("/control/fail", func(w http.ResponseWriter, _ *http.Request) {
		failing.Store(true)
		_, _ = fmt.Fprintf(w, "%s: now failing\n", name)
	})
	mux.HandleFunc("/control/slow", func(w http.ResponseWriter, _ *http.Request) {
		slow.Store(true)
		_, _ = fmt.Fprintf(w, "%s: now slow\n", name)
	})
	mux.HandleFunc("/control/heal", func(w http.ResponseWriter, _ *http.Request) {
		failing.Store(false)
		slow.Store(false)
		_, _ = fmt.Fprintf(w, "%s: healed\n", name)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// Run starts an HTTP server named name on addr and blocks until it exits.
func Run(name, addr string) error {
	log.Printf("%s listening on %s", name, addr)
	return http.ListenAndServe(addr, NewMux(name))
}
