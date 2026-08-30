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
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

func healthyQuerier() *fakeQuerier {
	return &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 1.0}
}

func trippedManagedDR() *networkingv1.DestinationRule {
	dr := patchTestDR()
	mitigation.ApplyLatencyErrorOutlierTrip(dr)
	return dr
}

func seededPolicy(phase cascadev1alpha1.PolicyPhase, step int32) *cascadev1alpha1.CascadePolicy {
	p := patchTestPolicy(cascadev1alpha1.PolicyModeMitigate)
	p.Status.Phase = phase
	p.Status.RestoreStep = step
	p.Status.LastSignature = cascadev1alpha1.SignatureLatencyErrorCascade
	trippedAt := metav1.NewTime(time.Now().Add(-time.Hour))
	p.Status.LastTrippedAt = &trippedAt
	return p
}

func restoreRequest() reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	}
}

func getPolicyAndDR(t *testing.T, c client.Client) (*cascadev1alpha1.CascadePolicy, *networkingv1.DestinationRule) {
	t.Helper()
	ctx := context.Background()
	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}, p); err != nil {
		t.Fatal(err)
	}
	dr := &networkingv1.DestinationRule{}
	if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	return p, dr
}

func TestRestoreEntersAtStepZeroWhenTrippedGoesHealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(), seededPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedDR())

	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, dr := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseRestoring {
		t.Errorf("phase = %s, want Restoring", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	od := dr.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetInterval().AsDuration() != 6*time.Second {
		t.Errorf("step 0 interval = %s, want 6s (first loosening toward 10s default)", od.GetInterval().AsDuration())
	}
	if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by stripped too early")
	}
}

func TestRestoreAdvancesEachHealthyStepThenCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, healthyQuerier(), seededPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedDR())

	wantPhase := []cascadev1alpha1.PolicyPhase{
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseNormal,
	}
	wantStep := []int32{0, 1, 2, 3, 4, 0}

	for i := range wantPhase {
		if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		p, dr := getPolicyAndDR(t, c)
		if p.Status.Phase != wantPhase[i] {
			t.Errorf("tick %d phase = %s, want %s", i, p.Status.Phase, wantPhase[i])
		}
		if p.Status.RestoreStep != wantStep[i] {
			t.Errorf("tick %d restoreStep = %d, want %d", i, p.Status.RestoreStep, wantStep[i])
		}
		if wantPhase[i] == cascadev1alpha1.PolicyPhaseNormal {
			if dr.Annotations[mitigation.AnnotationManagedBy] != "" || dr.Annotations[mitigation.AnnotationOriginalOutlier] != "" {
				t.Errorf("tick %d annotations remain: %v", i, dr.Annotations)
			}
			if dr.Spec.GetTrafficPolicy().GetOutlierDetection() != nil {
				t.Error("unset original should clear outlierDetection on complete")
			}
			continue
		}
		if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d stripped managed-by while still Restoring", i)
		}
	}
}

func TestRestoreRegressionReTripsAndBumpsLastTrippedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, 2)
	oldTrip := policy.Status.LastTrippedAt.DeepCopy()
	dr := trippedManagedDR()
	if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(dr, 2); err != nil {
		t.Fatal(err)
	}

	r, c := patchReconcileWith(t, &fakeQuerier{p99: 900, errorRate: 0.2}, policy, dr)
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if p.Status.LastTrippedAt == nil || !p.Status.LastTrippedAt.After(oldTrip.Time) {
		t.Errorf("LastTrippedAt not bumped: old=%v new=%v", oldTrip, p.Status.LastTrippedAt)
	}
	od := got.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od.GetConsecutive_5XxErrors().GetValue() != mitigation.TripConsecutive5xx {
		t.Errorf("consecutive5xx = %d, want trip value", od.GetConsecutive_5XxErrors().GetValue())
	}
	if od.GetInterval().AsDuration() != mitigation.TripInterval {
		t.Errorf("interval = %s, want trip value", od.GetInterval().AsDuration())
	}
	if got.Annotations[mitigation.AnnotationOriginalOutlier] != mitigation.OriginalOutlierUnsetJSON {
		t.Error("original annotation overwritten on regression")
	}
}

func TestRestoreCompleteWithStoredOriginalValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const original = `{"consecutive5xxErrors":7,"interval":"10s"}`
	dr := trippedManagedDR()
	dr.Annotations[mitigation.AnnotationOriginalOutlier] = original

	policy := seededPolicy(cascadev1alpha1.PolicyPhaseRestoring, mitigation.RestoreFinalStep)
	r, c := patchReconcileWith(t, healthyQuerier(), policy, dr)
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, got := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseNormal {
		t.Errorf("phase = %s, want Normal", p.Status.Phase)
	}
	if p.Status.RestoreStep != 0 {
		t.Errorf("restoreStep = %d, want 0", p.Status.RestoreStep)
	}
	if got.Annotations[mitigation.AnnotationManagedBy] != "" || got.Annotations[mitigation.AnnotationOriginalOutlier] != "" {
		t.Errorf("annotations remain: %v", got.Annotations)
	}
	od := got.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od == nil {
		t.Fatal("outlierDetection cleared; original was not unset")
	}
	if od.GetConsecutive_5XxErrors().GetValue() != 7 {
		t.Errorf("consecutive5xx = %d, want 7", od.GetConsecutive_5XxErrors().GetValue())
	}
	if od.GetInterval().AsDuration() != 10*time.Second {
		t.Errorf("interval = %s, want 10s", od.GetInterval().AsDuration())
	}
	if od.BaseEjectionTime != nil {
		t.Error("baseEjectionTime should remain unset")
	}
}

func TestQueryErrorWhileTrippedDoesNotRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := patchReconcileWith(t, &fakeQuerier{err: fmt.Errorf("prometheus unavailable")},
		seededPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedDR())
	if _, err := r.Reconcile(ctx, restoreRequest()); err != nil {
		t.Fatal(err)
	}
	p, dr := getPolicyAndDR(t, c)
	if p.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase = %s, want Tripped (no healthy evaluation)", p.Status.Phase)
	}
	if dr.Spec.GetTrafficPolicy().GetOutlierDetection().GetInterval().AsDuration() != mitigation.TripInterval {
		t.Error("query error loosened the trip patch")
	}
}
