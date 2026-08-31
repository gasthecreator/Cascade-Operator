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
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

func trippedFanOutManagedDR() *networkingv1.DestinationRule {
	dr := patchTestDR()
	mitigation.ApplyFanOutConnectionPoolTrip(dr)
	return dr
}

func seededFanOutPolicy(phase cascadev1alpha1.PolicyPhase, step int32) *cascadev1alpha1.CascadePolicy {
	return seededPolicyWithSignature(phase, step, cascadev1alpha1.SignatureFanOutAmplification)
}

func TestFanOutRestoreEntersAtStepZeroWhenTrippedGoesHealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(), seededFanOutPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedFanOutManagedDR())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, dr := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseRestoring {
		t.Errorf("phase = %s, want Restoring", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	// Unset original ramps toward the Envoy default of 1024:
	// lerp(1, 1024, 1/5) = round(1 + 1023*0.2) = 206.
	if http.GetHttp1MaxPendingRequests() != 206 {
		t.Errorf("step 0 http1MaxPendingRequests = %d, want 206 (first loosening toward Envoy default 1024)", http.GetHttp1MaxPendingRequests())
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by stripped too early")
	}
}

func TestFanOutRestoreAdvancesEachHealthyStepThenCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(), seededFanOutPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedFanOutManagedDR())

	wantPhase := []cascadev1alpha1.PolicyPhase{
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseNormal,
	}
	wantStep := []int32{0, 1, 2, 3, 4, 0}
	// Unset original: ticks 0-3 ramp toward the Envoy default 1024 (206,
	// 411, 615, 819); tick 4 is the final ramp step, which already writes
	// the true original per ApplyFanOutConnectionPoolRestoreStep's
	// step>=RestoreFinalStep contract — for an Unset original that means
	// clearing the block entirely (reads back as 0 via the getter), not a
	// fifth ramped value — same non-monotonic-at-the-last-step shape
	// ApplyLatencyErrorOutlierRestoreStep's Unset case has (see
	// TestRestoreAdvancesEachHealthyStepThenCompletes, restore_test.go).
	wantHTTP1 := []int32{206, 410, 615, 819, 0}

	for i := range wantPhase {
		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		p, dr := getPolicyAndDR(t, c)
		if p.Status.Phase != wantPhase[i] {
			t.Errorf("tick %d phase = %s, want %s", i, p.Status.Phase, wantPhase[i])
		}
		if p.Status.RestoreStep != wantStep[i] {
			t.Errorf("tick %d restoreStep = %d, want %d", i, p.Status.RestoreStep, wantStep[i])
		}
		if wantPhase[i] == cascadev1alpha1.PolicyPhaseNormal {
			if dr.Annotations[mitigation.AnnotationManagedBy] != "" || dr.Annotations[mitigation.AnnotationOriginalConnectionPool] != "" {
				t.Errorf("tick %d annotations remain: %v", i, dr.Annotations)
			}
			if dr.Spec.GetTrafficPolicy().GetConnectionPool() != nil {
				t.Error("unset original should clear connectionPool on complete")
			}
			continue
		}
		got := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp().GetHttp1MaxPendingRequests()
		if got != wantHTTP1[i] {
			t.Errorf("tick %d http1MaxPendingRequests = %d, want %d", i, got, wantHTTP1[i])
		}
		if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d stripped managed-by while still Restoring", i)
		}
	}
}

func TestFanOutRestoreRegressionReTripsAndBumpsLastTrippedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := seededFanOutPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	oldTrip := policy.Status.LastTrippedAt.DeepCopy()
	dr := trippedFanOutManagedDR()
	if err := mitigation.ApplyFanOutConnectionPoolRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}

	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), policy, dr)
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if p.Status.LastTrippedAt == nil || !p.Status.LastTrippedAt.After(oldTrip.Time) {
		t.Errorf("LastTrippedAt not bumped: old=%v new=%v", oldTrip, p.Status.LastTrippedAt)
	}
	http := got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want trip value", http.GetHttp1MaxPendingRequests())
	}
	if got.Annotations[mitigation.AnnotationOriginalConnectionPool] != mitigation.OriginalConnectionPoolUnsetJSON {
		t.Error("original annotation overwritten on regression")
	}
}

func TestFanOutRestoreCompleteWithStoredOriginalValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const original = `{"http1MaxPendingRequests":64,"http2MaxRequests":128}`
	dr := trippedFanOutManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalConnectionPool] = original

	policy := seededFanOutPolicy(cascadev1alpha1.PolicyPhaseRestoring, mitigation.RestoreFinalStep)
	r, c := patchReconcileWith(t, healthyQuerier(), policy, dr)
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Errorf("phase = %s, want Normal", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != "" || got.Annotations[mitigation.AnnotationOriginalConnectionPool] != "" {
		t.Errorf("annotations remain: %v", got.Annotations)
	}
	http := got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http == nil {
		t.Fatal("connectionPool.http cleared; original was not unset")
	}
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want 64", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want 128", http.GetHttp2MaxRequests())
	}
}

func TestFanOutQueryErrorWhileTrippedDoesNotRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, &fakeQuerier{err: fmt.Errorf("prometheus unavailable")},
		seededFanOutPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedFanOutManagedDR())
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, dr := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped (no healthy evaluation)", p.Status.Phase)
	}
	if dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp().GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Error("query error loosened the trip patch")
	}
}

// TestRestoreDispatchTouchesOnlyItsOwnFieldsAcrossAllThreeSignatures is the
// three-way version of TestRestoreDispatchTouchesOnlyDestinationRuleForLatencyError
// / TestRestoreDispatchTouchesOnlyVirtualServiceForRetryStorm
// (retry_restore_test.go), extended for the shared-object-kind subtlety this
// slice introduced: latency/error-cascade and fan-out both restore a
// DestinationRule, on disjoint field sets (outlierDetection vs.
// connectionPool.http), while retry storm restores a separate
// VirtualService. It seeds a *single* DestinationRule already carrying both
// signatures' trip-time fields and annotations (exactly the scenario
// fanout_restore.go's doc comment reasons through) plus a managed
// VirtualService, then trips each signature in turn and confirms each
// restore path only ever advances its own field set / annotation, leaving
// the other signature's fields, annotation, and the VirtualService
// completely untouched.
func TestRestoreDispatchTouchesOnlyItsOwnFieldsAcrossAllThreeSignatures(t *testing.T) {
	t.Parallel()

	// One DestinationRule, trip-time-patched by both signatures (fan-out
	// second, matching ApplyFanOutConnectionPoolTrip's own
	// "capture-my-baseline-even-if-managed-by-is-already-set" contract).
	dualManagedDR := func() *networkingv1.DestinationRule {
		dr := patchTestDR()
		mitigation.ApplyLatencyErrorOutlierTrip(dr)
		mitigation.ApplyFanOutConnectionPoolTrip(dr)
		return dr
	}

	t.Run("LatencyErrorCascade advances only outlierDetection and its own VirtualService field", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		// dualManagedVS, not trippedManagedVS: latency/error-cascade now
		// also manages the VirtualService's timeout (PLAN.md §2.6's
		// secondary), so this dispatch legitimately touches it — the
		// fixture must carry latency/error's own annotation too, same
		// reasoning as dualManagedDR just below for the DestinationRule
		// side (retry_restore_test.go's dualManagedVS doc comment has the
		// full why).
		r, c := patchReconcileWith(t, healthyQuerier(),
			seededPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), dualManagedDR(), dualManagedVS())

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		dr := &networkingv1.DestinationRule{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
			t.Fatal(err)
		}
		od := dr.Spec.GetTrafficPolicy().GetOutlierDetection()
		if od.GetInterval().AsDuration() != 6*time.Second {
			t.Errorf("outlierDetection not advanced by dispatch: interval = %s, want 6s", od.GetInterval().AsDuration())
		}
		http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
		if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
			t.Errorf("connectionPool.http touched by LatencyErrorCascade dispatch: http1MaxPendingRequests = %d, want unchanged trip value %d",
				http.GetHttp1MaxPendingRequests(), mitigation.TripHTTP1MaxPendingRequests)
		}
		if dr.Annotations[mitigation.AnnotationOriginalConnectionPool] != mitigation.OriginalConnectionPoolUnsetJSON {
			t.Error("fan-out's own original-connection-pool annotation disturbed by LatencyErrorCascade dispatch")
		}

		vs := &networkingv1.VirtualService{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
			t.Fatal(err)
		}
		if vs.Spec.Http[1].Retries.GetAttempts() != mitigation.TripRetryAttempts {
			t.Error("retry storm's own retries field touched by LatencyErrorCascade dispatch")
		}
		if vs.Annotations[mitigation.AnnotationOriginalRetries] == "" {
			t.Error("retry storm's own original-retries annotation disturbed by LatencyErrorCascade dispatch")
		}
		wantTimeout := 500*time.Millisecond + (2*time.Second-500*time.Millisecond)/5
		if vs.Spec.Http[1].Timeout.AsDuration() != wantTimeout {
			t.Errorf("latency/error's own timeout not advanced by dispatch: route[1] timeout = %s, want %s",
				vs.Spec.Http[1].Timeout.AsDuration(), wantTimeout)
		}
	})

	t.Run("FanOutAmplification advances only connectionPool.http", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		r, c := patchReconcileWith(t, healthyQuerier(),
			seededFanOutPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), dualManagedDR(), trippedManagedVS())

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		dr := &networkingv1.DestinationRule{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
			t.Fatal(err)
		}
		http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
		if http.GetHttp1MaxPendingRequests() != 206 {
			t.Errorf("connectionPool.http not advanced by dispatch: http1MaxPendingRequests = %d, want 206", http.GetHttp1MaxPendingRequests())
		}
		od := dr.Spec.GetTrafficPolicy().GetOutlierDetection()
		if od.GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
			t.Error("outlierDetection touched by FanOutAmplification dispatch")
		}
		if od.GetInterval().AsDuration() != mitigation.TripInterval {
			t.Error("outlierDetection touched by FanOutAmplification dispatch")
		}
		if dr.Annotations[mitigation.AnnotationOriginalOutlier] != mitigation.OriginalOutlierUnsetJSON {
			t.Error("latency/error's own original-outlier annotation disturbed by FanOutAmplification dispatch")
		}

		vs := &networkingv1.VirtualService{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
			t.Fatal(err)
		}
		if vs.Spec.Http[1].Retries.GetAttempts() != mitigation.TripRetryAttempts {
			t.Error("VirtualService touched by FanOutAmplification dispatch")
		}
	})

	t.Run("RetryStorm advances only the VirtualService", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		r, c := patchReconcileWith(t, healthyQuerier(),
			seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), dualManagedDR(), trippedManagedVS())

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		vs := &networkingv1.VirtualService{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
			t.Fatal(err)
		}
		if vs.Spec.Http[1].Retries.GetAttempts() != 1 {
			t.Errorf("VirtualService not advanced by dispatch: route[1] attempts = %d, want 1", vs.Spec.Http[1].Retries.GetAttempts())
		}

		dr := &networkingv1.DestinationRule{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
			t.Fatal(err)
		}
		od := dr.Spec.GetTrafficPolicy().GetOutlierDetection()
		if od.GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
			t.Error("outlierDetection touched by RetryStorm dispatch")
		}
		http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
		if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
			t.Error("connectionPool.http touched by RetryStorm dispatch")
		}
	})
}
