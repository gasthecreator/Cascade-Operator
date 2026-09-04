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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// linkerdTestPolicy always builds a Mitigate-mode policy — every test in
// this file needs mitigation to actually run, so unlike patchTestPolicy
// (whose callers span both Mitigate and DetectOnly) this helper takes no
// mode parameter.
func linkerdTestPolicy() *cascadev1alpha1.CascadePolicy {
	p := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
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
	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, linkerdTestPolicy(), svc)

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
	policy := linkerdTestPolicy()
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

// Tests below prove metricsQuerier()'s own dispatch: a real, previously-
// hidden bug (2026-09-03, docs/worklog/2026-09-03-operator-in-cluster-deploy-and-metrics-scrape.md)
// found this session live — a single process-wide Metrics querier silently
// starves whichever mesh it doesn't scrape, since a CascadePolicy on that
// mesh reconciles forever without ever seeing real data. MetricsIstio/
// MetricsLinkerd fix that; these tests fail the old way (no reconciler
// change) and pass the new way.

func TestMetricsQuerierDispatch(t *testing.T) {
	t.Parallel()
	shared := &fakeQuerier{p99: 1}
	istioOnly := &fakeQuerier{p99: 2}
	linkerdOnly := &fakeQuerier{p99: 3}

	istioPolicy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	linkerdPolicy := linkerdTestPolicy()

	cases := []struct {
		name   string
		r      CascadePolicyReconciler
		policy *cascadev1alpha1.CascadePolicy
		want   *fakeQuerier
	}{
		{
			name:   "istio policy falls back to shared Metrics when MetricsIstio unset",
			r:      CascadePolicyReconciler{Metrics: shared},
			policy: istioPolicy,
			want:   shared,
		},
		{
			name:   "istio policy prefers MetricsIstio over shared Metrics",
			r:      CascadePolicyReconciler{Metrics: shared, MetricsIstio: istioOnly},
			policy: istioPolicy,
			want:   istioOnly,
		},
		{
			name:   "linkerd policy falls back to shared Metrics when MetricsLinkerd unset",
			r:      CascadePolicyReconciler{Metrics: shared},
			policy: linkerdPolicy,
			want:   shared,
		},
		{
			name:   "linkerd policy prefers MetricsLinkerd over shared Metrics",
			r:      CascadePolicyReconciler{Metrics: shared, MetricsLinkerd: linkerdOnly},
			policy: linkerdPolicy,
			want:   linkerdOnly,
		},
		{
			name:   "linkerd policy is unaffected by MetricsIstio",
			r:      CascadePolicyReconciler{Metrics: shared, MetricsIstio: istioOnly},
			policy: linkerdPolicy,
			want:   shared,
		},
		{
			name:   "istio policy is unaffected by MetricsLinkerd",
			r:      CascadePolicyReconciler{Metrics: shared, MetricsLinkerd: linkerdOnly},
			policy: istioPolicy,
			want:   shared,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.r.metricsQuerier(tc.policy)
			if got != tc.want {
				t.Errorf("metricsQuerier() = %p, want %p", got, tc.want)
			}
		})
	}
}

// TestReconcileIstioPolicyUsesMetricsIstioNotSharedMetrics proves the
// dispatch through the real Reconcile path, not just the method in
// isolation: shared Metrics is wired to data that would never trip, while
// MetricsIstio is wired to data that trips immediately. If Reconcile still
// consulted the shared field for an Istio-mesh policy (the pre-fix
// behavior), this policy would stay Normal forever, exactly the silent
// failure mode this slice fixes.
func TestReconcileIstioPolicyUsesMetricsIstioNotSharedMetrics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := patchTestScheme(t)
	dr := patchTestDR()
	policy := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&cascadev1alpha1.CascadePolicy{}).
		WithObjects(policy, dr).
		Build()
	r := &CascadePolicyReconciler{
		Client:       c,
		Scheme:       s,
		Metrics:      &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 1.0}, // never trips
		MetricsIstio: &fakeQuerier{p99: 900, errorRate: 0.2},                        // trips immediately
	}

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("phase = %s, want Tripped (Reconcile must use MetricsIstio, not the healthy shared Metrics)", got.Status.Phase)
	}
}

// TestReconcileLinkerdPolicyUsesMetricsLinkerdNotSharedMetrics is the
// Linkerd-mesh mirror of the test above.
func TestReconcileLinkerdPolicyUsesMetricsLinkerdNotSharedMetrics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := patchTestScheme(t)
	svc := linkerdTestService()
	policy := linkerdTestPolicy()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&cascadev1alpha1.CascadePolicy{}).
		WithObjects(policy, svc).
		Build()
	r := &CascadePolicyReconciler{
		Client:         c,
		Scheme:         s,
		Metrics:        &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 1.0}, // never trips
		MetricsLinkerd: &fakeQuerier{p99: 900, errorRate: 0.2},                        // trips immediately
	}

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("phase = %s, want Tripped (Reconcile must use MetricsLinkerd, not the healthy shared Metrics)", got.Status.Phase)
	}
}

// TestReconcileNoMetricsForMeshNeverPolls locks in the exact silent-failure
// shape this slice's worklog describes: a mesh with no matching Querier at
// all (no shared Metrics, no mesh-specific field) never polls Prometheus
// and so never trips — no error, same as before this fix, since a
// deployment might legitimately run only one mesh.
func TestReconcileNoMetricsForMeshNeverPolls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := patchTestScheme(t)
	svc := linkerdTestService()
	policy := linkerdTestPolicy()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&cascadev1alpha1.CascadePolicy{}).
		WithObjects(policy, svc).
		Build()
	r := &CascadePolicyReconciler{
		Client:       c,
		Scheme:       s,
		MetricsIstio: &fakeQuerier{p99: 900, errorRate: 0.2}, // wrong mesh, must not be consulted
	}

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Fatalf("phase = %s, want Normal (no Metrics/MetricsLinkerd configured for this Linkerd policy)", got.Status.Phase)
	}
}
