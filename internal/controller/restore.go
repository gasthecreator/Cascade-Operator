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
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

type managedEdge struct {
	host string
	dr   *networkingv1.DestinationRule
}

// listManagedEdges resolves each dependsOn FQDN and returns DestinationRules
// this operator has already annotated. Missing objects are skipped; they
// do not fail the reconcile (same spirit as a missing trip target).
func (r *CascadePolicyReconciler) listManagedEdges(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
) ([]managedEdge, error) {
	log := logf.FromContext(ctx)
	var out []managedEdge
	for _, host := range policy.Spec.DependsOn {
		name, ns, err := mitigation.ParseServiceFQDN(host)
		if err != nil {
			log.Error(err, "cannot resolve DestinationRule from dependsOn FQDN", "host", host)
			continue
		}
		dr := &networkingv1.DestinationRule{}
		err = r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
		if isAbsent(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
		}
		if mitigation.IsOperatorManaged(dr) {
			out = append(out, managedEdge{host: host, dr: dr})
		}
	}
	return out, nil
}

func (r *CascadePolicyReconciler) beginRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := r.listManagedEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		log.Info("No managed DestinationRule to restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseRestoring
	policy.Status.RestoreStep = 0
	log.Info("Entered restoration ramp", "restoreStep", policy.Status.RestoreStep, "edges", len(edges))
	return r.applyRestoreStep(ctx, policy, edges, 0)
}

func (r *CascadePolicyReconciler) advanceRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := r.listManagedEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		log.Info("Managed DestinationRule gone during restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}

	if policy.Status.RestoreStep >= mitigation.RestoreFinalStep {
		if err := r.completeRestore(ctx, policy, edges); err != nil {
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
	return r.applyRestoreStep(ctx, policy, edges, next)
}

func (r *CascadePolicyReconciler) applyRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	edges []managedEdge,
	step int32,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would loosen DestinationRule outlierDetection",
			"restoreStep", step,
			"edges", len(edges),
		)
		return nil
	}
	for _, e := range edges {
		if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(e.dr, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule during restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (r *CascadePolicyReconciler) completeRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	edges []managedEdge,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original outlierDetection and drop annotations",
			"edges", len(edges),
		)
		return nil
	}
	for _, e := range edges {
		if err := mitigation.CompleteLatencyErrorOutlierRestore(e.dr); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule completing restore %s: %w", e.host, err)
		}
	}
	return nil
}
