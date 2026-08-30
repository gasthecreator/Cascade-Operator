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
