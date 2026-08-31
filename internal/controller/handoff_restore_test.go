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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// TestSignatureHandoffLatencyErrorToFanOutForceCompletesOutgoing is the
// scenario PROPOSALS.md's "Signature handoff on a shared DestinationRule"
// entry (approved 2026-08-30) and forceCompleteOutgoingRestore's doc
// comment (restore.go) describe: latency/error-cascade is mid-Restoring
// (step 2, a partially-loosened outlierDetection) when, on the same tick,
// its own condition has cleared but fan-out's has crossed threshold on the
// same host — no intervening healthy tick. The outgoing signature's true
// original outlierDetection must be restored and its own annotation
// dropped in this same reconcile call, not left at the mid-ramp
// interpolated value, before fan-out's trip is applied fresh to the same
// object.
func TestSignatureHandoffLatencyErrorToFanOutForceCompletesOutgoing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const originalOutlier = `{"consecutive5xxErrors":7,"interval":"10s"}`
	dr := trippedManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalOutlier] = originalOutlier
	if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}

	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), policy, dr)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndDR(t, c)

	if p.Status.LastSignature != cascadev1alpha1.SignatureFanOutAmplification {
		t.Errorf("lastSignature = %s, want FanOutAmplification", p.Status.LastSignature)
	}
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}

	// Outgoing signature (latency/error) landed on its TRUE original, not
	// left at step 2's mid-ramp interpolated value, and its own annotation
	// is gone — not just "not overwritten", actually removed.
	od := got.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetConsecutive_5XxErrors().GetValue() != 7 {
		t.Errorf("consecutive5xx = %d, want true original 7 (not mid-ramp interpolated)", od.GetConsecutive_5XxErrors().GetValue())
	}
	if od.GetInterval().AsDuration() != 10*time.Second {
		t.Errorf("interval = %s, want true original 10s (not mid-ramp interpolated)", od.GetInterval().AsDuration())
	}
	if _, present := got.Annotations[mitigation.AnnotationOriginalOutlier]; present {
		t.Error("outgoing signature's original-outlier-detection annotation left behind after handoff")
	}

	// Incoming signature (fan-out) applied its own trip fresh, with its own
	// annotation, on the very same object.
	http := got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want trip value %d", http.GetHttp1MaxPendingRequests(), mitigation.TripHTTP1MaxPendingRequests)
	}
	if got.Annotations[mitigation.AnnotationOriginalConnectionPool] != mitigation.OriginalConnectionPoolUnsetJSON {
		t.Errorf("original-connection-pool = %s, want a fresh unset-sentinel capture", got.Annotations[mitigation.AnnotationOriginalConnectionPool])
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by not (re-)set by the incoming signature's trip")
	}
}

// TestSignatureHandoffFanOutToLatencyErrorForceCompletesOutgoing mirrors
// the test above in the other direction: fan-out mid-Restoring (step 2,
// partially-loosened connectionPool.http) hands off to latency/error on
// the same host/same tick.
func TestSignatureHandoffFanOutToLatencyErrorForceCompletesOutgoing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const originalPool = `{"http1MaxPendingRequests":64,"http2MaxRequests":128}`
	dr := trippedFanOutManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalConnectionPool] = originalPool
	if err := mitigation.ApplyFanOutConnectionPoolRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}

	policy := seededFanOutPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, policy, dr)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndDR(t, c)

	if p.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Errorf("lastSignature = %s, want LatencyErrorCascade", p.Status.LastSignature)
	}
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}

	// Outgoing signature (fan-out) landed on its TRUE original, not left at
	// step 2's mid-ramp interpolated value, and its own annotation is gone.
	http := got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want true original 64 (not mid-ramp interpolated)", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want true original 128 (not mid-ramp interpolated)", http.GetHttp2MaxRequests())
	}
	if _, present := got.Annotations[mitigation.AnnotationOriginalConnectionPool]; present {
		t.Error("outgoing signature's original-connection-pool annotation left behind after handoff")
	}

	// Incoming signature (latency/error) applied its own trip fresh, with
	// its own annotation, on the very same object.
	od := got.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Errorf("consecutive5xx = %d, want trip value %d", od.GetConsecutive_5XxErrors().GetValue(), mitigation.TripConsecutive5xx)
	}
	if got.Annotations[mitigation.AnnotationOriginalOutlier] != mitigation.OriginalOutlierUnsetJSON {
		t.Errorf("original-outlier-detection = %s, want a fresh unset-sentinel capture", got.Annotations[mitigation.AnnotationOriginalOutlier])
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by not (re-)set by the incoming signature's trip")
	}
}

// singleRouteVSFor builds a minimal, unmanaged VirtualService with one
// forwarding route to patchDepHost — every caller needs exactly this host
// (it's the only dependsOn entry patchTestPolicy declares), so this takes
// no parameter rather than a host argument every call site would pass
// identically.
func singleRouteVSFor() *networkingv1.VirtualService {
	return &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: patchDepName, Namespace: patchPolicyNS},
		Spec: apinet.VirtualService{
			Hosts: []string{patchDepHost},
			Http: []*apinet.HTTPRoute{
				{Route: []*apinet.HTTPRouteDestination{{Destination: &apinet.Destination{Host: patchDepHost}}}},
			},
		},
	}
}

// TestSignatureHandoffLatencyErrorToFanOutForceCompletesBothObjectKinds
// extends TestSignatureHandoffLatencyErrorToFanOutForceCompletesOutgoing
// (above) for the object kind this slice added: latency/error-cascade is
// now mid-Restoring on *both* a DestinationRule (outlierDetection primary)
// and a VirtualService (timeout secondary, PLAN.md §2.6) when fan-out trips
// on the same host, same tick. forceCompleteOutgoingRestore's
// LatencyErrorCascade case must force-complete both, not just the
// DestinationRule — a VirtualService left mid-ramp (or annotated at all)
// after this handoff would be exactly the "orphaned outgoing signature
// state" bug PROPOSALS.md's handoff entry already resolved for the single-
// object-kind case, just on the object kind fan-out never touches (so
// nothing else would ever clean it up).
func TestSignatureHandoffLatencyErrorToFanOutForceCompletesBothObjectKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const originalOutlier = `{"consecutive5xxErrors":7,"interval":"10s"}`
	dr := trippedManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalOutlier] = originalOutlier
	if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}

	const originalTimeout = `[{"timeout":"4s"}]`
	vs := singleRouteVSFor()
	mitigation.ApplyLatencyErrorTimeoutTrip(vs, 500)
	vs.Annotations[mitigation.AnnotationOriginalTimeout] = originalTimeout
	if err := mitigation.ApplyLatencyErrorTimeoutRestoreStep(vs, 2, 500); err != nil {
		t.Fatal(err)
	}

	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
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

	// Outgoing signature's DestinationRule side: true original, not the
	// step-2 interpolated value; incoming signature's own trip applied
	// fresh — same assertions as the single-object-kind test above.
	od := gotDR.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetConsecutive_5XxErrors().GetValue() != 7 {
		t.Errorf("consecutive5xx = %d, want true original 7", od.GetConsecutive_5XxErrors().GetValue())
	}
	http := gotDR.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want fan-out's trip value %d", http.GetHttp1MaxPendingRequests(), mitigation.TripHTTP1MaxPendingRequests)
	}

	// Outgoing signature's VirtualService side: the object fan-out never
	// touches, so it must land on the true original with *both* operator
	// annotations gone entirely — nothing else claims this object this
	// tick, so nothing should be left managed on it at all.
	gotVS := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotVS); err != nil {
		t.Fatal(err)
	}
	if gotVS.Spec.Http[0].Timeout.AsDuration() != 4*time.Second {
		t.Errorf("VirtualService timeout = %s, want true original 4s (not mid-ramp interpolated)", gotVS.Spec.Http[0].Timeout.AsDuration())
	}
	if gotVS.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Error("VirtualService managed-by left behind after handoff to a signature that doesn't manage VirtualService")
	}
	if _, present := gotVS.Annotations[mitigation.AnnotationOriginalTimeout]; present {
		t.Error("VirtualService original-timeout annotation left behind after handoff")
	}
}

// TestSignatureHandoffFanOutToLatencyErrorPatchesFreshVirtualServiceSecondary
// mirrors the test above in the other direction: fan-out mid-Restoring
// (DestinationRule only — fan-out never manages a VirtualService) hands off
// to latency/error-cascade on the same host/tick. The DestinationRule side
// is the existing single-object-kind scenario
// (TestSignatureHandoffFanOutToLatencyErrorForceCompletesOutgoing); this
// adds the new part: latency/error's incoming trip must also patch its
// VirtualService secondary fresh, from a completely unmanaged object, on
// this same tick — a handoff is not a special case that should suppress
// the secondary.
func TestSignatureHandoffFanOutToLatencyErrorPatchesFreshVirtualServiceSecondary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const originalPool = `{"http1MaxPendingRequests":64,"http2MaxRequests":128}`
	dr := trippedFanOutManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalConnectionPool] = originalPool
	if err := mitigation.ApplyFanOutConnectionPoolRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}
	vs := singleRouteVSFor() // unmanaged — no prior annotations at all

	policy := seededFanOutPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, policy, dr, vs)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, gotDR := getPolicyAndDR(t, c)

	if p.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Errorf("lastSignature = %s, want LatencyErrorCascade", p.Status.LastSignature)
	}
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}

	// Outgoing signature's DestinationRule side: unchanged from the
	// existing single-object-kind test — true original connectionPool,
	// incoming signature's fresh outlierDetection trip.
	http := gotDR.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want true original 64", http.GetHttp1MaxPendingRequests())
	}
	od := gotDR.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Errorf("consecutive5xx = %d, want trip value %d", od.GetConsecutive_5XxErrors().GetValue(), mitigation.TripConsecutive5xx)
	}

	// Incoming signature's VirtualService secondary: patched fresh on this
	// same tick, even though the object was never previously managed by
	// anyone.
	gotVS := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotVS); err != nil {
		t.Fatal(err)
	}
	if gotVS.Spec.Http[0].Timeout.AsDuration() != 500*time.Millisecond {
		t.Errorf("VirtualService timeout = %s, want fresh trip value 500ms", gotVS.Spec.Http[0].Timeout.AsDuration())
	}
	if gotVS.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("VirtualService not marked managed by the incoming signature's secondary")
	}
	if _, present := gotVS.Annotations[mitigation.AnnotationOriginalTimeout]; !present {
		t.Error("VirtualService missing a fresh original-timeout capture")
	}
}

// TestSignatureReTripSameSignatureDoesNotForceComplete confirms the common
// case is unaffected: re-tripping the *same* signature (outgoingSig == sig)
// must never force-complete anything. A single dependsOn edge makes the
// final DestinationRule state identical whether or not force-complete
// incorrectly ran first (a same-signature re-trip re-patches to the same
// trip values regardless), so this asserts something a same-signature
// re-trip would actually distinguish: the stored original-outlier
// annotation is deliberately malformed JSON. ApplyLatencyErrorOutlierTrip
// (the trip path) only ever checks the annotation's *presence*, never its
// contents, so a correct same-signature re-trip leaves it untouched and
// Reconcile succeeds. CompleteLatencyErrorOutlierRestore (the restore
// path, which forceCompleteOutgoingRestore would call if it incorrectly
// fired here) parses it and would return an error — so if this test ever
// starts failing with a parse error, that is exactly the signal that the
// outgoingSig != sig guard in Reconcile's trip branch broke.
func TestSignatureReTripSameSignatureDoesNotForceComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := trippedManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalOutlier] = "not valid json"

	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, policy, dr)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatalf("re-trip of the same signature must not force-complete (and must not fail parsing the malformed original annotation): %v", err)
	}
	p, got := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if p.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Errorf("lastSignature = %s, want unchanged LatencyErrorCascade", p.Status.LastSignature)
	}
	if got.Annotations[mitigation.AnnotationOriginalOutlier] != "not valid json" {
		t.Error("original annotation touched on a same-signature re-trip; force-complete must not have run")
	}
}

// A fresh CR's first-ever trip (LastSignature == "") is deliberately not
// covered by a dedicated test here: it is exactly
// TestMitigateFirstTripPatchesAndCapturesOriginal's scenario
// (istio_patch_test.go), which already exercises Reconcile's trip branch
// end to end on a policy with no prior LastSignature, and which continues
// to pass unchanged after this slice's handoff guard — the outgoingSig !=
// "" condition never engages there, and this is direct evidence of that
// rather than something needing its own duplicate test.
