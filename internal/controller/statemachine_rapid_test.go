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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"

	"pgregory.net/rapid"
)

// rapidSignalMode is the querier state driven per tick by
// TestRapidSignatureHandoffNeverOrphansAnnotations — deliberately excludes
// retry storm (its primary is a VirtualService, not the DestinationRule this
// test's fixture uses) so every generated sequence stays confined to the two
// signatures that actually share one DestinationRule (PLAN.md §2.6) and can
// hand off between each other: latency/error cascade and fan-out.
type rapidSignalMode int

const (
	rapidHealthy rapidSignalMode = iota
	rapidLatencyError
	rapidFanOut
)

// rapidQuerier is a mutable metrics.Querier: the mode field is changed
// between reconciles by the property test driving it, so one fake client +
// one reconciler can be walked through an arbitrary trip/handoff/restore
// sequence. Query-shape dispatch mirrors fakeQuerier/hostAwareQuerier
// (cascadepolicy_controller_test.go, test/integration/cluster.go) — matched
// by which PromQL substrings are present, not by host, since this test uses
// a single dependsOn host throughout.
type rapidQuerier struct {
	mode rapidSignalMode
}

func (q *rapidQuerier) Query(_ context.Context, promql string) (metrics.Snapshot, error) {
	v := 0.001 // healthy error-rate default
	switch {
	case strings.Contains(promql, "histogram_quantile"):
		v = 80
		if q.mode == rapidLatencyError {
			v = 600 // >= thresholds.latencyP99Ms:500
		}
	case strings.Contains(promql, `reporter="source"`):
		v = 1.0 // retry-storm ratio: always healthy, this signature is out of scope here
	case strings.Contains(promql, `reporter="destination"`):
		v = 1.0
		if q.mode == rapidFanOut {
			v = 10.0 // >= thresholds.fanOutMultiplier:2
		}
	default:
		if q.mode == rapidLatencyError {
			v = 0.10 // >= thresholds.errorRateFraction:0.05
		}
	}
	return metrics.Snapshot{Samples: []metrics.Sample{{Value: v}}}, nil
}

// TestRapidSignatureHandoffNeverOrphansAnnotations drives the real Reconcile
// path through a randomly generated sequence of healthy/latency-error/
// fan-out ticks on a single shared DestinationRule and checks, after every
// tick, the three invariants PLAN.md §2.6 and the zero-value/handoff
// worklogs establish as things that must always hold — not just for the
// hand-picked scenarios the existing non-property tests cover, but for any
// sequence rapid can generate:
//
//  1. RestoreStep always stays within [0, mitigation.RestoreFinalStep].
//  2. While continuously Restoring, RestoreStep only ever advances by
//     exactly one step or resets to 0 (a regression/handoff) — never an
//     arbitrary jump.
//  3. Whenever LastSignature changes to a different, non-empty value than
//     the tick before (a handoff, or a fresh trip after a full restore), the
//     outgoing signature's own original-* annotation is never still present
//     on the DestinationRule — proving forceCompleteOutgoingRestore ran
//     synchronously rather than leaving it for a later tick.
//  4. Once Phase reaches Normal, neither signature's original-* annotation
//     survives on the object — restoration is never partial at rest.
func TestRapidSignatureHandoffNeverOrphansAnnotations(t *testing.T) {
	s := patchTestScheme(t) // built once with *testing.T; rapid.Check's callback only gets *rapid.T
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()

		policy := &cascadev1alpha1.CascadePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: patchPolicyName, Namespace: patchPolicyNS},
			Spec: cascadev1alpha1.CascadePolicySpec{
				Service:   patchServiceFQDN,
				DependsOn: []string{patchDepHost},
				Thresholds: cascadev1alpha1.Thresholds{
					LatencyP99Ms:         500,
					ErrorRateFraction:    0.05,
					WindowSeconds:        30,
					RetryStormMultiplier: 3,
					FanOutMultiplier:     2,
				},
				Mode: cascadev1alpha1.PolicyModeMitigate,
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(s).
			WithStatusSubresource(&cascadev1alpha1.CascadePolicy{}).
			WithObjects(policy, patchTestDR()).
			Build()

		q := &rapidQuerier{}
		r := &CascadePolicyReconciler{Client: c, Scheme: s, Metrics: q}

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}}
		drKey := types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}

		var prevPhase cascadev1alpha1.PolicyPhase
		var prevStep int32
		var prevSig cascadev1alpha1.SignatureType

		numTicks := rapid.IntRange(1, 15).Draw(t, "numTicks")
		for i := range numTicks {
			q.mode = rapid.SampledFrom([]rapidSignalMode{rapidHealthy, rapidLatencyError, rapidFanOut}).
				Draw(t, fmt.Sprintf("mode%d", i))

			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatalf("tick %d (mode=%v): Reconcile: %v", i, q.mode, err)
			}

			got := &cascadev1alpha1.CascadePolicy{}
			if err := c.Get(ctx, req.NamespacedName, got); err != nil {
				t.Fatalf("tick %d: get policy: %v", i, err)
			}
			dr := &networkingv1.DestinationRule{}
			if err := c.Get(ctx, drKey, dr); err != nil {
				t.Fatalf("tick %d: get DestinationRule: %v", i, err)
			}

			step := got.Status.RestoreStep
			if step < 0 || step > mitigation.RestoreFinalStep {
				t.Fatalf("tick %d: RestoreStep = %d, out of [0,%d]", i, step, mitigation.RestoreFinalStep)
			}

			if prevPhase == cascadev1alpha1.PolicyPhaseRestoring && got.Status.Phase == cascadev1alpha1.PolicyPhaseRestoring {
				if step != prevStep+1 && step != 0 {
					t.Fatalf("tick %d: RestoreStep jumped %d -> %d while continuously Restoring", i, prevStep, step)
				}
			}

			if got.Status.Phase == cascadev1alpha1.PolicyPhaseNormal {
				if _, ok := dr.Annotations[mitigation.AnnotationOriginalOutlier]; ok {
					t.Fatalf("tick %d: Phase=Normal but %s still present", i, mitigation.AnnotationOriginalOutlier)
				}
				if _, ok := dr.Annotations[mitigation.AnnotationOriginalConnectionPool]; ok {
					t.Fatalf("tick %d: Phase=Normal but %s still present", i, mitigation.AnnotationOriginalConnectionPool)
				}
			}

			if prevSig != "" && got.Status.LastSignature != "" && got.Status.LastSignature != prevSig {
				switch prevSig {
				case cascadev1alpha1.SignatureLatencyErrorCascade:
					if _, ok := dr.Annotations[mitigation.AnnotationOriginalOutlier]; ok {
						t.Fatalf("tick %d: handoff away from LatencyErrorCascade left %s orphaned", i, mitigation.AnnotationOriginalOutlier)
					}
				case cascadev1alpha1.SignatureFanOutAmplification:
					if _, ok := dr.Annotations[mitigation.AnnotationOriginalConnectionPool]; ok {
						t.Fatalf("tick %d: handoff away from FanOutAmplification left %s orphaned", i, mitigation.AnnotationOriginalConnectionPool)
					}
				}
			}

			prevPhase = got.Status.Phase
			prevStep = step
			prevSig = got.Status.LastSignature
		}
	})
}
