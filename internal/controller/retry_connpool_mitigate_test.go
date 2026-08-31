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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// These tests exercise applyRetryStormMitigation's two-object-kind shape
// directly, the retry-storm twin of latency_timeout_mitigate_test.go's own
// suite for the same shape. See applyRetryStormMitigation's doc comment
// (retry_mitigate.go) for the reasoning these tests confirm:
//   - the primary (VirtualService) applies even if the secondary
//     (DestinationRule) is missing, and vice versa — each resolved
//     independently, not as a joint precondition;
//   - DependencyObjectMissing tracks the primary only.

func TestRetryStormMitigateBothObjectsPresentPatchesBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t, patchTestDR(), patchTestVS())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond != nil && cond.Status == metav1.ConditionTrue {
		t.Errorf("DependencyObjectMissing = True, want unset/False when both objects are present")
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Spec.Http[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Error("VirtualService primary not patched")
	}
	if vs.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("VirtualService not marked managed-by")
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetMaxRetries() != mitigation.TripRetryStormMaxRetries || http.GetHttp1MaxPendingRequests() != mitigation.TripRetryStormMaxPendingRequests {
		t.Errorf("DestinationRule secondary not patched: http = %+v", http)
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("DestinationRule not marked managed-by")
	}
	if _, present := dr.Annotations[mitigation.AnnotationOriginalRetryConnectionPool]; !present {
		t.Error("DestinationRule missing original-retry-connection-pool capture")
	}
}

func TestRetryStormMitigateMissingDestinationRuleStillPatchesPrimary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// No DestinationRule seeded at all.
	r, c := patchReconcile(t, patchTestVS())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond != nil && cond.Status == metav1.ConditionTrue {
		t.Error("DependencyObjectMissing = True, want unset/False: the primary is present, and a missing *secondary* must not flag the edge")
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Spec.Http[0].Retries.GetAttempts() != mitigation.TripRetryAttempts {
		t.Error("VirtualService primary not patched despite the secondary DestinationRule being absent")
	}
}

func TestRetryStormMitigateMissingVirtualServiceStillPatchesSecondaryAndSetsCondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// No VirtualService seeded at all.
	r, c := patchReconcile(t, patchTestDR())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True: the primary VirtualService is missing", cond)
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http.GetMaxRetries() != mitigation.TripRetryStormMaxRetries || http.GetHttp1MaxPendingRequests() != mitigation.TripRetryStormMaxPendingRequests {
		t.Error("DestinationRule secondary not patched despite the primary VirtualService being absent")
	}
}

func TestRetryStormMitigateBothObjectsMissingSetsConditionAndCreatesNeither(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t)
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True", cond)
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); !isAbsent(err) {
		t.Errorf("VirtualService Get err = %v, want NotFound (must not create)", err)
	}
	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); !isAbsent(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (must not create)", err)
	}
}

func TestRetryStormDetectOnlyDoesNotPatchEitherObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t, patchTestDR(), patchTestVS())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeDetectOnly)

	if err := r.applyRetryStormMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Error("DetectOnly wrote managed-by on the VirtualService")
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Error("DetectOnly wrote managed-by on the DestinationRule")
	}
	if dr.Spec.GetTrafficPolicy().GetConnectionPool() != nil {
		t.Error("DetectOnly mutated the DestinationRule's connectionPool")
	}
}
