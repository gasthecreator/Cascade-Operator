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

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Migrated to mesh.Mitigator (PLAN.md §5 Phase 6.3): begin/advance/
// completeFanOutRestore below now delegate the actual object mutation to
// r.mitigator(), keeping only the mesh-agnostic status.Phase/RestoreStep
// transitions, metrics, and notification here — same external shape as
// before the migration (mirrors restore.go's beginRestoreLatencyError/
// advanceRestoreLatencyError, still unmigrated), just no longer calling
// internal/mitigation or r.Get/r.Update directly. The Istio Mitigator's own
// edge-listing (internal/mesh/istio/mitigator.go's listFanOutEdges) is a
// temporary, intentional duplicate of this file's own former
// listManagedDestinationRuleEdges call — see that function's doc comment
// for why.
func (r *CascadePolicyReconciler) beginRestoreFanOut(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification)
	if err != nil {
		return err
	}
	if !has {
		log.Info("No managed edges to restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseRestoring
	policy.Status.RestoreStep = 0
	log.Info("Entered restoration ramp", "restoreStep", policy.Status.RestoreStep)
	return r.applyFanOutRestoreStep(ctx, policy, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreFanOut(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification)
	if err != nil {
		return err
	}
	if !has {
		log.Info("Managed edges gone during restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}

	if policy.Status.RestoreStep >= mitigation.RestoreFinalStep {
		if err := r.completeFanOutRestore(ctx, policy); err != nil {
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
	return r.applyFanOutRestoreStep(ctx, policy, next)
}

func (r *CascadePolicyReconciler) applyFanOutRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	step int32,
) error {
	if err := r.mitigator().ApplyRestoreStep(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification, step); err != nil {
		return fmt.Errorf("apply fan-out restore step %d: %w", step, err)
	}
	return nil
}

func (r *CascadePolicyReconciler) completeFanOutRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	if err := r.mitigator().CompleteRestore(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification); err != nil {
		return fmt.Errorf("complete fan-out restore: %w", err)
	}
	// DetectOnly never counts/notifies a completion — matches the
	// pre-migration code's own early return before reaching either call.
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		return nil
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification)).Inc()
	r.notifyRestore(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification)
	return nil
}
