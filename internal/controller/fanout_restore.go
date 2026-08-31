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

// Fan-out's restore path deliberately reuses listManagedDestinationRuleEdges
// and managedDREdge — the exact same DestinationRule-listing helper
// latency/error-cascade's restore path uses (restore.go) — instead of a
// separate "listManagedFanOutDestinationRuleEdges". This is the
// shared-object-kind subtlety flagged for this slice, reasoned through
// here rather than assumed:
//
//   - listManagedDestinationRuleEdges only checks the managed-by annotation
//     (mitigation.IsOperatorManaged), which does not — and cannot — say
//     *which* signature patched a given DestinationRule's fields. Both
//     latency/error-cascade (outlierDetection) and fan-out
//     (connectionPool.http) can set managed-by on the same object, so this
//     helper will return a DestinationRule fan-out patched just as readily
//     as one latency/error patched.
//   - That is safe here because restoration dispatch (beginRestore/
//     advanceRestore below) is keyed by status.LastSignature, and the CRD
//     tracks exactly one active signature per policy. Whichever path runs
//     this tick — applyFanOutRestoreStep/completeFanOutRestore vs. the
//     latency/error equivalents in restore.go — only ever reads and writes
//     its own field set (connectionPool.http here; outlierDetection there)
//     and its own annotation (original-connection-pool vs.
//     original-outlier-detection). Listing the same object twice from two
//     different call sites is harmless when neither call site's
//     read-modify-write ever touches the other's fields.
//   - The remaining edge is a real one, not fully closed by the above: if a
//     policy's detected signature *changes* on the same host — say
//     latency/error trips, mitigates, and is mid-Restoring when a later
//     tick finds fan-out now tripping on that same host instead — Reconcile
//     (cascadepolicy_controller.go) adopts the new signature immediately,
//     without first driving the outgoing signature's restore ramp to
//     completion. Each trip function's own-annotation-presence check (see
//     ApplyLatencyErrorOutlierTrip's and ApplyFanOutConnectionPoolTrip's doc
//     comments) ensures the *newly* tripping signature still correctly
//     captures its own baseline in this case — that part is fixed and
//     tested (TestApplyFanOutConnectionPoolTripLeavesOutlierDetectionAlone
//     in the mitigation package). What is not yet handled is the outgoing
//     signature's own state: its trip-time field values and its own
//     original-* annotation are left exactly as they were, with no future
//     reconcile tick ever pointed back at restoring or cleaning them up,
//     since LastSignature has already moved on. Filed as a PROPOSALS.md
//     entry rather than silently patched here, since resolving it well
//     likely needs either a status-shape change (tracking more than one
//     active signature) or a policy decision (force a full restore of the
//     outgoing signature before adopting a new one on the same object) —
//     both bigger than this slice's stated scope.
func (r *CascadePolicyReconciler) beginRestoreFanOut(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := r.listManagedDestinationRuleEdges(ctx, policy)
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
	return r.applyFanOutRestoreStep(ctx, policy, edges, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreFanOut(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := r.listManagedDestinationRuleEdges(ctx, policy)
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
		if err := r.completeFanOutRestore(ctx, policy, edges); err != nil {
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
	return r.applyFanOutRestoreStep(ctx, policy, edges, next)
}

func (r *CascadePolicyReconciler) applyFanOutRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	edges []managedDREdge,
	step int32,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would loosen DestinationRule connectionPool.http",
			"restoreStep", step,
			"edges", len(edges),
		)
		return nil
	}
	for _, e := range edges {
		if err := mitigation.ApplyFanOutConnectionPoolRestoreStep(e.dr, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule during restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (r *CascadePolicyReconciler) completeFanOutRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	edges []managedDREdge,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original connectionPool.http and drop annotations",
			"edges", len(edges),
		)
		return nil
	}
	for _, e := range edges {
		if err := mitigation.CompleteFanOutConnectionPoolRestore(e.dr); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule completing restore %s: %w", e.host, err)
		}
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification)).Inc()
	return nil
}
