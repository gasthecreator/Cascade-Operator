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

package linkerd

import (
	"strings"
	"testing"

	"github.com/gasthecreator/Cascade-Operator/internal/mesh"
)

var _ mesh.QueryBuilder = QueryBuilder{}

func TestLatencyP99Query(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.LatencyP99Query("payments-service.linkerd-demo.svc.cluster.local", 30)
	want := `histogram_quantile(0.99, sum by (le) (rate(response_latency_ms_bucket{authority="payments-service.linkerd-demo.svc.cluster.local",direction="outbound"}[30s])))`
	if got != want {
		t.Fatalf("LatencyP99Query =\n%s\nwant\n%s", got, want)
	}
}

func TestErrorRateQuery(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.ErrorRateQuery("payments-service.linkerd-demo.svc.cluster.local", 30)
	want := `sum(rate(response_total{authority="payments-service.linkerd-demo.svc.cluster.local",direction="outbound",classification="failure"}[30s])) / sum(rate(response_total{authority="payments-service.linkerd-demo.svc.cluster.local",direction="outbound"}[30s]))`
	if got != want {
		t.Fatalf("ErrorRateQuery =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "5..") || strings.Contains(got, "status_code") {
		t.Errorf("Linkerd error rate must use classification=\"failure\", not an Istio-style status-code regex: %s", got)
	}
}

func TestErrorRateQueryVectorMatchingRequiresSum(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.ErrorRateQuery("payments-service.linkerd-demo.svc.cluster.local", 30)
	numerator := strings.SplitN(got, " / ", 2)[0]
	denominator := strings.SplitN(got, " / ", 2)[1]
	if !strings.HasPrefix(numerator, "sum(rate(") || !strings.HasSuffix(numerator, "))") {
		t.Errorf("numerator must be sum(rate(...)) so the division has exactly one series per side: %s", numerator)
	}
	if !strings.HasPrefix(denominator, "sum(rate(") || !strings.HasSuffix(denominator, "))") {
		t.Errorf("denominator must be sum(rate(...)) so the division has exactly one series per side: %s", denominator)
	}
}

func TestRetryStormRatioQuery(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.RetryStormRatioQuery("inventory-service.linkerd-demo.svc.cluster.local", 30)
	want := `sum(rate(route_actual_request_total{dst=~"inventory-service.linkerd-demo.svc.cluster.local:.*",direction="outbound"}[30s])) / sum(rate(route_request_total{dst=~"inventory-service.linkerd-demo.svc.cluster.local:.*",direction="outbound"}[30s]))`
	if got != want {
		t.Fatalf("RetryStormRatioQuery =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "reporter=") {
		t.Errorf("Linkerd retry-storm ratio must not carry over Istio's reporter label: %s", got)
	}
}

func TestFanOutRatioQuery(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.FanOutRatioQuery("inventory-service.linkerd-demo.svc.cluster.local", "checkout-service.linkerd-demo.svc.cluster.local", 30)
	want := `sum(rate(request_total{deployment="inventory-service",namespace="linkerd-demo",direction="inbound",authz_name!="probe"}[30s])) / sum(rate(request_total{deployment="checkout-service",namespace="linkerd-demo",direction="inbound",authz_name!="probe"}[30s]))`
	if got != want {
		t.Fatalf("FanOutRatioQuery =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "probe") == false {
		t.Errorf("fan-out query must exclude proxy admin/probe traffic: %s", got)
	}
}

func TestFanOutRatioQueryUnparseableHostDegradesToNoMatch(t *testing.T) {
	t.Parallel()
	got := QueryBuilder{}.FanOutRatioQuery("not-a-valid-fqdn", "checkout-service.linkerd-demo.svc.cluster.local", 30)
	if !strings.Contains(got, `deployment="not-a-valid-fqdn"`) {
		t.Fatalf("expected unparseable host to degrade to a literal, non-matching deployment label, got: %s", got)
	}
}
