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
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Shared across every dependsOn object-kind lookup (DestinationRule,
// VirtualService, ...) — the DependencyObjectMissing condition is generic,
// not tied to one Istio kind.
const (
	reasonDependencyObjectNotFound = "DependencyObjectNotFound"
	reasonDependencyObjectFound    = "DependencyObjectFound"
)

// applyLatencyErrorMitigation resolves both the DestinationRule primary
// and the VirtualService secondary for host by convention (PLAN.md §2.6)
// and, in Mitigate mode, patches whichever exists — independently, not as a
// joint precondition. This is the first signature to manage two object
// kinds on a single trip, so the two-object-kind shape was worked through
// deliberately rather than defaulted:
//
//   - The primary applies even if the secondary's VirtualService is
//     missing. §2.6 calls the timeout a *secondary* — additive to outlier
//     detection, not a co-requirement — so a missing backstop object must
//     not silently disable the mitigation this project actually claims as
//     its gap vs. hand-tuned Istio circuit breaking.
//   - Symmetrically, the secondary applies even if the primary's
//     DestinationRule is missing: there's no principled reason to
//     withhold the fail-fast timeout backstop just because the
//     outlier-detection object happens not to exist for this dependency.
//   - DependencyObjectMissing stays a single boolean, and stays scoped to
//     the primary only (see applyLatencyErrorOutlierPrimary /
//     applyLatencyErrorTimeoutSecondary below). A missing secondary is
//     logged at info level and is separately observable via
//     mitigationPatchesAppliedTotal{kind="VirtualService"} simply not
//     incrementing that tick — but it does not flip the condition. With
//     two independent objects, a missing *secondary* while the primary is
//     present still means real mitigation is happening for this edge;
//     flipping a generic "this edge is broken" condition there would
//     overstate the problem. A missing *primary* still flips the
//     condition exactly as before (unchanged behavior for that case) —
//     the primary not applying does mean this edge isn't getting the
//     mitigation this project claims.
func (r *CascadePolicyReconciler) applyLatencyErrorMitigation(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) error {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		log.Error(err, "cannot resolve dependsOn FQDN", "host", host)
		setDependencyMissing(policy, fmt.Sprintf("cannot parse dependsOn FQDN %q", host))
		return nil
	}

	if err := r.applyLatencyErrorOutlierPrimary(ctx, policy, host, name, ns); err != nil {
		return err
	}
	return r.applyLatencyErrorTimeoutSecondary(ctx, policy, name, ns)
}

// applyLatencyErrorOutlierPrimary patches DestinationRule outlierDetection —
// unchanged behavior from before this signature grew a secondary, still the
// sole driver of DependencyObjectMissing (see applyLatencyErrorMitigation's
// doc comment above for why the secondary's own absence doesn't also set it).
func (r *CascadePolicyReconciler) applyLatencyErrorOutlierPrimary(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host, name, ns string,
) error {
	log := logf.FromContext(ctx)

	dr := &networkingv1.DestinationRule{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
	if isAbsent(err) {
		log.Info("DestinationRule missing; skipping primary patch", "name", name, "namespace", ns)
		setDependencyMissing(policy, fmt.Sprintf("DestinationRule %s/%s not found for dependsOn %q", ns, name, host))
		return nil
	}
	if err != nil {
		return fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
	}
	clearDependencyMissing(policy)

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would patch DestinationRule outlierDetection",
			"name", name,
			"namespace", ns,
			"consecutive5xxErrors", mitigation.TripConsecutive5xx,
			"interval", mitigation.TripInterval.String(),
			"baseEjectionTime", mitigation.TripBaseEjection.String(),
		)
		return nil
	}

	mitigation.ApplyLatencyErrorOutlierTrip(dr)
	if err := r.Update(ctx, dr); err != nil {
		return fmt.Errorf("update DestinationRule %s/%s: %w", ns, name, err)
	}
	mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindDestinationRule).Inc()
	log.Info("patched DestinationRule outlierDetection", "name", name, "namespace", ns)
	return nil
}

// applyLatencyErrorTimeoutSecondary patches VirtualService route timeout
// down to the policy's own latencyP99Ms threshold (PLAN.md §2.6's
// secondary). Deliberately never touches DependencyObjectMissing either
// way — see applyLatencyErrorMitigation's doc comment: a missing
// VirtualService here is logged but does not flag the edge, and a present
// one must not incorrectly clear a condition the primary's own absence may
// have correctly set.
func (r *CascadePolicyReconciler) applyLatencyErrorTimeoutSecondary(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	name, ns string,
) error {
	log := logf.FromContext(ctx)

	vs := &networkingv1.VirtualService{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)
	if isAbsent(err) {
		log.Info("VirtualService missing; skipping secondary patch", "name", name, "namespace", ns)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get VirtualService %s/%s: %w", ns, name, err)
	}

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cap VirtualService route timeout",
			"name", name,
			"namespace", ns,
			"timeoutMs", policy.Spec.Thresholds.LatencyP99Ms,
		)
		return nil
	}

	mitigation.ApplyLatencyErrorTimeoutTrip(vs, policy.Spec.Thresholds.LatencyP99Ms)
	if err := r.Update(ctx, vs); err != nil {
		return fmt.Errorf("update VirtualService %s/%s: %w", ns, name, err)
	}
	mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade), kindVirtualService).Inc()
	log.Info("patched VirtualService timeout", "name", name, "namespace", ns)
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
