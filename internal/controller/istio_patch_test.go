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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	spv1alpha2 "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd/serviceprofile/v1alpha2"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

const (
	patchPolicyName  = "checkout-service"
	patchPolicyNS    = "default"
	patchDepName     = "payments-service"
	patchDepHost     = "payments-service.default.svc.cluster.local"
	patchServiceFQDN = "checkout-service.default.svc.cluster.local"
	inventoryDepHost = "inventory-service.default.svc.cluster.local"
)

func patchTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := cascadev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	// corev1/spv1alpha2 registered here too (not only in a Linkerd-specific
	// helper) so every existing fake-client test in this package can build
	// a Linkerd-mode CascadePolicy fixture without a second scheme —
	// registering unused types is harmless to the Istio-only tests that
	// already use this helper.
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := spv1alpha2.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func patchTestPolicy(mode cascadev1alpha1.PolicyMode) *cascadev1alpha1.CascadePolicy {
	return &cascadev1alpha1.CascadePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: patchPolicyName, Namespace: patchPolicyNS},
		Spec: cascadev1alpha1.CascadePolicySpec{
			Service:   patchServiceFQDN,
			DependsOn: []string{patchDepHost},
			Thresholds: cascadev1alpha1.Thresholds{
				LatencyP99Ms:         500,
				ErrorRateFraction:    0.05,
				WindowSeconds:        30,
				RetryStormMultiplier: 3.0,
				FanOutMultiplier:     5.0,
			},
			Mode: mode,
		},
	}
}

func patchTestDR() *networkingv1.DestinationRule {
	return &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: patchDepName, Namespace: patchPolicyNS},
		Spec:       apinet.DestinationRule{Host: patchDepHost},
	}
}

func patchReconcile(t *testing.T, objs ...client.Object) (*CascadePolicyReconciler, client.Client) {
	t.Helper()
	return patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, objs...)
}

func patchReconcileWith(t *testing.T, q *fakeQuerier, objs ...client.Object) (*CascadePolicyReconciler, client.Client) {
	t.Helper()
	s := patchTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&cascadev1alpha1.CascadePolicy{}).
		WithObjects(objs...).
		Build()
	return &CascadePolicyReconciler{
		Client:  c,
		Scheme:  s,
		Metrics: q,
	}, c
}

func TestMitigateMissingDestinationRuleSetsCondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate))

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", got.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("DependencyObjectMissing = %+v, want True", cond)
	}

	dr := &networkingv1.DestinationRule{}
	err = c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DestinationRule Get err = %v, want NotFound (must not create)", err)
	}
}

func TestDetectOnlyDoesNotPatchDestinationRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dr := patchTestDR()
	r, c := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeDetectOnly), dr)

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	})
	if err != nil {
		t.Fatal(err)
	}

	gotPolicy := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, gotPolicy); err != nil {
		t.Fatal(err)
	}
	if gotPolicy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped (detection is not mode-gated)", gotPolicy.Status.Phase)
	}

	gotDR := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotDR); err != nil {
		t.Fatal(err)
	}
	if gotDR.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("DetectOnly wrote managed-by: %v", gotDR.Annotations)
	}
	if gotDR.Spec.TrafficPolicy != nil && gotDR.Spec.TrafficPolicy.OutlierDetection != nil {
		t.Error("DetectOnly mutated outlierDetection")
	}
}

func TestMitigateFirstTripPatchesAndCapturesOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Errorf("managed-by = %q", got.Annotations[mitigation.AnnotationManagedBy])
	}
	if got.Annotations[mitigation.AnnotationOriginalOutlier] != mitigation.OriginalOutlierUnsetJSON {
		t.Errorf("original = %s, want unset sentinel", got.Annotations[mitigation.AnnotationOriginalOutlier])
	}
	od := got.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od == nil {
		t.Fatal("outlierDetection not set")
	}
	if od.GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Errorf("consecutive5xx = %d", od.GetConsecutive_5XxErrors().GetValue())
	}
	if od.GetInterval().AsDuration() != mitigation.TripInterval {
		t.Errorf("interval = %s", od.GetInterval().AsDuration())
	}
	if od.GetBaseEjectionTime().AsDuration() != mitigation.TripBaseEjection {
		t.Errorf("baseEjectionTime = %s", od.GetBaseEjectionTime().AsDuration())
	}
}

func TestMitigateRetriggerDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const keptOriginal = `{"consecutive5xxErrors":5,"interval":"10s"}`
	dr := patchTestDR()
	dr.Annotations = map[string]string{
		mitigation.AnnotationManagedBy:       mitigation.ManagedByValue,
		mitigation.AnnotationOriginalOutlier: keptOriginal,
	}
	r, c := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), dr)

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[mitigation.AnnotationOriginalOutlier] != keptOriginal {
		t.Errorf("original overwritten: %s", got.Annotations[mitigation.AnnotationOriginalOutlier])
	}
	if got.Spec.GetTrafficPolicy().GetOutlierDetection().GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Error("re-trip did not re-apply trip values")
	}
}
