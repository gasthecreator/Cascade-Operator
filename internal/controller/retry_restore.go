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

// beginRestoreRetryStorm, advanceRestoreRetryStorm,
// applyRetryStormRestoreStep, and completeRetryStormRestore delegate to
// r.mitigator(policy) (PLAN.md §5 Phase 6.5 — the last signature migrated) for
// the actual object mutation across *both* object kinds this signature
// manages — the VirtualService primary (retries.attempts) and the
// DestinationRule secondary (connectionPool.http maxRetries, PLAN.md
// §2.6) — independently of each other, mirroring applyRetryStormMitigation's
// own independence and the exact shape latency/error-cascade's own
// migration (restore.go) already established. A single
// restorationsCompletedTotal increment covers a completion touching
// either or both object kinds.
func (r *CascadePolicyReconciler) beginRestoreRetryStorm(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	has, err := r.mitigator(policy).HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureRetryStorm)
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
	return r.applyRetryStormRestoreStep(ctx, policy, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreRetryStorm(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	has, err := r.mitigator(policy).HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureRetryStorm)
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
		if err := r.completeRetryStormRestore(ctx, policy); err != nil {
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
	return r.applyRetryStormRestoreStep(ctx, policy, next)
}

func (r *CascadePolicyReconciler) applyRetryStormRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, step int32) error {
	if err := r.mitigator(policy).ApplyRestoreStep(ctx, policy, cascadev1alpha1.SignatureRetryStorm, step); err != nil {
		return fmt.Errorf("apply retry-storm restore step %d: %w", step, err)
	}
	return nil
}

func (r *CascadePolicyReconciler) completeRetryStormRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	if err := r.mitigator(policy).CompleteRestore(ctx, policy, cascadev1alpha1.SignatureRetryStorm); err != nil {
		return fmt.Errorf("complete retry-storm restore: %w", err)
	}
	// DetectOnly never counts/notifies a completion — matches the
	// pre-migration code's own early return before reaching either call
	// (see fanout_restore.go's completeFanOutRestore for the identical
	// reasoning, caught there first).
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		return nil
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm)).Inc()
	r.notifyRestore(ctx, policy, cascadev1alpha1.SignatureRetryStorm)
	return nil
}
