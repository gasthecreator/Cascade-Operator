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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch

// applyRetryStormMitigation resolves both the VirtualService primary and
// the DestinationRule connectionPool.http secondary for host by convention
// (PLAN.md §2.6) and, in Mitigate mode, patches whichever exists —
// independently, not as a joint precondition. Same two-object-kind
// independence shape latency/error-cascade's timeout secondary already
// proved (mitigate.go's applyLatencyErrorMitigation doc comment), one
// signature over: the primary applies even if the secondary's
// DestinationRule is missing, and vice versa, and DependencyObjectMissing
// stays scoped to the primary (VirtualService) only — see
// applyRetryStormRetriesPrimary/applyRetryStormConnectionPoolSecondary
// below. That independence is carrying out the two-object-kind shape
// already locked in §2.6 after review, not a new decision.
//
// This is the *third* signature to potentially manage a DestinationRule
// (latency/error-cascade's outlierDetection, fan-out's connectionPool.http,
// and now this signature's own MaxRetries on that same sub-message). The
// three field sets are disjoint: retry storm no longer writes
// Http1MaxPendingRequests (PLAN.md §2.6, overlap resolved 2026-08-30,
// direction 2).
func (r *CascadePolicyReconciler) applyRetryStormMitigation(
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

	if err := r.applyRetryStormRetriesPrimary(ctx, policy, host, name, ns); err != nil {
		return err
	}
	return r.applyRetryStormConnectionPoolSecondary(ctx, policy, name, ns)
}

// applyRetryStormRetriesPrimary patches VirtualService retries.attempts —
// unchanged behavior from before this signature grew a secondary, still
// the sole driver of DependencyObjectMissing (see
// applyRetryStormMitigation's doc comment above for why the secondary's
// own absence doesn't also set it).
func (r *CascadePolicyReconciler) applyRetryStormRetriesPrimary(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host, name, ns string,
) error {
	log := logf.FromContext(ctx)

	vs := &networkingv1.VirtualService{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)
	if isAbsent(err) {
		log.Info("VirtualService missing; skipping primary patch", "name", name, "namespace", ns)
		setDependencyMissing(policy, fmt.Sprintf("VirtualService %s/%s not found for dependsOn %q", ns, name, host))
		return nil
	}
	if err != nil {
		return fmt.Errorf("get VirtualService %s/%s: %w", ns, name, err)
	}
	clearDependencyMissing(policy)

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cut VirtualService retries.attempts",
			"name", name,
			"namespace", ns,
			"attempts", mitigation.TripRetryAttempts,
		)
		return nil
	}

	mitigation.ApplyRetryStormTrip(vs)
	if err := r.Patch(ctx, vs, client.RawPatch(types.JSONPatchType, mitigation.RetryStormAttemptsJSONPatch(vs))); err != nil {
		return fmt.Errorf("patch VirtualService %s/%s: %w", ns, name, err)
	}
	mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm), kindVirtualService).Inc()
	log.Info("patched VirtualService retries.attempts", "name", name, "namespace", ns)
	return nil
}

// applyRetryStormConnectionPoolSecondary patches DestinationRule
// connectionPool.http's maxRetries (PLAN.md §2.6's secondary). Deliberately
// never touches DependencyObjectMissing either way — see
// applyRetryStormMitigation's doc comment: a missing DestinationRule here
// is logged but does not flag the edge, and a present one must not
// incorrectly clear a condition the primary's own absence may have
// correctly set.
func (r *CascadePolicyReconciler) applyRetryStormConnectionPoolSecondary(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	name, ns string,
) error {
	log := logf.FromContext(ctx)

	dr := &networkingv1.DestinationRule{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
	if isAbsent(err) {
		log.Info("DestinationRule missing; skipping secondary patch", "name", name, "namespace", ns)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
	}

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cap DestinationRule connectionPool.http maxRetries",
			"name", name,
			"namespace", ns,
			"maxRetries", mitigation.TripRetryStormMaxRetries,
		)
		return nil
	}

	mitigation.ApplyRetryStormConnectionPoolTrip(dr)
	if err := r.Patch(ctx, dr, client.RawPatch(types.MergePatchType, mitigation.RetryStormMaxRetriesMergePatch(dr))); err != nil {
		return fmt.Errorf("patch DestinationRule %s/%s: %w", ns, name, err)
	}
	mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm), kindDestinationRule).Inc()
	log.Info("patched DestinationRule connectionPool.http (retry storm secondary)", "name", name, "namespace", ns)
	return nil
}
