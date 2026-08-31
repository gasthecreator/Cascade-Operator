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

// TestSignatureHandoffFanOutToRetryStormLeavesFanOutFieldsUntouched: fan-out
// is mid-Restoring (step 2) when retry storm trips on the same host, same
// tick. Force-complete must land fan-out's true original on *its* fields
// before retry storm's trip; retry storm then writes only MaxRetries and
// must not touch Http1MaxPendingRequests/Http2MaxRequests at all.
func TestSignatureHandoffFanOutToRetryStormLeavesFanOutFieldsUntouched(t *testing.T) {
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
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want fan-out's true original 64 (retry storm must not write this field)", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want fan-out's true original 128 (not mid-ramp interpolated)", http.GetHttp2MaxRequests())
	}
	if http.GetMaxRetries() != mitigation.TripRetryStormMaxRetries {
		t.Errorf("maxRetries = %d, want retry storm's trip value %d", http.GetMaxRetries(), mitigation.TripRetryStormMaxRetries)
	}
	if _, present := gotDR.Annotations[mitigation.AnnotationOriginalConnectionPool]; present {
		t.Error("fan-out's original-connection-pool annotation left behind after handoff")
	}
	// Retry storm only captures MaxRetries. Fan-out never wrote it, so the
	// captured original is empty (0 omitted).
	if got := gotDR.Annotations[mitigation.AnnotationOriginalRetryConnectionPool]; got != "{}" {
		t.Errorf("original-retry-connection-pool = %s, want {}", got)
	}

	gotVS := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotVS); err != nil {
		t.Fatal(err)
	}
	if gotVS.Spec.Http[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Error("retry storm's own VirtualService primary was not applied fresh on this handoff")
	}
}

// TestSignatureHandoffRetryStormToFanOutForceCompletesBothObjectKinds is
// the reverse direction, and confirms forceCompleteOutgoingRestore's
// RetryStorm case force-completes *both* object kinds this signature
// manages — not just the DestinationRule fan-out also happens to share.
func TestSignatureHandoffRetryStormToFanOutForceCompletesBothObjectKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	vs := trippedManagedVS()
	if err := mitigation.ApplyRetryStormRestoreStep(vs, 2); err != nil {
		t.Fatal(err)
	}

	const originalRetryPool = `{"maxRetries":10}`
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
	if http.GetMaxRetries() != 10 {
		t.Errorf("maxRetries = %d, want retry storm's true original 10 (not mid-ramp interpolated, not fan-out-disturbed)", http.GetMaxRetries())
	}
	if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want fan-out's trip value %d", http.GetHttp1MaxPendingRequests(), mitigation.TripHTTP1MaxPendingRequests)
	}
	if http.GetHttp2MaxRequests() != mitigation.TripHTTP2MaxRequests {
		t.Errorf("http2MaxRequests = %d, want fan-out's trip value %d", http.GetHttp2MaxRequests(), mitigation.TripHTTP2MaxRequests)
	}
	if _, present := gotDR.Annotations[mitigation.AnnotationOriginalRetryConnectionPool]; present {
		t.Error("retry storm's original-retry-connection-pool annotation left behind after handoff")
	}

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
