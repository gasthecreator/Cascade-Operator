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

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

func patchTestVS() *networkingv1.VirtualService {
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

func TestRetryMitigateMissingVirtualServiceSetsCondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, retryStormOnlyQuerier())

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True", cond)
	}

	vs := &networkingv1.VirtualService{}
	err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs)
	if !apierrors.IsNotFound(err) {
		t.Errorf("VirtualService Get err = %v, want NotFound (must not create)", err)
	}
}

func TestRetryMitigateDetectOnlyDoesNotPatchVirtualService(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	vs := patchTestVS()
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(), vs)

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeDetectOnly)
	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("DetectOnly wrote managed-by: %v", got.Annotations)
	}
	if got.Spec.Http[0].Retries != nil {
		t.Error("DetectOnly mutated retries")
	}
}

func TestRetryMitigateFirstTripPatchesAndCapturesOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	vs := patchTestVS()
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(), vs)

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Errorf("managed-by = %q", got.Annotations[mitigation.AnnotationManagedBy])
	}
	if got.Annotations[mitigation.AnnotationOriginalRetries] == "" {
		t.Error("original-retries annotation not written")
	}
	if got.Spec.Http[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Errorf("attempts = %d, want %d", got.Spec.Http[0].Retries.GetAttempts(), mitigation.TripRetryAttempts)
	}
}

func TestRetryMitigateRetriggerDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const keptOriginal = `[{"attempts":3,"retryOn":"5xx"}]`
	vs := patchTestVS()
	vs.Annotations = map[string]string{
		mitigation.AnnotationManagedBy:       mitigation.ManagedByValue,
		mitigation.AnnotationOriginalRetries: keptOriginal,
	}
	vs.Spec.Http[0].Retries = &apinet.HTTPRetry{Attempts: mitigation.TripRetryAttempts}
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(), vs)

	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationOriginalRetries] != keptOriginal {
		t.Errorf("original overwritten: %s", got.Annotations[mitigation.AnnotationOriginalRetries])
	}
	if got.Spec.Http[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Error("re-trip did not re-apply trip attempts")
	}
}

// TestReconcileDoesNotWireRetryStormMitigationYet locks in the deliberate
// choice not to call applyRetryStormMitigation from Reconcile in this
// slice (see retry_mitigate.go's doc comment): a live retry-storm trip must
// leave an existing VirtualService completely untouched, same as it leaves
// DestinationRule untouched (TestRetryStormTripsStatusOnlyWithoutPatchingDestinationRule).
func TestReconcileDoesNotWireRetryStormMitigationYet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	vs := patchTestVS()
	r, c := patchReconcileWith(t, retryStormOnlyQuerier(), patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), vs)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("live Reconcile patched VirtualService: %v", got.Annotations)
	}
	if got.Spec.Http[0].Retries != nil {
		t.Error("live Reconcile mutated VirtualService retries")
	}
}
