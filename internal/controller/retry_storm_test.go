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

func TestRetryStormTripsStatusOnlyWithoutPatchingDestinationRule(t *testing.T) {
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
	if gotDR.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("retry storm annotated DestinationRule: %v", gotDR.Annotations)
	}
	if gotDR.Spec.TrafficPolicy != nil && gotDR.Spec.TrafficPolicy.OutlierDetection != nil {
		t.Error("retry storm mutated outlierDetection")
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

	// Unmanaged DestinationRule: listManagedEdges must not treat it as ours.
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
