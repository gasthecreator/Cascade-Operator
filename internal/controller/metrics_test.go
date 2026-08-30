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

// Deliberately no t.Parallel() anywhere in this file. metrics.go's counters
// are package-level vars registered once on controller-runtime's global
// metrics.Registry — exactly the production shape the task asked for, and
// the correct one for a single running operator process, but every other
// test in this package that calls Reconcile/apply*Mitigation/complete*Restore
// increments those same global counters as a side effect, and almost all of
// them run under t.Parallel(). Reading an absolute or even a naively-diffed
// value from inside a t.Parallel() test racing against dozens of others that
// touch the same label combinations (they mostly all share patchDepHost)
// would be flaky by construction, not by mistake. Go's testing package runs
// every top-level test function that never calls t.Parallel() to full
// completion, in discovery order, before releasing any paused-at-Parallel
// test to actually run its body — so a non-parallel test's own before/after
// delta on these globals is race-free as long as it doesn't call
// t.Parallel(), regardless of what the rest of the package's ~30 parallel
// tests do once they're released afterward. That is the actual contract
// being relied on here, not an assumption.

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

func TestMetricsSignatureDetectedIncrementsOnTrip(t *testing.T) {
	ctx := context.Background()
	// No DestinationRule seeded: detection must still count the trip even
	// though the mitigation patch itself is skipped (DependencyObjectMissing).
	r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate))
	before := testutil.ToFloat64(signaturesDetectedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), patchDepHost))

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	after := testutil.ToFloat64(signaturesDetectedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), patchDepHost))
	if after-before != 1 {
		t.Errorf("cascade_signatures_detected_total{LatencyErrorCascade,%s} delta = %v, want 1", patchDepHost, after-before)
	}
}

func TestMetricsSignatureDetectedDoesNotIncrementWhenHealthy(t *testing.T) {
	ctx := context.Background()
	r, _ := patchReconcileWith(t, healthyQuerier(), patchTestPolicy(cascadev1alpha1.PolicyModeMitigate))
	before := testutil.ToFloat64(signaturesDetectedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), patchDepHost))

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	after := testutil.ToFloat64(signaturesDetectedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), patchDepHost))
	if after != before {
		t.Errorf("cascade_signatures_detected_total{LatencyErrorCascade,%s} changed on a healthy tick: %v -> %v", patchDepHost, before, after)
	}
}

func TestMetricsMitigationPatchAppliedOnEachSignature(t *testing.T) {
	t.Run("LatencyErrorCascade patches DestinationRule outlierDetection", func(t *testing.T) {
		ctx := context.Background()
		r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
		before := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule))
		if after-before != 1 {
			t.Errorf("cascade_mitigation_patches_applied_total{LatencyErrorCascade,DestinationRule} delta = %v, want 1", after-before)
		}
	})

	t.Run("RetryStorm patches VirtualService retries", func(t *testing.T) {
		ctx := context.Background()
		r, _ := patchReconcileWith(t, retryStormOnlyQuerier(), patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestVS())
		before := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm), kindVirtualService))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm), kindVirtualService))
		if after-before != 1 {
			t.Errorf("cascade_mitigation_patches_applied_total{RetryStorm,VirtualService} delta = %v, want 1", after-before)
		}
	})

	t.Run("FanOutAmplification patches DestinationRule connectionPool", func(t *testing.T) {
		ctx := context.Background()
		r, _ := patchReconcileWith(t, fanOutOnlyQuerier(), patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
		before := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification), kindDestinationRule))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification), kindDestinationRule))
		if after-before != 1 {
			t.Errorf("cascade_mitigation_patches_applied_total{FanOutAmplification,DestinationRule} delta = %v, want 1", after-before)
		}
	})
}

func TestMetricsMitigationPatchDoesNotIncrementInDetectOnlyOrWhenObjectMissing(t *testing.T) {
	t.Run("DetectOnly mode never patches", func(t *testing.T) {
		ctx := context.Background()
		r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeDetectOnly), patchTestDR())
		before := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule))
		if after != before {
			t.Errorf("cascade_mitigation_patches_applied_total changed in DetectOnly mode: %v -> %v", before, after)
		}
	})

	t.Run("missing DestinationRule never patches", func(t *testing.T) {
		ctx := context.Background()
		r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate))
		before := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule))
		if after != before {
			t.Errorf("cascade_mitigation_patches_applied_total changed with no DestinationRule to patch: %v -> %v", before, after)
		}
	})
}

func TestMetricsRestorationCompletedOnEachSignatureRampFinalStep(t *testing.T) {
	t.Run("LatencyErrorCascade", func(t *testing.T) {
		ctx := context.Background()
		policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, mitigation.RestoreFinalStep)
		r, _ := patchReconcileWith(t, healthyQuerier(), policy, trippedManagedDR())
		before := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))
		if after-before != 1 {
			t.Errorf("cascade_restorations_completed_total{LatencyErrorCascade} delta = %v, want 1", after-before)
		}
	})

	t.Run("RetryStorm", func(t *testing.T) {
		ctx := context.Background()
		policy := seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseRestoring, mitigation.RestoreFinalStep)
		r, _ := patchReconcileWith(t, healthyQuerier(), policy, trippedManagedVS())
		before := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm)))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm)))
		if after-before != 1 {
			t.Errorf("cascade_restorations_completed_total{RetryStorm} delta = %v, want 1", after-before)
		}
	})

	t.Run("FanOutAmplification", func(t *testing.T) {
		ctx := context.Background()
		policy := seededFanOutPolicy(cascadev1alpha1.PolicyPhaseRestoring, mitigation.RestoreFinalStep)
		r, _ := patchReconcileWith(t, healthyQuerier(), policy, trippedFanOutManagedDR())
		before := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification)))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification)))
		if after-before != 1 {
			t.Errorf("cascade_restorations_completed_total{FanOutAmplification} delta = %v, want 1", after-before)
		}
	})

	// A same-object signature handoff's force-complete (forceCompleteOutgoingRestore)
	// funnels through the exact same complete*Restore functions as the
	// gradual ramp's own final step, so it must count as a restoration
	// completion too — same call site, same instrumentation, no separate
	// increment needed at the handoff call site itself.
	t.Run("signature handoff force-complete also counts as a completion", func(t *testing.T) {
		ctx := context.Background()
		dr := trippedManagedDR()
		if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(dr, 2); err != nil {
			t.Fatal(err)
		}
		policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
		r, _ := patchReconcileWith(t, fanOutOnlyQuerier(), policy, dr)
		before := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))

		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatal(err)
		}

		after := testutil.ToFloat64(restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))
		if after-before != 1 {
			t.Errorf("cascade_restorations_completed_total{LatencyErrorCascade} delta on handoff = %v, want 1", after-before)
		}
	})
}

// TestMetricsRestorationRegressionOnSameSignatureReTrip mirrors
// handoff_restore_test.go's TestSignatureReTripSameSignatureDoesNotForceComplete
// setup exactly (same signature re-tripping mid-Restoring) but asserts the
// metric this slice adds rather than the annotation-preservation behavior
// that test already covers.
func TestMetricsRestorationRegressionOnSameSignatureReTrip(t *testing.T) {
	ctx := context.Background()
	dr := trippedManagedDR()
	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, _ := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, policy, dr)
	before := testutil.ToFloat64(restorationRegressionsTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	after := testutil.ToFloat64(restorationRegressionsTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))
	if after-before != 1 {
		t.Errorf("cascade_restoration_regressions_total{LatencyErrorCascade} delta = %v, want 1", after-before)
	}
}

// TestMetricsRestorationRegressionDoesNotFireOnHandoff confirms the
// regression counter and the handoff path are distinct: a *different*
// signature tripping mid-Restoring is a handoff (asserted elsewhere,
// including that it counts as a completion above), never a regression of
// the outgoing signature's own ramp.
func TestMetricsRestorationRegressionDoesNotFireOnHandoff(t *testing.T) {
	ctx := context.Background()
	dr := trippedManagedDR()
	if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}
	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, _ := patchReconcileWith(t, fanOutOnlyQuerier(), policy, dr)
	beforeLat := testutil.ToFloat64(restorationRegressionsTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))
	beforeFan := testutil.ToFloat64(restorationRegressionsTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification)))

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	afterLat := testutil.ToFloat64(restorationRegressionsTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)))
	afterFan := testutil.ToFloat64(restorationRegressionsTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification)))
	if afterLat != beforeLat || afterFan != beforeFan {
		t.Errorf("regression counter changed on a handoff: LatencyErrorCascade %v->%v, FanOutAmplification %v->%v",
			beforeLat, afterLat, beforeFan, afterFan)
	}
}
