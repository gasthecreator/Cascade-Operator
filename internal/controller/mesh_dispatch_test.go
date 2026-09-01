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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	spv1alpha2 "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd/serviceprofile/v1alpha2"
)

// Tests in this file exist to prove PLAN.md §5 Phase 6.6's reconciler
// wiring actually dispatches on policy.Spec.Mesh through the real
// Reconcile path — not just that internal/mesh/linkerd's Mitigator/
// QueryBuilder work correctly in isolation (already covered by that
// package's own tests). Fixtures here deliberately include *no*
// DestinationRule/VirtualService at all: if queryBuilder()/mitigator()
// silently fell back to the Istio default despite spec.mesh: Linkerd,
// these tests would either error (no Istio object to patch, but
// mitigation still tries) or simply fail to observe any Service/
// ServiceProfile mutation — either way a real, distinguishing failure
// mode, not a coincidental pass.

func linkerdTestPolicy(mode cascadev1alpha1.PolicyMode) *cascadev1alpha1.CascadePolicy {
	p := patchTestPolicy(mode)
	p.Spec.Mesh = cascadev1alpha1.MeshLinkerd
	return p
}

func linkerdTestService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: patchDepName, Namespace: patchPolicyNS},
	}
}

func linkerdTestServiceProfile(rb *spv1alpha2.RetryBudget) *spv1alpha2.ServiceProfile {
	return &spv1alpha2.ServiceProfile{
		ObjectMeta: metav1.ObjectMeta{Name: patchDepHost, Namespace: patchPolicyNS},
		Spec: spv1alpha2.ServiceProfileSpec{
			Routes: []spv1alpha2.RouteSpec{
				{Name: "GET /", Condition: spv1alpha2.RequestMatch{Method: "GET", PathRegex: "/"}, IsRetryable: true},
			},
			RetryBudget: rb,
		},
	}
}

func TestLinkerdModeTripsServiceAnnotationsNotDestinationRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := linkerdTestService()
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, linkerdTestPolicy(cascadev1alpha1.PolicyModeMitigate), svc)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	policy := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, policy); err != nil {
		t.Fatal(err)
	}
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("phase = %s, want Tripped", policy.Status.Phase)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Fatalf("lastSignature = %s, want LatencyErrorCascade", policy.Status.LastSignature)
	}

	got := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations["balancer.linkerd.io/failure-accrual"] != "consecutive" {
		t.Errorf("Service failure-accrual annotation = %q, want %q (Linkerd Mitigator should have patched the Service)",
			got.Annotations["balancer.linkerd.io/failure-accrual"], "consecutive")
	}

	dr := &networkingv1.DestinationRule{}
	err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (Linkerd mode must never touch Istio objects)", err)
	}
}

func TestIstioModeStillDefaultWhenMeshUnset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := patchTestDR()
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	if policy.Spec.Mesh != "" {
		t.Fatalf("test fixture precondition: spec.mesh = %q, want unset", policy.Spec.Mesh)
	}
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, policy, dr)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.GetTrafficPolicy().GetOutlierDetection() == nil {
		t.Error("DestinationRule outlierDetection not patched — an unset spec.mesh must still default to Istio")
	}
}

func TestLinkerdModeRetryStormFullTripAndRestoreCycleViaReconcile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sp := linkerdTestServiceProfile(&spv1alpha2.RetryBudget{RetryRatio: 1.0, MinRetriesPerSecond: 10, TTL: "10s"})
	policy := linkerdTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 6.0}, policy, sp)

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	got := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepHost, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.RetryBudget.RetryRatio != 0 || got.Spec.RetryBudget.MinRetriesPerSecond != 0 {
		t.Fatalf("retryBudget after trip = %+v, want fully suppressed (0, 0)", got.Spec.RetryBudget)
	}

	// Go healthy and drive the restore ramp to completion through
	// Reconcile itself — proving RestoreStep/Phase transitions dispatch
	// through the Linkerd Mitigator on every tick, not just the trip.
	healthy := &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 1.0}
	r.Metrics = healthy
	for i := range 6 {
		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatalf("restore tick %d: %v", i, err)
		}
	}

	final := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, final); err != nil {
		t.Fatal(err)
	}
	if final.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Errorf("phase after restore cycle = %s, want Normal", final.Status.Phase)
	}

	gotFinal := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepHost, Namespace: patchPolicyNS}, gotFinal); err != nil {
		t.Fatal(err)
	}
	if gotFinal.Spec.RetryBudget == nil || gotFinal.Spec.RetryBudget.RetryRatio != 1.0 || gotFinal.Spec.RetryBudget.MinRetriesPerSecond != 10 {
		t.Errorf("retryBudget after full restore = %+v, want the true original (1.0, 10, %s)", gotFinal.Spec.RetryBudget, "10s")
	}
	if len(gotFinal.Annotations) != 0 {
		t.Errorf("annotations after full restore = %v, want none", gotFinal.Annotations)
	}
}
