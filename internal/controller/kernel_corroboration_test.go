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
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
	"github.com/gasthecreator/Cascade-Operator/internal/signatures"
)

// testTrippedEvidence is arbitrary, already-tripped-verdict evidence text —
// these tests only care that applyKernelCorroboration preserves or extends
// it, not which signature it came from.
const testTrippedEvidence = "some_signature_tripped=true"

func TestTetragonKernelEventCountQuery(t *testing.T) {
	t.Parallel()
	got := tetragonKernelEventCountQuery("inventory-service.linkerd-demo.svc.cluster.local", 30)
	want := `sum(increase(tetragon_events_total{namespace="linkerd-demo",workload="inventory-service",type="PROCESS_KPROBE"}[30s]))`
	if got != want {
		t.Fatalf("tetragonKernelEventCountQuery =\n%s\nwant\n%s", got, want)
	}
}

func TestTetragonKernelEventCountQueryUnparseableHostDegradesToNoMatch(t *testing.T) {
	t.Parallel()
	got := tetragonKernelEventCountQuery("not-a-valid-fqdn", 30)
	if !strings.Contains(got, `workload="not-a-valid-fqdn"`) {
		t.Fatalf("expected unparseable host to degrade to a literal, non-matching workload label, got: %s", got)
	}
}

// kernelQuerier is a minimal metrics.Querier stub, local to this test file
// (not the package-wide fakeQuerier, which is shaped around the four mesh
// detection queries, not this fifth, mesh-agnostic one).
type kernelQuerier struct {
	value float64
	empty bool
	err   error
}

func (q *kernelQuerier) Query(_ context.Context, _ string) (metrics.Snapshot, error) {
	if q.err != nil {
		return metrics.Snapshot{}, q.err
	}
	if q.empty {
		return metrics.Snapshot{}, nil
	}
	return metrics.Snapshot{Samples: []metrics.Sample{{Value: q.value}}}, nil
}

func TestApplyKernelCorroborationBoostsOnRealCount(t *testing.T) {
	t.Parallel()
	r := &CascadePolicyReconciler{Metrics: &kernelQuerier{value: 12}}
	v := signatures.Verdict{Tripped: true, Confidence: 0.6, Evidence: testTrippedEvidence}

	got := r.applyKernelCorroboration(context.Background(), v, "inventory-service.default.svc.cluster.local", 30)

	want := signatures.ApplyKernelCorroboration(v, 12)
	if got != want {
		t.Errorf("applyKernelCorroboration = %+v, want %+v", got, want)
	}
}

func TestApplyKernelCorroborationUnchangedOnQueryError(t *testing.T) {
	t.Parallel()
	r := &CascadePolicyReconciler{Metrics: &kernelQuerier{err: errors.New("prometheus unreachable")}}
	v := signatures.Verdict{Tripped: true, Confidence: 0.6, Evidence: testTrippedEvidence}

	got := r.applyKernelCorroboration(context.Background(), v, "inventory-service.default.svc.cluster.local", 30)

	if got != v {
		t.Errorf("applyKernelCorroboration on query error = %+v, want unchanged %+v (never fail the caller's own detection)", got, v)
	}
}

func TestApplyKernelCorroborationUnchangedOnNoData(t *testing.T) {
	t.Parallel()
	r := &CascadePolicyReconciler{Metrics: &kernelQuerier{empty: true}}
	v := signatures.Verdict{Tripped: true, Confidence: 0.6, Evidence: testTrippedEvidence}

	got := r.applyKernelCorroboration(context.Background(), v, "inventory-service.default.svc.cluster.local", 30)

	if got != v {
		t.Errorf("applyKernelCorroboration with no scraped data = %+v, want unchanged %+v (Tetragon absent must be a no-op)", got, v)
	}
}

func TestApplyKernelCorroborationNilMetricsIsANoOp(t *testing.T) {
	t.Parallel()
	r := &CascadePolicyReconciler{}
	v := signatures.Verdict{Tripped: true, Confidence: 0.6, Evidence: testTrippedEvidence}

	got := r.applyKernelCorroboration(context.Background(), v, "inventory-service.default.svc.cluster.local", 30)

	if got != v {
		t.Errorf("applyKernelCorroboration with Metrics unset = %+v, want unchanged %+v", got, v)
	}
}
