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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

func retryStormOnlyQuerier() *fakeQuerier {
	// Latency/error under threshold; dest:source = 4 matches the Kind scrape
	// (retries.attempts:3 → 1 original + 3 retries).
	return &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 4.0}
}

func bothSignaturesQuerier() *fakeQuerier {
	return &fakeQuerier{p99: 900, errorRate: 0.2, retryStormRatio: 4.0}
}

// TestRetryStormTripPatchesConnectionPoolSecondaryButNotOutlierDetection
// confirms a retry-storm trip patches both the VirtualService primary
// (tested separately in retry_mitigate_test.go) and, now that this
// signature has its own DestinationRule connectionPool.http secondary
// (PLAN.md §2.6), the DestinationRule too — but only its own two fields
// (maxRetries, http1MaxPendingRequests), never latency/error-cascade's
// exclusive outlierDetection field on the same object kind.
func TestRetryStormTripPatchesConnectionPoolSecondaryButNotOutlierDetection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := patchTestDR()
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(),
		patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), dr)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", got.Status.Phase)
	}
	if got.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Errorf("lastSignature = %s, want RetryStorm", got.Status.LastSignature)
	}
	if got.Status.LastTrippedAt == nil {
		t.Error("lastTrippedAt is nil")
	}
	if got.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", got.Status.RestoreStep)
	}

	gotDR := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR); err != nil {
		t.Fatal(err)
	}
	if gotDR.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Errorf("retry storm secondary did not patch DestinationRule: %v", gotDR.Annotations)
	}
	if gotDR.Annotations[mitigation.AnnotationOriginalRetryConnectionPool] == "" {
		t.Error("retry storm secondary did not capture its own original-retry-connection-pool annotation")
	}
	http := gotDR.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetMaxRetries() != mitigation.TripRetryStormMaxRetries {
		t.Errorf("maxRetries = %d, want %d", http.GetMaxRetries(), mitigation.TripRetryStormMaxRetries)
	}
	if http.GetHttp1MaxPendingRequests() != mitigation.TripRetryStormMaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want %d", http.GetHttp1MaxPendingRequests(), mitigation.TripRetryStormMaxPendingRequests)
	}
	if gotDR.Spec.TrafficPolicy != nil && gotDR.Spec.TrafficPolicy.OutlierDetection != nil {
		t.Error("retry storm mutated outlierDetection (latency/error-cascade's exclusive field)")
	}
}

func TestRetryStormHealthySnapsToNormalWithoutRestoring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseTripped
	policy.Status.LastSignature = cascadev1alpha1.SignatureRetryStorm
	trippedAt := metav1.NewTime(time.Now().Add(-time.Hour))
	policy.Status.LastTrippedAt = &trippedAt

	// Unmanaged DestinationRule and no VirtualService at all: neither
	// listManagedDestinationRuleEdges nor listManagedVirtualServiceEdges
	// finds a managed edge, so this must fall straight to Normal.
	r, c := patchReconcileWith(t, healthyQuerier(), policy, patchTestDR())
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Errorf("phase = %s, want Normal (no Restoring; nothing was managed)", got.Status.Phase)
	}
	if got.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", got.Status.RestoreStep)
	}
	if got.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Errorf("lastSignature cleared: %s", got.Status.LastSignature)
	}

	gotDR := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR); err != nil {
		t.Fatal(err)
	}
	if gotDR.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("healthy tick annotated DestinationRule: %v", gotDR.Annotations)
	}
}

func TestLatencyErrorCascadeWinsWhenBothSignaturesCouldTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, bothSignaturesQuerier(),
		patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Errorf("lastSignature = %s, want LatencyErrorCascade (priority over RetryStorm)", got.Status.LastSignature)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", got.Status.Phase)
	}

	gotDR := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR); err != nil {
		t.Fatal(err)
	}
	if gotDR.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("latency/error win should still patch DestinationRule")
	}
}

// retryStormAndFanOutQuerier trips both retry storm (dest:source=4 >= the
// default multiplier 3) and fan-out (dependency:caller=6 >= the default
// multiplier 5) on the same host, to prove retry storm's earlier position
// in detectSignatures' per-host check order wins.
func retryStormAndFanOutQuerier() *fakeQuerier {
	return &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 4.0, fanOutRatio: 6.0}
}

// TestRetryStormWinsWhenRetryStormAndFanOutCouldTrip confirms fan-out sits
// last in detectSignatures' per-host priority order (latency/error, then
// retry storm, then fan-out) — the fan-out twin of
// TestLatencyErrorCascadeWinsWhenBothSignaturesCouldTrip above. Retry
// storm now has its own DestinationRule connectionPool.http secondary
// (PLAN.md §2.6), so "retry storm wins" no longer means the DestinationRule
// is left untouched — it means retry storm's *own* fields land there and
// fan-out's own field (Http2MaxRequests, never touched by retry storm's
// secondary) never does, since fan-out lost the race and its trip function
// never ran at all.
func TestRetryStormWinsWhenRetryStormAndFanOutCouldTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, retryStormAndFanOutQuerier(),
		patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Errorf("lastSignature = %s, want RetryStorm (priority over FanOutAmplification)", got.Status.LastSignature)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", got.Status.Phase)
	}

	gotDR := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR); err != nil {
		t.Fatal(err)
	}
	http := gotDR.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetMaxRetries() != mitigation.TripRetryStormMaxRetries || http.GetHttp1MaxPendingRequests() != mitigation.TripRetryStormMaxPendingRequests {
		t.Errorf("retry storm's own secondary did not patch its own fields: %+v", http)
	}
	if http.GetHttp2MaxRequests() != 0 {
		t.Errorf("fan-out's own field was touched even though fan-out lost the race: %d", http.GetHttp2MaxRequests())
	}
}

func TestRetryStormDoesNotCreateDestinationRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(),
		patchTestPolicy(cascadev1alpha1.PolicyModeMitigate))

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	gotDR := &networkingv1.DestinationRule{}
	err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (must not create)", err)
	}
}
