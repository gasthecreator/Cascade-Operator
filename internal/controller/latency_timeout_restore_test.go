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

	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/types"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// TestLatencyErrorRestoreAdvancesBothObjectKindsTogetherThenCompletes is the
// full end-to-end ramp for the two-object-kind shape this slice added — the
// single-object-kind version of this test is
// TestRestoreAdvancesEachHealthyStepThenCompletes (restore_test.go). Both
// the DestinationRule primary and the VirtualService secondary are managed
// by the same signature here, so they must advance in lockstep, tick for
// tick, and both land on their true original (with both annotations
// stripped) at the same completion tick.
func TestLatencyErrorRestoreAdvancesBothObjectKindsTogetherThenCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	vs := singleRouteVSFor(patchDepHost)
	// A real (non-Unset) pre-trip timeout, so the ramp is a smooth
	// monotonic curve across all 5 steps rather than the Unset case's
	// ramp-toward-default-then-snap-to-cleared-at-the-final-step shape
	// (same distinction ApplyLatencyErrorTimeoutRestoreStep's own tests
	// already cover in the mitigation package).
	vs.Spec.Http[0].Timeout = durationpb.New(2 * time.Second)
	mitigation.ApplyLatencyErrorTimeoutTrip(vs, 500)
	r, c := patchReconcileWith(t, healthyQuerier(), seededPolicy(cascadev1alpha1.PolicyPhaseTripped, 0), trippedManagedDR(), vs)

	wantPhase := []cascadev1alpha1.PolicyPhase{
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseRestoring,
		cascadev1alpha1.PolicyPhaseNormal,
	}
	wantStep := []int32{0, 1, 2, 3, 4, 0}

	var prevTimeout time.Duration
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

		gotVS := &networkingv1.VirtualService{}
		if err := c.Get(ctx, types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, gotVS); err != nil {
			t.Fatal(err)
		}

		if wantPhase[i] == cascadev1alpha1.PolicyPhaseNormal {
			if dr.Annotations[mitigation.AnnotationManagedBy] != "" || dr.Annotations[mitigation.AnnotationOriginalOutlier] != "" {
				t.Errorf("tick %d DestinationRule annotations remain: %v", i, dr.Annotations)
			}
			if gotVS.Annotations[mitigation.AnnotationManagedBy] != "" || gotVS.Annotations[mitigation.AnnotationOriginalTimeout] != "" {
				t.Errorf("tick %d VirtualService annotations remain: %v", i, gotVS.Annotations)
			}
			if gotVS.Spec.Http[0].Timeout.AsDuration() != 2*time.Second {
				t.Errorf("VirtualService timeout on complete = %s, want true original 2s", gotVS.Spec.Http[0].Timeout.AsDuration())
			}
			continue
		}

		if dr.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d DestinationRule stripped managed-by while still Restoring", i)
		}
		if gotVS.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
			t.Errorf("tick %d VirtualService stripped managed-by while still Restoring", i)
		}
		got := gotVS.Spec.Http[0].Timeout.AsDuration()
		if got <= 500*time.Millisecond {
			t.Errorf("tick %d VirtualService timeout = %s, want > trip value 500ms", i, got)
		}
		if i > 0 && got < prevTimeout {
			t.Errorf("tick %d VirtualService timeout went backwards: %s -> %s", i, prevTimeout, got)
		}
		prevTimeout = got
	}
}
