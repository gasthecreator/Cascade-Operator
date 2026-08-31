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

package istio

import (
	"strings"
	"testing"

	"github.com/gasthecreator/Cascade-Operator/internal/mesh"
)

// Moved verbatim from internal/controller/promql_test.go (PLAN.md §5 Phase
// 6.1) alongside the query-builder functions themselves.
var _ mesh.QueryBuilder = QueryBuilder{}

func TestLatencyP99Query(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.LatencyP99Query("payments-service.default.svc.cluster.local", 30)
	want := `histogram_quantile(0.99, sum by (le) (rate(istio_request_duration_milliseconds_bucket{destination_service="payments-service.default.svc.cluster.local",reporter="source"}[30s])))`
	if got != want {
		t.Fatalf("LatencyP99Query =\n%s\nwant\n%s", got, want)
	}
}

func TestRetryStormRatioQuery(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.RetryStormRatioQuery("payments-service.default.svc.cluster.local", 30)
	want := `sum(rate(istio_requests_total{destination_service="payments-service.default.svc.cluster.local",reporter="destination"}[30s])) / sum(rate(istio_requests_total{destination_service="payments-service.default.svc.cluster.local",reporter="source"}[30s]))`
	if got != want {
		t.Fatalf("RetryStormRatioQuery =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "URX") || strings.Contains(got, "response_flags") {
		t.Errorf("retry-storm ratio must not filter URX/response_flags: %s", got)
	}
}

func TestFanOutRatioQuery(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.FanOutRatioQuery("payments-service.default.svc.cluster.local", "checkout-service.default.svc.cluster.local", 30)
	want := `sum(rate(istio_requests_total{destination_service="payments-service.default.svc.cluster.local",reporter="destination"}[30s])) / sum(rate(istio_requests_total{destination_service="checkout-service.default.svc.cluster.local",reporter="destination"}[30s]))`
	if got != want {
		t.Fatalf("FanOutRatioQuery =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, `reporter="source"`) {
		t.Errorf("fan-out ratio must not use reporter=source (cross-host, not same-host reporter split): %s", got)
	}
}

func TestErrorRateQuery(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.ErrorRateQuery("payments-service.default.svc.cluster.local", 30)
	want := `sum(rate(istio_requests_total{destination_service="payments-service.default.svc.cluster.local",response_code=~"5.."}[30s])) / sum(rate(istio_requests_total{destination_service="payments-service.default.svc.cluster.local"}[30s]))`
	if got != want {
		t.Fatalf("ErrorRateQuery =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "response_flags") {
		t.Errorf("latency/error queries must not use response_flags: %s", got)
	}
}

// TestErrorRateQueryVectorMatchingRequiresSum locks in *why* both sides need
// sum(): without it, Prometheus's default one-to-one vector matching pairs
// series by their full label set (response_code included), which — verified
// live against the dev cluster (docs/worklog/2026-08-31-error-rate-query-sum-fix.md)
// — produces NaN or a spurious flat 1.0 depending on live traffic
// composition, never the true error fraction. This regression-locks the
// query shape a fake/stub PromQL client cannot catch, since a stub that
// returns a canned scalar for any query text masks label-cardinality bugs
// entirely (the same class of gap the retry-storm restore bug thread
// documented — see docs/worklog/2026-08-31-phase5-retry-storm-restore-zero-value.md).
func TestErrorRateQueryVectorMatchingRequiresSum(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.ErrorRateQuery("payments-service.default.svc.cluster.local", 30)
	numerator := strings.SplitN(got, " / ", 2)[0]
	denominator := strings.SplitN(got, " / ", 2)[1]
	if !strings.HasPrefix(numerator, "sum(rate(") || !strings.HasSuffix(numerator, "))") {
		t.Errorf("numerator must be sum(rate(...)) so the division has exactly one series per side: %s", numerator)
	}
	if !strings.HasPrefix(denominator, "sum(rate(") || !strings.HasSuffix(denominator, "))") {
		t.Errorf("denominator must be sum(rate(...)) so the division has exactly one series per side: %s", denominator)
	}
}
