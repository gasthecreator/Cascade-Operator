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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// Shared across every dependsOn object-kind lookup (DestinationRule,
// VirtualService, ...) — the DependencyObjectMissing condition is generic,
// not tied to one Istio kind.
const (
	reasonDependencyObjectNotFound = "DependencyObjectNotFound"
	reasonDependencyObjectFound    = "DependencyObjectFound"
)

// applyLatencyErrorMitigation delegates to the reconciler's Mitigator
// (PLAN.md §5 Phase 6.4) to resolve and, in Mitigate mode, patch whatever
// primitives that mesh uses for latency/error-cascade — the
// DestinationRule primary (outlierDetection) and, independently, the
// VirtualService secondary (route timeout). DependencyObjectMissing stays
// a controller-owned, mesh-agnostic concern, driven only by the
// Mitigator's PrimaryFound return value: the primary applies even if the
// secondary's object is missing and vice versa (they are independent, not
// a joint precondition — see mesh.Mitigator's own doc comment), and a
// missing secondary must not flip a condition the primary's own presence
// already correctly cleared.
func (r *CascadePolicyReconciler) applyLatencyErrorMitigation(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) error {
	outcome, err := r.mitigator(policy).ApplyTrip(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade, host)
	if err != nil {
		return fmt.Errorf("apply latency/error trip for %q: %w", host, err)
	}
	if !outcome.PrimaryFound {
		setDependencyMissing(policy, fmt.Sprintf("no mitigation target found for dependsOn %q", host))
		return nil
	}
	clearDependencyMissing(policy)
	for _, kind := range outcome.AppliedKinds {
		mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kind).Inc()
	}
	return nil
}

func isAbsent(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

func setDependencyMissing(policy *cascadev1alpha1.CascadePolicy, message string) {
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    cascadev1alpha1.ConditionTypeDependencyObjectMissing,
		Status:  metav1.ConditionTrue,
		Reason:  reasonDependencyObjectNotFound,
		Message: message,
	})
}

func clearDependencyMissing(policy *cascadev1alpha1.CascadePolicy) {
	cond := meta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return
	}
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    cascadev1alpha1.ConditionTypeDependencyObjectMissing,
		Status:  metav1.ConditionFalse,
		Reason:  reasonDependencyObjectFound,
		Message: "Dependency object resolved for the tripped dependency",
	})
}
