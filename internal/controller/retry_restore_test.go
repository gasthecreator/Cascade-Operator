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

	"google.golang.org/protobuf/types/known/durationpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

const testRetryOn5xx = "5xx"

// multiRouteTestVS exercises all three per-route original states the
// AnnotationOriginalRetries contract distinguishes: route[0] has no
// explicit retries pre-trip (Unset), route[1] has an explicit policy that
// must come back exactly, and route[2] is a redirect with no destination
// (Skipped — never touched on trip or restore).
func multiRouteTestVS() *networkingv1.VirtualService {
	return &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: patchDepName, Namespace: patchPolicyNS},
		Spec: apinet.VirtualService{
			Hosts: []string{patchDepHost},
			Http: []*apinet.HTTPRoute{
				{Route: []*apinet.HTTPRouteDestination{{Destination: &apinet.Destination{Host: patchDepHost}}}},
				{
					Route:   []*apinet.HTTPRouteDestination{{Destination: &apinet.Destination{Host: patchDepHost}}},
					Retries: &apinet.HTTPRetry{Attempts: 5, RetryOn: testRetryOn5xx},
				},
				{Redirect: &apinet.HTTPRedirect{Uri: "/elsewhere"}},
			},
		},
	}
}

func trippedManagedVS() *networkingv1.VirtualService {
	vs := multiRouteTestVS()
	mitigation.ApplyRetryStormTrip(vs)
	return vs
}

// dualManagedVS is trippedManagedVS's two-signature twin — the VirtualService
// analog of fanout_restore_test.go's dualManagedDR. VirtualService became a
// shared object kind in the same slice that added latency/error-cascade's
// timeout secondary (retry storm already owned retries.attempts on it): a
// real handoff always force-completes the outgoing signature before the
// incoming one's own trip touches the object (PLAN.md §2.6), so "managed-by
// true but only *one* signature's own annotation present" is the only
// reachable state in production — but the dispatch-isolation tests below
// seed Restoring directly, bypassing that handoff sequence, so a fixture
// that skips latency/error-cascade's own trip on the VS would make
// ApplyLatencyErrorTimeoutRestoreStep error on a missing annotation instead
// of testing what it's meant to test: that dispatch only ever advances its
// own field/annotation, leaving the other signature's alone, exactly as
// dualManagedDR already established for the DestinationRule side.
func dualManagedVS() *networkingv1.VirtualService {
	vs := multiRouteTestVS()
	mitigation.ApplyRetryStormTrip(vs)
	// Give route[1] an explicit pre-trip timeout before latency/error's own
	// trip captures its baseline, so the restore-ramp test below exercises
	// a real captured original rather than the Unset -> 15s-default path.
	vs.Spec.Http[1].Timeout = durationpb.New(2 * time.Second)
	mitigation.ApplyLatencyErrorTimeoutTrip(vs, 500)
	return vs
}

// dualManagedDR is trippedManagedDR's (restore_test.go) two-signature
// twin for the DestinationRule side — dualManagedVS's counterpart, now
// that retry storm also manages a DestinationRule (its own
// connectionPool.http MaxRetries secondary, PLAN.md §2.6) alongside
// latency/error-cascade's outlierDetection primary. The two field sets
// don't share a sub-message at all, so applying both trips back-to-back
// here still captures each signature's true pre-trip baseline.
func dualManagedDR() *networkingv1.DestinationRule {
	dr := patchTestDR()
	mitigation.ApplyLatencyErrorOutlierTrip(dr)
	mitigation.ApplyRetryStormConnectionPoolTrip(dr)
	return dr
}

func seededPolicyWithSignature(phase cascadev1alpha1.PolicyPhase, step int32, sig cascadev1alpha1.SignatureType) *cascadev1alpha1.CascadePolicy {
	p := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	p.Status.Phase = phase
	p.Status.RestoreStep = step
	p.Status.LastSignature = sig
	trippedAt := metav1.NewTime(time.Now().Add(-time.Hour))
	p.Status.LastTrippedAt = &trippedAt
	return p
}

func seededRetryStormPolicy(phase cascadev1alpha1.PolicyPhase, step int32) *cascadev1alpha1.CascadePolicy {
	return seededPolicyWithSignature(phase, step, cascadev1alpha1.SignatureRetryStorm)
}

func getPolicyAndVS(t *testing.T, c client.Client) (*cascadev1alpha1.CascadePolicy, *networkingv1.VirtualService) {
	t.Helper()
	ctx := context.Background()
	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, p); err != nil {
		t.Fatal(err)
	}
	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	return p, vs
}

func TestRetryStormRestoreEntersAtStepZeroWhenTrippedGoesHealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(), seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedVS())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, vs := getPolicyAndVS(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseRestoring {
		t.Errorf("phase = %s, want Restoring", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	routes := vs.Spec.Http
	if routes[0].Retries.GetAttempts() != 0 {
		t.Errorf("step 0 route[0] (unset) attempts = %d, want 0 (ramping toward implicit default 2)", routes[0].Retries.GetAttempts())
	}
	if routes[1].Retries.GetAttempts() != 1 {
		t.Errorf("step 0 route[1] attempts = %d, want 1 (first loosening toward original 5)", routes[1].Retries.GetAttempts())
	}
	if routes[2].Retries != nil {
		t.Error("skipped route should never get a retries block")
	}
	if vs.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by stripped too early")
	}
}

func TestRetryStormRestoreAdvancesEachStepThenCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(), seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedVS())

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
	// route[0] is Unset, ramping toward the implicit default of 2 until the
	// final step clears the block entirely instead of writing attempts=2.
	wantRoute0Attempts := []int32{0, 1, 1, 2}

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
		routes := vs.Spec.Http
		if routes[1].Retries.GetAttempts() != wantRoute1Attempts[i] {
			t.Errorf("tick %d route[1] attempts = %d, want %d", i, routes[1].Retries.GetAttempts(), wantRoute1Attempts[i])
		}
		// retryOn stays cleared through every mid-ramp tick — trip already
		// cleared it (Istio's webhook rejects attempts:0 alongside a set
		// retryOn, so trip must clear it; see mitigation.ApplyRetryStormTrip)
		// and steps 0-3 only ever touch attempts. It comes back, alongside
		// the rest of the original block, at step RestoreFinalStep (tick 4,
		// still Phase Restoring — the phase transition to Normal happens on
		// the following tick) and stays back once Normal (tick 5).
		wantRetryOn := ""
		if wantStep[i] >= mitigation.RestoreFinalStep || wantPhase[i] == cascadev1alpha1.PolicyPhaseNormal {
			wantRetryOn = testRetryOn5xx
		}
		if routes[1].Retries.GetRetryOn() != wantRetryOn {
			t.Errorf("tick %d route[1] retryOn = %q, want %q", i, routes[1].Retries.GetRetryOn(), wantRetryOn)
		}
		if i >= 4 {
			if routes[0].Retries != nil {
				t.Errorf("tick %d route[0] (unset) should be cleared, got %+v", i, routes[0].Retries)
			}
		} else if routes[0].Retries.GetAttempts() != wantRoute0Attempts[i] {
			t.Errorf("tick %d route[0] attempts = %d, want %d", i, routes[0].Retries.GetAttempts(), wantRoute0Attempts[i])
		}
		if routes[2].Retries != nil {
			t.Errorf("tick %d skipped route got a retries block", i)
		}
		if wantPhase[i] == cascadev1alpha1.PolicyPhaseNormal {
			if vs.Annotations[mitigation.AnnotationManagedBy] != "" || vs.Annotations[mitigation.AnnotationOriginalRetries] != "" {
				t.Errorf("tick %d annotations remain: %v", i, vs.Annotations)
			}
			continue
		}
		if vs.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d stripped managed-by while still Restoring", i)
		}
	}
}

func TestRetryStormRestoreRegressionReTripsAndBumpsLastTrippedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	oldTrip := policy.Status.LastTrippedAt.DeepCopy()
	vs := trippedManagedVS()
	if err := mitigation.ApplyRetryStormRestoreStep(vs, 2); err != nil {
		t.Fatal(err)
	}
	wantOriginal := vs.Annotations[mitigation.AnnotationOriginalRetries]

	r, c := patchReconcileWith(t, retryStormOnlyQuerier(), policy, vs)
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndVS(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if p.Status.LastTrippedAt == nil || !p.Status.LastTrippedAt.After(oldTrip.Time) {
		t.Errorf("LastTrippedAt not bumped: old=%v new=%v", oldTrip, p.Status.LastTrippedAt)
	}
	routes := got.Spec.Http
	if routes[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Errorf("route[0] attempts = %d, want trip value", routes[0].Retries.GetAttempts())
	}
	if routes[1].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Errorf("route[1] attempts = %d, want trip value", routes[1].Retries.GetAttempts())
	}
	if got.Annotations[mitigation.AnnotationOriginalRetries] != wantOriginal {
		t.Error("original annotation overwritten on regression")
	}
}

func TestRetryStormRestoreCompleteWithStoredOriginalValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseRestoring, mitigation.RestoreFinalStep)
	r, c := patchReconcileWith(t, healthyQuerier(), policy, trippedManagedVS())
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndVS(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Errorf("phase = %s, want Normal", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != "" || got.Annotations[mitigation.AnnotationOriginalRetries] != "" {
		t.Errorf("annotations remain: %v", got.Annotations)
	}
	routes := got.Spec.Http
	if routes[0].Retries != nil {
		t.Errorf("route[0] (unset original) should be cleared, got %+v", routes[0].Retries)
	}
	if routes[1].Retries.GetAttempts() != 5 || routes[1].Retries.GetRetryOn() != testRetryOn5xx {
		t.Errorf("route[1] = %+v, want attempts=5 retryOn=5xx", routes[1].Retries)
	}
	if routes[2].Retries != nil {
		t.Error("skipped route should never get a retries block")
	}
}

func TestRetryStormQueryErrorWhileTrippedDoesNotRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, &fakeQuerier{err: fmt.Errorf("prometheus unavailable")},
		seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedVS())
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, vs := getPolicyAndVS(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped (no healthy evaluation)", p.Status.Phase)
	}
	if vs.Spec.Http[1].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Error("query error loosened the trip patch")
	}
}

// TestRestoreDispatchAdvancesLatencyErrorsOwnFieldsOnBothObjectKinds and
// TestRestoreDispatchTouchesOnlyVirtualServiceForRetryStorm both seed a
// managed DestinationRule *and* a managed VirtualService, then trip a
// specific signature — the point is to catch beginRestore/advanceRestore's
// switch routing to the wrong object (kind or field), not just that each
// path works checked in isolation (which the tests above and
// restore_test.go already cover individually). Latency/error-cascade now
// legitimately manages *both* object kinds (DestinationRule primary,
// VirtualService secondary — PLAN.md §2.6), so unlike before this slice,
// "touches only the DestinationRule" is no longer the right thing to
// assert; the VirtualService fixture is dual-managed (dualManagedVS) so
// this test can assert the sharper claim: latency/error-cascade's dispatch
// advances its own timeout field on the VirtualService while leaving retry
// storm's own retries field (and annotation) on that same object alone.
func TestRestoreDispatchAdvancesLatencyErrorsOwnFieldsOnBothObjectKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(),
		seededPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedDR(), dualManagedVS())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	od := dr.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetInterval().AsDuration() != 6*time.Second {
		t.Errorf("DestinationRule not advanced by dispatch: interval = %s, want 6s", od.GetInterval().AsDuration())
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Spec.Http[1].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Errorf("retry storm's own field touched by LatencyErrorCascade dispatch: route[1] attempts = %d, want unchanged trip value %d",
			vs.Spec.Http[1].Retries.GetAttempts(), mitigation.TripRetryAttempts)
	}
	if vs.Annotations[mitigation.AnnotationOriginalRetries] == "" {
		t.Error("retry storm's own original-retries annotation disturbed by LatencyErrorCascade dispatch")
	}
	// latency/error-cascade's own secondary *should* advance: route[1]'s
	// timeout ramps from the 500ms trip value toward its true 2s original.
	wantTimeout := 500*time.Millisecond + (2*time.Second-500*time.Millisecond)/5
	if vs.Spec.Http[1].Timeout.AsDuration() != wantTimeout {
		t.Errorf("latency/error's own timeout not advanced by dispatch: route[1] timeout = %s, want %s",
			vs.Spec.Http[1].Timeout.AsDuration(), wantTimeout)
	}
	if vs.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by disturbed by LatencyErrorCascade dispatch")
	}
}

// TestRestoreDispatchAdvancesRetryStormsOwnFieldsOnBothObjectKinds is the
// retry-storm twin of TestRestoreDispatchAdvancesLatencyErrorsOwnFieldsOnBothObjectKinds
// above: retry storm now also manages a DestinationRule (its own
// connectionPool.http secondary, PLAN.md §2.6) alongside its VirtualService
// primary, so "touches only the VirtualService" is no longer the right
// thing to assert (see the previous version of this test's own name,
// retired in the same slice that added the secondary). The DestinationRule
// fixture is dual-managed (dualManagedDR) so this test can assert the
// sharper claim: retry storm's dispatch advances its own MaxRetries on the
// DestinationRule while leaving latency/error-cascade's own outlierDetection
// (and annotation) on that same object alone.
func TestRestoreDispatchAdvancesRetryStormsOwnFieldsOnBothObjectKinds(t *testing.T) {
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
		t.Error("latency/error's own outlierDetection touched by RetryStorm dispatch")
	}
	if od.GetInterval().AsDuration() != mitigation.TripInterval {
		t.Error("latency/error's own outlierDetection touched by RetryStorm dispatch")
	}
	if dr.Annotations[mitigation.AnnotationOriginalOutlier] != mitigation.OriginalOutlierUnsetJSON {
		t.Error("latency/error's own original-outlier annotation disturbed by RetryStorm dispatch")
	}
	// retry storm's own secondary *should* advance: MaxRetries ramps from
	// the trip value toward whatever dualManagedDR's construction captured
	// as "original" (see its own doc comment) — this test only needs to
	// see it move off the trip value, not match an exact target.
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetMaxRetries() == mitigation.TripRetryStormMaxRetries {
		t.Error("retry storm's own connectionPool.http maxRetries was not advanced by dispatch")
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by disturbed by RetryStorm dispatch")
	}
}

// TestRestoreFallsBackToNormalForUnrecognizedSignature is defensive
// coverage, not a currently-reachable production path now that all three
// SignatureType enum values (LatencyErrorCascade, RetryStorm,
// FanOutAmplification) have a wired restore path — this slice's own
// FanOutAmplification wiring is what retired the previous version of this
// test, which used FanOutAmplification as its stand-in "unwired" value.
// It exercises beginRestore/advanceRestore's default case directly with a
// value outside the CRD's own enum, so the fail-safe (snap to Normal rather
// than get stuck at Restoring) still holds for whatever signature is wired
// next, or for any status corruption that produces a value the switch
// does not recognize.
func TestRestoreFallsBackToNormalForUnrecognizedSignature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const bogusSignature = cascadev1alpha1.SignatureType("Bogus")
	policy := seededPolicyWithSignature(cascadev1alpha1.PolicyPhaseTripped, 0, bogusSignature)
	r, c := patchReconcileWith(t, healthyQuerier(), policy, trippedManagedDR())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Errorf("phase = %s, want Normal (fail-safe fallback)", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
}
