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
	"testing"

	"k8s.io/apimachinery/pkg/types"

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// trippedRetryStormConnPoolDR is a DestinationRule already at retry storm's
// trip-time connectionPool.http values, with a real (non-zero) pre-trip
// snapshot so the ramp is a smooth monotonic curve across all 5 steps
// rather than the zero-original case's ramp-toward-Envoy-default-then-
// snap-to-zero-at-the-final-step shape (same distinction
// ApplyRetryStormConnectionPoolRestoreStep's own tests already cover in
// the mitigation package).
func trippedRetryStormConnPoolDR() *networkingv1.DestinationRule {
	dr := patchTestDR()
	dr.Spec.TrafficPolicy = &apinet.TrafficPolicy{
		ConnectionPool: &apinet.ConnectionPoolSettings{
			Http: &apinet.ConnectionPoolSettings_HTTPSettings{
				MaxRetries:              10,
				Http1MaxPendingRequests: 64,
			},
		},
	}
	mitigation.ApplyRetryStormConnectionPoolTrip(dr)
	return dr
}

// TestRetryStormRestoreAdvancesBothObjectKindsTogetherThenCompletes is the
// full end-to-end ramp for retry storm's two-object-kind shape — the
// VirtualService-only version is TestRetryStormRestoreAdvancesEachStepThenCompletes
// (retry_restore_test.go); the latency/error-cascade twin is
// TestLatencyErrorRestoreAdvancesBothObjectKindsTogetherThenCompletes.
// Both the VirtualService primary and the DestinationRule secondary must
// advance in lockstep, tick for tick, and both land on their true original
// (with both signatures' annotations stripped) at the same completion tick.
func TestRetryStormRestoreAdvancesBothObjectKindsTogetherThenCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(),
		seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseTripped, 0),
		trippedManagedVS(), trippedRetryStormConnPoolDR())

	wantPhase := []cascadev1alpha1.PolicyPhase{
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseNormal,
	}
	wantStep := []int32{0, 1, 2, 3, 4, 0}
	// route[1]'s original attempts=5: lerp(0, 5, t) at t=(step+1)/5.
	wantRoute1Attempts := []int32{1, 2, 3, 4, 5, 5}
	// maxRetries original 10: lerp(TripRetryStormMaxRetries=1, 10, t).
	wantMaxRetries := []int32{3, 5, 6, 8, 10, 10}

	for i := range wantPhase {
		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		p, vs := getPolicyAndVS(t, c)
		if p.Status.Phase != wantPhase[i] {
			t.Errorf("tick %d phase = %s, want %s", i, p.Status.Phase, wantPhase[i])
		}
		if p.Status.RestoreStep != wantStep[i] {
			t.Errorf("tick %d restoreStep = %d, want %d", i, p.Status.RestoreStep, wantStep[i])
		}
		if vs.Spec.Http[1].Retries.GetAttempts() != wantRoute1Attempts[i] {
			t.Errorf("tick %d route[1] attempts = %d, want %d", i, vs.Spec.Http[1].Retries.GetAttempts(), wantRoute1Attempts[i])
		}

		dr := &networkingv1.DestinationRule{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
			t.Fatal(err)
		}
		http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
		if http.GetMaxRetries() != wantMaxRetries[i] {
			t.Errorf("tick %d maxRetries = %d, want %d", i, http.GetMaxRetries(), wantMaxRetries[i])
		}
		if http.GetHttp1MaxPendingRequests() != 64 {
			t.Errorf("tick %d http1MaxPendingRequests = %d, want 64 (never this signature's field, must not move)", i, http.GetHttp1MaxPendingRequests())
		}

		if wantPhase[i] == cascadev1alpha1.PolicyPhaseNormal {
			if vs.Annotations[mitigation.AnnotationManagedBy] != "" || vs.Annotations[mitigation.AnnotationOriginalRetries] != "" {
				t.Errorf("tick %d VirtualService annotations remain: %v", i, vs.Annotations)
			}
			if dr.Annotations[mitigation.AnnotationManagedBy] != "" || dr.Annotations[mitigation.AnnotationOriginalRetryConnectionPool] != "" {
				t.Errorf("tick %d DestinationRule annotations remain: %v", i, dr.Annotations)
			}
			continue
		}
		if vs.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d stripped VirtualService managed-by while still Restoring", i)
		}
		if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d stripped DestinationRule managed-by while still Restoring", i)
		}
	}
}
