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

package controller

import (
	"strings"
	"testing"

	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

func TestLatencyP99Query(t *testing.T) {
	t.Parallel()
	got := latencyP99Query("payments-service.default.svc.cluster.local", 30)
	want := `histogram_quantile(0.99, rate(istio_request_duration_milliseconds_bucket{destination_service="payments-service.default.svc.cluster.local"}[30s]))`
	if got != want {
		t.Fatalf("latencyP99Query =\n%s\nwant\n%s", got, want)
	}
}

func TestErrorRateQuery(t *testing.T) {
	t.Parallel()
	got := errorRateQuery("payments-service.default.svc.cluster.local", 30)
	if !strings.Contains(got, `destination_service="payments-service.default.svc.cluster.local"`) {
		t.Errorf("missing verbatim host: %s", got)
	}
	if !strings.Contains(got, `response_code=~"5.."`) {
		t.Errorf("missing 5xx matcher: %s", got)
	}
	if !strings.Contains(got, "[30s]") {
		t.Errorf("missing window: %s", got)
	}
	if strings.Contains(got, "response_flags") {
		t.Errorf("latency/error queries must not use response_flags: %s", got)
	}
}

func TestSnapshotMax(t *testing.T) {
	t.Parallel()
	if _, ok := snapshotMax(metrics.Snapshot{}); ok {
		t.Fatal("empty snapshot should be missing")
	}
	s := metrics.Snapshot{Samples: []metrics.Sample{{Value: 1}, {Value: 3}, {Value: 2}}}
	v, ok := snapshotMax(s)
	if !ok || v != 3 {
		t.Fatalf("snapshotMax = %v, %v; want 3, true", v, ok)
	}
}
