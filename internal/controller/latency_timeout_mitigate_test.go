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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// These tests exercise applyLatencyErrorMitigation's two-object-kind shape
// directly (mirroring TestRetryMitigateMissingVirtualServiceSetsCondition's
// style, retry_mitigate_test.go) rather than going through the full
// Reconcile loop, since the point here is the primary/secondary
// independence itself, not detection. See applyLatencyErrorMitigation's doc
// comment (mitigate.go) for the reasoning these tests confirm:
//   - the primary (DestinationRule) applies even if the secondary
//     (VirtualService) is missing, and vice versa — each resolved
//     independently, not as a joint precondition;
//   - DependencyObjectMissing tracks the primary only.

func TestLatencyErrorMitigateBothObjectsPresentPatchesBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t, patchTestDR(), patchTestVS())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyLatencyErrorMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond != nil && cond.Status == metav1.ConditionTrue {
		t.Errorf("DependencyObjectMissing = True, want unset/False when both objects are present")
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.GetTrafficPolicy().GetOutlierDetection().GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Error("DestinationRule primary not patched")
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("DestinationRule not marked managed-by")
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Spec.Http[0].Timeout.AsDuration() != 500*time.Millisecond {
		t.Errorf("VirtualService secondary not patched: timeout = %s, want 500ms (policy's latencyP99Ms)", vs.Spec.Http[0].Timeout.AsDuration())
	}
	if vs.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("VirtualService not marked managed-by")
	}
	if _, present := vs.Annotations[mitigation.AnnotationOriginalTimeout]; !present {
		t.Error("VirtualService missing original-timeout capture")
	}
}

func TestLatencyErrorMitigateMissingVirtualServiceStillPatchesPrimary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// No VirtualService seeded at all.
	r, c := patchReconcile(t, patchTestDR())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyLatencyErrorMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond != nil && cond.Status == metav1.ConditionTrue {
		t.Error("DependencyObjectMissing = True, want unset/False: the primary is present, and a missing *secondary* must not flag the edge")
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.GetTrafficPolicy().GetOutlierDetection().GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Error("DestinationRule primary not patched despite the secondary VirtualService being absent")
	}
}

func TestLatencyErrorMitigateMissingDestinationRuleStillPatchesSecondaryAndSetsCondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// No DestinationRule seeded at all.
	r, c := patchReconcile(t, patchTestVS())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyLatencyErrorMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True: the primary DestinationRule is missing", cond)
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Spec.Http[0].Timeout.AsDuration() != 500*time.Millisecond {
		t.Error("VirtualService secondary not patched despite the primary DestinationRule being absent")
	}
}

func TestLatencyErrorMitigateBothObjectsMissingSetsConditionAndCreatesNeither(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t)
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)

	if err := r.applyLatencyErrorMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	cond := apimeta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True", cond)
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); !isAbsent(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (must not create)", err)
	}
	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); !isAbsent(err) {
		t.Errorf("VirtualService Get err = %v, want NotFound (must not create)", err)
	}
}

func TestLatencyErrorDetectOnlyDoesNotPatchEitherObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t, patchTestDR(), patchTestVS())
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeDetectOnly)

	if err := r.applyLatencyErrorMitigation(ctx, policy, patchDepHost); err != nil {
		t.Fatal(err)
	}

	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Error("DetectOnly wrote managed-by on the DestinationRule")
	}

	vs := &networkingv1.VirtualService{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Error("DetectOnly wrote managed-by on the VirtualService")
	}
	if vs.Spec.Http[0].Timeout != nil {
		t.Error("DetectOnly mutated the VirtualService's timeout")
	}
}
