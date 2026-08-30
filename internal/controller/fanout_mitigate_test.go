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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

func fanOutOnlyQuerier() *fakeQuerier {
	// Latency/error and retry storm under threshold; dependency:caller = 6
	// well above the 5.0 CRD default fanOutMultiplier.
	return &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 1.0, fanOutRatio: 6.0}
}

func TestFanOutMitigateMissingDestinationRuleSetsCondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, fanOutOnlyQuerier())

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if err := r.applyFanOutMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True", cond)
	}

	dr := &networkingv1.DestinationRule{}
	err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (must not create)", err)
	}
}

func TestFanOutMitigateDetectOnlyDoesNotPatchDestinationRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := patchTestDR()
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), dr)

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeDetectOnly)
	if err := r.applyFanOutMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("DetectOnly wrote managed-by: %v", got.Annotations)
	}
	if got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp() != nil {
		t.Error("DetectOnly mutated connectionPool.http")
	}
}

func TestFanOutMitigateFirstTripPatchesAndCapturesOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := patchTestDR()
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), dr)

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if err := r.applyFanOutMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Errorf("managed-by = %q", got.Annotations[mitigation.AnnotationManagedBy])
	}
	if got.Annotations[mitigation.AnnotationOriginalConnectionPool] != mitigation.OriginalConnectionPoolUnsetJSON {
		t.Errorf("original = %s, want unset sentinel", got.Annotations[mitigation.AnnotationOriginalConnectionPool])
	}
	http := got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http == nil {
		t.Fatal("connectionPool.http not set")
	}
	if http.GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != mitigation.TripHTTP2MaxRequests {
		t.Errorf("http2MaxRequests = %d", http.GetHttp2MaxRequests())
	}
}

func TestFanOutMitigateRetriggerDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const keptOriginal = `{"http1MaxPendingRequests":64,"http2MaxRequests":128}`
	dr := patchTestDR()
	dr.Annotations = map[string]string{
		mitigation.AnnotationManagedBy:              mitigation.ManagedByValue,
		mitigation.AnnotationOriginalConnectionPool: keptOriginal,
	}
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), dr)

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if err := r.applyFanOutMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationOriginalConnectionPool] != keptOriginal {
		t.Errorf("original overwritten: %s", got.Annotations[mitigation.AnnotationOriginalConnectionPool])
	}
	if got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp().GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Error("re-trip did not re-apply trip values")
	}
}

// TestReconcileWiresFanOutMitigationLive confirms a live fan-out trip
// through the full Reconcile path (not just the unit-level
// applyFanOutMitigation call above) actually patches connectionPool.http —
// same shape as TestReconcileWiresRetryStormMitigationLive, one signature
// over.
func TestReconcileWiresFanOutMitigationLive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := patchTestDR()
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), dr)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Errorf("live Reconcile did not patch DestinationRule: %v", got.Annotations)
	}
	if got.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp().GetHttp1MaxPendingRequests() != mitigation.TripHTTP1MaxPendingRequests {
		t.Error("live Reconcile did not cap connectionPool.http")
	}

	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.LastSignature != cascadev1alpha1.SignatureFanOutAmplification {
		t.Errorf("lastSignature = %s, want FanOutAmplification", p.Status.LastSignature)
	}
}

func TestFanOutDoesNotCreateDestinationRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, fanOutOnlyQuerier(), patchTestPolicy(cascadev1alpha1.PolicyModeMitigate))

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	gotDR := &networkingv1.DestinationRule{}
	err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (must not create)", err)
	}
}
