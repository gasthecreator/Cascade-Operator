/*
Copyright 2026 Gideon Sanni.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientRejectsBadURL(t *testing.T) {
	t.Parallel()
	cases := []string{"", "not-a-url", "ftp://prom.example", "/relative"}
	for _, raw := range cases {
		if _, err := NewClient(raw, nil); err == nil {
			t.Errorf("NewClient(%q) succeeded, want error", raw)
		}
	}
}

func TestQueryVectorSuccess(t *testing.T) {
	t.Parallel()

	const promql = `rate(istio_requests_total[30s])`
	body := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{
					"metric": {
						"destination_service": "payments.default.svc.cluster.local",
						"response_code": "500"
					},
					"value": [1600000000.5, "0.05"]
				}
			]
		}
	}`

	srv := prometheusServer(t, http.StatusOK, body, func(r *http.Request) {
		if r.URL.Path != instantQueryPath {
			t.Errorf("path = %q, want %s", r.URL.Path, instantQueryPath)
		}
		if got := r.URL.Query().Get("query"); got != promql {
			t.Errorf("query = %q, want %q", got, promql)
		}
	})

	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	snap, err := c.Query(context.Background(), promql)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(snap.Samples) != 1 {
		t.Fatalf("len(Samples) = %d, want 1", len(snap.Samples))
	}
	s := snap.Samples[0]
	wantLabels := map[string]string{
		"destination_service": "payments.default.svc.cluster.local",
		"response_code":       "500",
	}
	if !maps.Equal(s.Labels, wantLabels) {
		t.Errorf("Labels = %#v, want %#v", s.Labels, wantLabels)
	}
	if s.Value != 0.05 {
		t.Errorf("Value = %v, want 0.05", s.Value)
	}
	wantTS := unixSeconds(1600000000.5)
	if !s.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %s, want %s", s.Timestamp, wantTS)
	}
}

func TestQueryEmptyVector(t *testing.T) {
	t.Parallel()
	body := `{"status":"success","data":{"resultType":"vector","result":[]}}`
	srv := prometheusServer(t, http.StatusOK, body, nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(snap.Samples) != 0 {
		t.Errorf("len(Samples) = %d, want 0", len(snap.Samples))
	}
}

func TestQueryScalarSuccess(t *testing.T) {
	t.Parallel()
	body := `{"status":"success","data":{"resultType":"scalar","result":[1600000000,"1"]}}`
	srv := prometheusServer(t, http.StatusOK, body, nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Query(context.Background(), "vector(1)")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(snap.Samples) != 1 {
		t.Fatalf("len(Samples) = %d, want 1", len(snap.Samples))
	}
	if s := snap.Samples[0]; s.Value != 1 || len(s.Labels) != 0 {
		t.Errorf("scalar sample = %+v", s)
	}
}

func TestQueryPrometheusErrorStatus(t *testing.T) {
	t.Parallel()
	body := `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": parse error"}`
	srv := prometheusServer(t, http.StatusOK, body, nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Query(context.Background(), "not-promql")
	if err == nil {
		t.Fatal("Query succeeded, want error")
	}
	if !strings.Contains(err.Error(), "bad_data") {
		t.Errorf("error %q: want bad_data", err)
	}
}

// TestQueryPrometheusErrorStatusCode covers the shape real Prometheus
// actually sends for query errors: a non-200 status (422 here) carrying the
// same {status,errorType,error} envelope as the 200 case above. Without
// parseErrorBody, this would fall through to a raw truncated-body message
// instead of surfacing errorType/error.
func TestQueryPrometheusErrorStatusCode(t *testing.T) {
	t.Parallel()
	body := `{"status":"error","errorType":"execution","error":"vector cannot contain metrics with the same labelset"}`
	srv := prometheusServer(t, http.StatusUnprocessableEntity, body, nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("Query succeeded, want error")
	}
	if !strings.Contains(err.Error(), "execution") || !strings.Contains(err.Error(), "labelset") {
		t.Errorf("error %q: want errorType and error message surfaced", err)
	}
}

func TestQueryHTTPError(t *testing.T) {
	t.Parallel()
	srv := prometheusServer(t, http.StatusInternalServerError, "upstream exploded", nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("Query succeeded, want error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error %q: want HTTP 500", err)
	}
}

func TestQueryMalformedJSON(t *testing.T) {
	t.Parallel()
	srv := prometheusServer(t, http.StatusOK, `{not json`, nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("Query succeeded, want error")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error %q: want decode response", err)
	}
}

func TestQueryRejectsMatrix(t *testing.T) {
	t.Parallel()
	body := `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	srv := prometheusServer(t, http.StatusOK, body, nil)
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("Query succeeded, want error")
	}
	if !strings.Contains(err.Error(), "matrix") {
		t.Errorf("error %q: want matrix", err)
	}
}

func TestQueryEmptyPromQL(t *testing.T) {
	t.Parallel()
	c, err := NewClient("http://127.0.0.1:9090", &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(context.Background(), "  "); err == nil {
		t.Fatal("Query(empty) succeeded, want error")
	}
}

func TestQueryContextCanceled(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, qerr := c.Query(ctx, "up")
		errCh <- qerr
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the request")
	}
	cancel()

	select {
	case qerr := <-errCh:
		if qerr == nil {
			t.Fatal("Query succeeded, want cancellation error")
		}
		if !errors.Is(qerr, context.Canceled) && !errors.Is(qerr, context.DeadlineExceeded) {
			// httptest/http wrap the cancel; accept timeout or canceled, plus
			// the generic "context canceled" string from net/http.
			if !strings.Contains(qerr.Error(), "context canceled") &&
				!strings.Contains(qerr.Error(), "canceled") {
				t.Errorf("error %q: want context cancellation", qerr)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Query did not return after cancel")
	}
}

func TestQueryContextTimeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = c.Query(ctx, "up")
	if err == nil {
		t.Fatal("Query succeeded, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") &&
		!strings.Contains(err.Error(), "context") {
		t.Errorf("error %q: want deadline/context", err)
	}
}

func prometheusServer(t *testing.T, status int, body string, check func(*http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
