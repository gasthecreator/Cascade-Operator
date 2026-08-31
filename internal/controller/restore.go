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

// beginRestore and advanceRestore dispatch by status.LastSignature, the
// same field Reconcile's own detectSignatures/mitigation switch already
// keys off — the CRD tracks exactly one active signature at a time, so this
// mirrors that switch instead of building a generic "any Istio object"
// restore abstraction for what is currently three cases (two object kinds:
// latency/error-cascade and fan-out both restore a DestinationRule, on
// disjoint field sets — see fanout_restore.go's doc comment for the
// shared-object-kind reasoning). A signature with no
// restore path wired yet falls back to snapToNormalNoRestore rather than
// getting stuck: that was retry storm's own situation between its
// mitigation slice (mitigation built, not called from Reconcile) and this
// one (mitigation called, restoration wired in the same change) — applying
// the same fail-safe going forward means a future signature can be wired
// into Reconcile's mitigation dispatch ahead of its restore logic without
// ever leaving a live patch stuck.
func (r *CascadePolicyReconciler) beginRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	switch policy.Status.LastSignature {
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return r.beginRestoreLatencyError(ctx, policy)
	case cascadev1alpha1.SignatureRetryStorm:
		return r.beginRestoreRetryStorm(ctx, policy)
	case cascadev1alpha1.SignatureFanOutAmplification:
		return r.beginRestoreFanOut(ctx, policy)
	default:
		return r.snapToNormalNoRestore(ctx, policy)
	}
}

func (r *CascadePolicyReconciler) advanceRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	switch policy.Status.LastSignature {
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return r.advanceRestoreLatencyError(ctx, policy)
	case cascadev1alpha1.SignatureRetryStorm:
		return r.advanceRestoreRetryStorm(ctx, policy)
	case cascadev1alpha1.SignatureFanOutAmplification:
		return r.advanceRestoreFanOut(ctx, policy)
	default:
		return r.snapToNormalNoRestore(ctx, policy)
	}
}

// forceCompleteOutgoingRestore is called from Reconcile's trip branch when
// the signature that just tripped (sig) differs from status.LastSignature
// as it stood *before* this tick (outgoing), and Phase != Normal — i.e. a
// same-object signature handoff mid-Tripped/Restoring, with no intervening
// healthy tick. See PROPOSALS.md's "Signature handoff on a shared
// DestinationRule can orphan the outgoing signature's fields" (approved
// 2026-08-30) and PLAN.md §2.6's "Signature handoff on a shared object"
// note for the full reasoning; this is that resolved direction, not a new
// one.
//
// It synchronously drives the *outgoing* signature's restore straight to
// true completion — the same complete*Restore function its own gradual
// ramp already calls at its final step — before Reconcile applies the
// incoming signature's trip. This is a new call site for existing, already
// -tested logic ("restore to true original, strip both annotations"), not
// new mitigation-package code. It is safe to skip the gradual ramp's
// per-step regression check here specifically because the caller
// (Reconcile) just confirmed, this same tick, that the outgoing signature's
// own detector no longer trips on this policy: there is no in-progress
// ramp to protect from a regression, only a stale object state that must
// be cleared before the incoming signature's trip touches the same object.
//
// Mirrors beginRestore/advanceRestore's dispatch shape, keyed on the
// outgoing signature rather than the current one, but calls straight
// through to each path's complete function instead of its step-by-step
// one. A signature with no restore path (including the empty string, which
// covers "no prior trip" — Reconcile's caller already guards on that) is a
// no-op, same spirit as snapToNormalNoRestore.
func (r *CascadePolicyReconciler) forceCompleteOutgoingRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	outgoing cascadev1alpha1.SignatureType,
) error {
	log := logf.FromContext(ctx)
	switch outgoing {
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		// Both object kinds this signature manages (DestinationRule primary,
		// VirtualService secondary) must be force-completed together — an
		// incoming signature adopting this host's DestinationRule (e.g.
		// fan-out) must never find a lingering timeout on its VirtualService
		// either, since the whole point of force-complete is leaving a
		// clean, fully-original object state behind for whatever claims it
		// next.
		has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
		if err != nil {
			return err
		}
		if !has {
			return nil
		}
		log.Info("Signature handoff: force-completing outgoing restore", "outgoing", outgoing)
		return r.completeLatencyErrorRestore(ctx, policy)
	case cascadev1alpha1.SignatureRetryStorm:
		// Both object kinds this signature manages (VirtualService
		// primary, DestinationRule secondary) must be force-completed
		// together — an incoming signature adopting this host's
		// DestinationRule (e.g. fan-out or latency/error-cascade) must
		// never find retry storm's own MaxRetries still at the trip
		// value, and the reverse (an incoming signature adopting the
		// VirtualService) must never find retries.attempts still at 0.
		has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureRetryStorm)
		if err != nil {
			return err
		}
		if !has {
			return nil
		}
		log.Info("Signature handoff: force-completing outgoing restore", "outgoing", outgoing)
		return r.completeRetryStormRestore(ctx, policy)
	case cascadev1alpha1.SignatureFanOutAmplification:
		has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification)
		if err != nil {
			return err
		}
		if !has {
			return nil
		}
		log.Info("Signature handoff: force-completing outgoing restore", "outgoing", outgoing)
		return r.completeFanOutRestore(ctx, policy)
	default:
		return nil
	}
}

// snapToNormalNoRestore is the fail-safe fallback for a signature with no
// restore path — same shape as each path's own "zero managed edges" branch,
// generalized so an unwired or unrecognized signature never gets stuck at
// Restoring.
func (r *CascadePolicyReconciler) snapToNormalNoRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	log.Info("No restoration path for signature; returning to Normal",
		"lastSignature", policy.Status.LastSignature,
	)
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
	policy.Status.RestoreStep = 0
	return nil
}

// beginRestoreLatencyError, advanceRestoreLatencyError,
// applyLatencyErrorRestoreStep, and completeLatencyErrorRestore delegate to
// r.mitigator() (PLAN.md §5 Phase 6.4) for the actual object mutation
// across *both* object kinds this signature manages — the DestinationRule
// primary (outlierDetection) and the VirtualService secondary (route
// timeout, PLAN.md §2.6) — independently of each other, mirroring
// applyLatencyErrorMitigation's own independence. A single
// restorationsCompletedTotal increment covers a completion touching either
// or both object kinds — "this signature's restoration completed" is one
// event per episode, not one per object kind.
func (r *CascadePolicyReconciler) beginRestoreLatencyError(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
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
	return r.applyLatencyErrorRestoreStep(ctx, policy, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreLatencyError(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	has, err := r.mitigator().HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
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
		if err := r.completeLatencyErrorRestore(ctx, policy); err != nil {
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
	return r.applyLatencyErrorRestoreStep(ctx, policy, next)
}

func (r *CascadePolicyReconciler) applyLatencyErrorRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, step int32) error {
	if err := r.mitigator().ApplyRestoreStep(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade, step); err != nil {
		return fmt.Errorf("apply latency/error restore step %d: %w", step, err)
	}
	return nil
}

func (r *CascadePolicyReconciler) completeLatencyErrorRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	if err := r.mitigator().CompleteRestore(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade); err != nil {
		return fmt.Errorf("complete latency/error restore: %w", err)
	}
	// DetectOnly never counts/notifies a completion — matches the
	// pre-migration code's own early return before reaching either call
	// (see fanout_restore.go's completeFanOutRestore for the identical
	// reasoning, caught there first).
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		return nil
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)).Inc()
	r.notifyRestore(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
	return nil
}
