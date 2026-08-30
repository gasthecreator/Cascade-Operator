// Package depsvc is the shared implementation behind the demo topology's
// two leaf dependencies (payments, inventory). It is intentionally minimal:
// a "/" handler that answers healthy until told otherwise, and two control
// endpoints to flip that state at runtime — this exists to let the fan-out
// evidence-gathering slice (PLAN.md §2.7) turn a dependency's failure on
// and off from the outside (curl) without redeploying anything or reaching
// for Istio fault injection.
package depsvc

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

// Run starts an HTTP server named name on addr and blocks until it exits.
func Run(name, addr string) error {
	var failing atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
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
	mux.HandleFunc("/control/heal", func(w http.ResponseWriter, _ *http.Request) {
		failing.Store(false)
		_, _ = fmt.Fprintf(w, "%s: healed\n", name)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("%s listening on %s", name, addr)
	return http.ListenAndServe(addr, mux)
}
