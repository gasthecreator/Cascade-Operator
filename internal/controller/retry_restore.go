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

type managedVSEdge struct {
	host string
	vs   *networkingv1.VirtualService
}

// listManagedVirtualServiceEdges is listManagedDestinationRuleEdges' twin
// for the retry-storm restore path: same convention-based resolution, same
// managed-by filter, one object kind over.
func (r *CascadePolicyReconciler) listManagedVirtualServiceEdges(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
) ([]managedVSEdge, error) {
	log := logf.FromContext(ctx)
	var out []managedVSEdge
	for _, host := range policy.Spec.DependsOn {
		name, ns, err := mitigation.ParseServiceFQDN(host)
		if err != nil {
			log.Error(err, "cannot resolve VirtualService from dependsOn FQDN", "host", host)
			continue
		}
		vs := &networkingv1.VirtualService{}
		err = r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)
		if isAbsent(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get VirtualService %s/%s: %w", ns, name, err)
		}
		if mitigation.IsVirtualServiceManaged(vs) {
			out = append(out, managedVSEdge{host: host, vs: vs})
		}
	}
	return out, nil
}

// beginRestoreRetryStorm, advanceRestoreRetryStorm,
// applyRetryStormRestoreStep, and completeRetryStormRestore all gather and
// act on *both* object kinds this signature now manages — the
// VirtualService primary (retries.attempts) and the DestinationRule
// secondary (connectionPool.http maxRetries, PLAN.md §2.6) — independently of each other, mirroring
// applyRetryStormMitigation's own independence and exactly the shape
// beginRestoreLatencyError/advanceRestoreLatencyError/
// completeLatencyErrorRestore (restore.go) already established for the
// first two-object-kind signature. An edge with only one of the two
// objects managed still restores that one correctly, an edge with both
// restores both, and "nothing managed at all" (both lists empty) is the
// only case that snaps straight to Normal. A single
// restorationsCompletedTotal increment covers a completion touching
// either or both object kinds.
func (r *CascadePolicyReconciler) beginRestoreRetryStorm(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	vsEdges, err := r.listManagedVirtualServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	drEdges, err := r.listManagedDestinationRuleEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(vsEdges) == 0 && len(drEdges) == 0 {
		log.Info("No managed VirtualService or DestinationRule to restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseRestoring
	policy.Status.RestoreStep = 0
	log.Info("Entered restoration ramp", "restoreStep", policy.Status.RestoreStep, "vsEdges", len(vsEdges), "drEdges", len(drEdges))
	return r.applyRetryStormRestoreStep(ctx, policy, vsEdges, drEdges, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreRetryStorm(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	vsEdges, err := r.listManagedVirtualServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	drEdges, err := r.listManagedDestinationRuleEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(vsEdges) == 0 && len(drEdges) == 0 {
		log.Info("Managed VirtualService and DestinationRule both gone during restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}

	if policy.Status.RestoreStep >= mitigation.RestoreFinalStep {
		if err := r.completeRetryStormRestore(ctx, policy, vsEdges, drEdges); err != nil {
			return err
		}
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		log.Info("Completed restoration ramp")
		return nil
	}

	next := policy.Status.RestoreStep + 1
	policy.Status.RestoreStep = next
	log.Info("Advanced restoration step", "restoreStep", next)
	return r.applyRetryStormRestoreStep(ctx, policy, vsEdges, drEdges, next)
}

func (r *CascadePolicyReconciler) applyRetryStormRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	vsEdges []managedVSEdge,
	drEdges []managedDREdge,
	step int32,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would ramp VirtualService retries.attempts and DestinationRule connectionPool.http",
			"restoreStep", step,
			"vsEdges", len(vsEdges),
			"drEdges", len(drEdges),
		)
		return nil
	}
	for _, e := range vsEdges {
		if err := mitigation.ApplyRetryStormRestoreStep(e.vs, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		patch := client.RawPatch(types.MergePatchType, mitigation.RetryStormRestoreStepMergePatch(e.vs))
		if err := r.Patch(ctx, e.vs, patch); err != nil {
			return fmt.Errorf("patch VirtualService during restore %s: %w", e.host, err)
		}
	}
	for _, e := range drEdges {
		if err := mitigation.ApplyRetryStormConnectionPoolRestoreStep(e.dr, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule during restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (r *CascadePolicyReconciler) completeRetryStormRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	vsEdges []managedVSEdge,
	drEdges []managedDREdge,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original retries.attempts/connectionPool.http and drop annotations",
			"vsEdges", len(vsEdges),
			"drEdges", len(drEdges),
		)
		return nil
	}
	for _, e := range vsEdges {
		if err := mitigation.CompleteRetryStormRestore(e.vs); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		patch := client.RawPatch(types.MergePatchType, mitigation.RetryStormRestoreCompleteJSONPatch(e.vs))
		if err := r.Patch(ctx, e.vs, patch); err != nil {
			return fmt.Errorf("patch VirtualService completing restore %s: %w", e.host, err)
		}
	}
	// Deliberately still a typed Update, not a patch (unlike the VirtualService
	// loop above) — investigated for PLAN.md §5 Phase 5 and found not to be
	// the same bug. originalRetryConnectionPoolJSON.MaxRetries itself has
	// omitempty, so a true original of exactly 0 is already indistinguishable
	// from "was never set" at annotation-capture time, before this write is
	// ever reached — applyOriginalRetryConnectionPool's own doc comment
	// already treats a restored 0 as "go back to absent" on purpose. Writing
	// an explicit 0 here via patch would both contradict that documented
	// intent and accomplish nothing at Envoy anyway: Istio Pilot's own
	// DestinationRule->CDS translation ignores an explicit MaxRetries of 0
	// regardless of how it's written (PROPOSALS.md, approved 2026-08-30,
	// direction 2 — the reason this signature's trip value is 1, not 0).
	for _, e := range drEdges {
		if err := mitigation.CompleteRetryStormConnectionPoolRestore(e.dr); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule completing restore %s: %w", e.host, err)
		}
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm)).Inc()
	r.notifyRestore(ctx, policy, cascadev1alpha1.SignatureRetryStorm)
	return nil
}
