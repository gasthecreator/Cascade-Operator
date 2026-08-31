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

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// TestSignatureHandoffFanOutToRetryStormRestoresSharedFieldBeforeNewTrip
// records what the current implementation (matrix-as-written: both
// signatures claim Http1MaxPendingRequests) actually does on a handoff —
// evidence for the pending PROPOSALS.md entry, not a claim that the
// overlap is decided. Fan-out is mid-Restoring (step 2, partially-
// loosened connectionPool.http) when retry storm trips on the same host,
// same tick — no intervening healthy tick. If forceCompleteOutgoingRestore's
// ordering (force-complete the outgoing signature fully, *then* apply the
// incoming trip) is doing its job, the shared field lands on retry storm's
// own trip value, never on fan-out's still-mid-ramp interpolated value or
// a value derived from it.
func TestSignatureHandoffFanOutToRetryStormRestoresSharedFieldBeforeNewTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const originalPool = `{"http1MaxPendingRequests":64,"http2MaxRequests":128}`
	dr := trippedFanOutManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalConnectionPool] = originalPool
	if err := mitigation.ApplyFanOutConnectionPoolRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}
	vs := singleRouteVSFor()

	policy := seededFanOutPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(), policy, dr, vs)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, gotDR := getPolicyAndDR(t, c)

	if p.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Errorf("lastSignature = %s, want RetryStorm", p.Status.LastSignature)
	}
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}

	http := gotDR.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	// The shared field: retry storm's own trip value — not fan-out's true
	// original (64), and not fan-out's still-mid-ramp step-2 interpolated
	// value either. This is the exact correctness claim under test: the
	// handoff must fully land the outgoing signature (true original)
	// before the incoming signature's trip is allowed to overwrite the
	// same field.
	if http.GetHttp1MaxPendingRequests() != mitigation.TripRetryStormMaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want retry storm's trip value %d", http.GetHttp1MaxPendingRequests(), mitigation.TripRetryStormMaxPendingRequests)
	}
	// fan-out's own field, never touched by retry storm's trip, must land
	// on its TRUE original (128) — not the step-2 interpolated value —
	// proving force-complete ran to full completion before anything else
	// touched the object.
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want fan-out's true original 128 (not mid-ramp interpolated)", http.GetHttp2MaxRequests())
	}
	if http.GetMaxRetries() != mitigation.TripRetryStormMaxRetries {
		t.Errorf("maxRetries = %d, want retry storm's trip value %d", http.GetMaxRetries(), mitigation.TripRetryStormMaxRetries)
	}
	if _, present := gotDR.Annotations[mitigation.AnnotationOriginalConnectionPool]; present {
		t.Error("fan-out's original-connection-pool annotation left behind after handoff")
	}
	// Retry storm's own captured baseline must be the true original (64),
	// not fan-out's leftover trip/interpolated value — this is what
	// guarantees retry storm's *own* eventual restore is also correct, not
	// just its immediate trip-time write.
	const wantCaptured = `{"http1MaxPendingRequests":64}`
	if got := gotDR.Annotations[mitigation.AnnotationOriginalRetryConnectionPool]; got != wantCaptured {
		t.Errorf("original-retry-connection-pool = %s, want %s (true original, not fan-out's leftover value)", got, wantCaptured)
	}

	gotVS := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotVS); err != nil {
		t.Fatal(err)
	}
	if gotVS.Spec.Http[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Error("retry storm's own VirtualService primary was not applied fresh on this handoff")
	}
}

// TestSignatureHandoffRetryStormToFanOutForceCompletesBothObjectKindsAndSharedField
// is the reverse direction, and additionally confirms
// forceCompleteOutgoingRestore's newly-extended RetryStorm case correctly
// force-completes *both* object kinds this signature now manages (its
// VirtualService primary and its DestinationRule secondary) — not just the
// one fan-out also happens to share.
func TestSignatureHandoffRetryStormToFanOutForceCompletesBothObjectKindsAndSharedField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	vs := trippedManagedVS()
	if err := mitigation.ApplyRetryStormRestoreStep(vs, 2); err != nil {
		t.Fatal(err)
	}

	const originalRetryPool = `{"maxRetries":10,"http1MaxPendingRequests":64}`
	dr := patchTestDR()
	mitigation.ApplyRetryStormConnectionPoolTrip(dr)
	dr.Annotations[mitigation.AnnotationOriginalRetryConnectionPool] = originalRetryPool
	if err := mitigation.ApplyRetryStormConnectionPoolRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}

	policy := seededRetryStormPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), policy, dr, vs)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, gotDR := getPolicyAndDR(t, c)

	if p.Status.LastSignature != cascadev1alpha1.SignatureFanOutAmplification {
		t.Errorf("lastSignature = %s, want FanOutAmplification", p.Status.LastSignature)
	}
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}

	http := gotDR.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	// retry storm's own field, restored to its true original (10) by
	// force-complete and never touched by fan-out's trip — the strongest
	// proof force-complete ran fully: without it, this would still read
	// the step-2 interpolated value (round(1+9*0.6)=6), not 10.
	if http.GetMaxRetries() != 10 {
		t.Errorf("maxRetries = %d, want retry storm's true original 10 (not mid-ramp interpolated, not fan-out-disturbed)", http.GetMaxRetries())
	}
	// The shared field: fan-out's own trip value, applied fresh after
	// retry storm's own restore correctly landed the true original (64)
	// first — not retry storm's still-mid-ramp interpolated value
	// (round(1+63*0.6)=39).
	if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want fan-out's trip value %d", http.GetHttp1MaxPendingRequests(), mitigation.TripHTTP1MaxPendingRequests)
	}
	if http.GetHttp2MaxRequests() != mitigation.TripHTTP2MaxRequests {
		t.Errorf("http2MaxRequests = %d, want fan-out's trip value %d", http.GetHttp2MaxRequests(), mitigation.TripHTTP2MaxRequests)
	}
	if _, present := gotDR.Annotations[mitigation.AnnotationOriginalRetryConnectionPool]; present {
		t.Error("retry storm's original-retry-connection-pool annotation left behind after handoff")
	}
	// Fan-out's own captured baseline must be the true original (64), not
	// retry storm's leftover interpolated value.
	const wantCaptured = `{"http1MaxPendingRequests":64}`
	if got := gotDR.Annotations[mitigation.AnnotationOriginalConnectionPool]; got != wantCaptured {
		t.Errorf("original-connection-pool = %s, want %s (true original, not retry storm's leftover value)", got, wantCaptured)
	}

	// Retry storm's own VirtualService primary must also be fully
	// restored and unmanaged — fan-out never touches VirtualService at
	// all, so nothing else would ever clean this up if force-complete
	// left it behind.
	gotVS := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotVS); err != nil {
		t.Fatal(err)
	}
	if gotVS.Spec.Http[1].Retries.GetAttempts() != 5 {
		t.Errorf("VirtualService retries.attempts = %d, want retry storm's true original 5", gotVS.Spec.Http[1].Retries.GetAttempts())
	}
	if gotVS.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Error("VirtualService managed-by left behind after handoff to a signature that doesn't manage VirtualService")
	}
	if _, present := gotVS.Annotations[mitigation.AnnotationOriginalRetries]; present {
		t.Error("VirtualService original-retries annotation left behind after handoff")
	}
}
