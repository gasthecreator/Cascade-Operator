// Command checkout is the caller in the §2.7 demo topology (checkout ->
// {payments, inventory}) — the fan-out signature's evidence-gathering
// workload (see the fan-out-evidence worklog). Its /checkout handler calls
// both dependencies per inbound request; payments additionally gets an
// application-level retry-on-failure loop, deliberately separate from any
// Envoy-level retry policy (that's the retry-storm signature), so a single
// downstream failure can turn into several outbound calls instead of one.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// paymentsMaxAttempts is how many times checkout will re-call payments
// before giving up on a non-2xx response. inventory gets no such loop —
// keeping one dependency's call count fixed at 1 makes it the control the
// evidence slice compares payments' amplified count against.
const paymentsMaxAttempts = 3

var httpClient = &http.Client{Timeout: 3 * time.Second}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// callWithAppRetry issues an HTTP GET to url, retrying up to maxAttempts
// times as long as the previous attempt returned a non-2xx status. Each
// attempt is a fully independent HTTP request through the sidecar, not an
// Envoy retry — that distinction is the point (see the package doc).
func callWithAppRetry(url string, maxAttempts int) (status int, err error) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, reqErr := httpClient.Get(url) //nolint:noctx // demo traffic generator, not production code
		if reqErr != nil {
			err = reqErr
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		status = resp.StatusCode
		if status < 300 {
			return status, nil
		}
		err = fmt.Errorf("status %d", status)
	}
	return status, err
}

func main() {
	paymentsURL := envOr("PAYMENTS_URL", "http://payments-service.default.svc.cluster.local/")
	inventoryURL := envOr("INVENTORY_URL", "http://inventory-service.default.svc.cluster.local/")

	mux := http.NewServeMux()
	mux.HandleFunc("/checkout", func(w http.ResponseWriter, _ *http.Request) {
		payStatus, payErr := callWithAppRetry(paymentsURL, paymentsMaxAttempts)
		invStatus, invErr := callWithAppRetry(inventoryURL, 1)

		if payErr != nil || invErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprintf(w, "checkout failed: payments=%d (%v) inventory=%d (%v)\n", payStatus, payErr, invStatus, invErr)
			return
		}
		_, _ = fmt.Fprintf(w, "checkout ok: payments=%d inventory=%d\n", payStatus, invStatus)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Println("checkout listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
