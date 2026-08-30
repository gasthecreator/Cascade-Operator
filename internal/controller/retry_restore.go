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

func (r *CascadePolicyReconciler) beginRestoreRetryStorm(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := r.listManagedVirtualServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		log.Info("No managed VirtualService to restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseRestoring
	policy.Status.RestoreStep = 0
	log.Info("Entered restoration ramp", "restoreStep", policy.Status.RestoreStep, "edges", len(edges))
	return r.applyRetryStormRestoreStep(ctx, policy, edges, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreRetryStorm(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := r.listManagedVirtualServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		log.Info("Managed VirtualService gone during restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}

	if policy.Status.RestoreStep >= mitigation.RestoreFinalStep {
		if err := r.completeRetryStormRestore(ctx, policy, edges); err != nil {
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
	return r.applyRetryStormRestoreStep(ctx, policy, edges, next)
}

func (r *CascadePolicyReconciler) applyRetryStormRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	edges []managedVSEdge,
	step int32,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would ramp VirtualService retries.attempts",
			"restoreStep", step,
			"edges", len(edges),
		)
		return nil
	}
	for _, e := range edges {
		if err := mitigation.ApplyRetryStormRestoreStep(e.vs, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := r.Update(ctx, e.vs); err != nil {
			return fmt.Errorf("update VirtualService during restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (r *CascadePolicyReconciler) completeRetryStormRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	edges []managedVSEdge,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original retries.attempts and drop annotations",
			"edges", len(edges),
		)
		return nil
	}
	for _, e := range edges {
		if err := mitigation.CompleteRetryStormRestore(e.vs); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := r.Update(ctx, e.vs); err != nil {
			return fmt.Errorf("update VirtualService completing restore %s: %w", e.host, err)
		}
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm)).Inc()
	return nil
}
