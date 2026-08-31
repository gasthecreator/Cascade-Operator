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

type managedDREdge struct {
	host string
	dr   *networkingv1.DestinationRule
}

// listManagedDestinationRuleEdges resolves each dependsOn FQDN and returns
// DestinationRules this operator has already annotated. Missing objects are
// skipped; they do not fail the reconcile (same spirit as a missing trip
// target).
func (r *CascadePolicyReconciler) listManagedDestinationRuleEdges(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
) ([]managedDREdge, error) {
	log := logf.FromContext(ctx)
	var out []managedDREdge
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
			out = append(out, managedDREdge{host: host, dr: dr})
		}
	}
	return out, nil
}

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
		drEdges, err := r.listManagedDestinationRuleEdges(ctx, policy)
		if err != nil {
			return err
		}
		vsEdges, err := r.listManagedVirtualServiceEdges(ctx, policy)
		if err != nil {
			return err
		}
		if len(drEdges) == 0 && len(vsEdges) == 0 {
			return nil
		}
		log.Info("Signature handoff: force-completing outgoing restore", "outgoing", outgoing, "drEdges", len(drEdges), "vsEdges", len(vsEdges))
		return r.completeLatencyErrorRestore(ctx, policy, drEdges, vsEdges)
	case cascadev1alpha1.SignatureRetryStorm:
		// Both object kinds this signature manages (VirtualService
		// primary, DestinationRule secondary) must be force-completed
		// together — an incoming signature adopting this host's
		// DestinationRule (e.g. fan-out or latency/error-cascade) must
		// never find retry storm's own MaxRetries still at the trip
		// value, and the reverse (an incoming signature adopting the
		// VirtualService) must never find retries.attempts still at 0.
		vsEdges, err := r.listManagedVirtualServiceEdges(ctx, policy)
		if err != nil {
			return err
		}
		drEdges, err := r.listManagedDestinationRuleEdges(ctx, policy)
		if err != nil {
			return err
		}
		if len(vsEdges) == 0 && len(drEdges) == 0 {
			return nil
		}
		log.Info("Signature handoff: force-completing outgoing restore", "outgoing", outgoing, "vsEdges", len(vsEdges), "drEdges", len(drEdges))
		return r.completeRetryStormRestore(ctx, policy, vsEdges, drEdges)
	case cascadev1alpha1.SignatureFanOutAmplification:
		edges, err := r.listManagedDestinationRuleEdges(ctx, policy)
		if err != nil {
			return err
		}
		if len(edges) == 0 {
			return nil
		}
		log.Info("Signature handoff: force-completing outgoing restore", "outgoing", outgoing, "edges", len(edges))
		return r.completeFanOutRestore(ctx, policy, edges)
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
// applyLatencyErrorRestoreStep, and completeLatencyErrorRestore all gather
// and act on *both* object kinds this signature now manages — the
// DestinationRule primary (outlierDetection) and the VirtualService
// secondary (route timeout, PLAN.md §2.6) — independently of each other,
// mirroring applyLatencyErrorMitigation's own independence: an edge with
// only one of the two objects managed still restores that one correctly, an
// edge with both restores both, and "nothing managed at all" (both lists
// empty) is the only case that snaps straight to Normal. A single
// restorationsCompletedTotal increment covers a completion touching either
// or both object kinds — "this signature's restoration completed" is one
// event per episode, not one per object kind.
func (r *CascadePolicyReconciler) beginRestoreLatencyError(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	drEdges, err := r.listManagedDestinationRuleEdges(ctx, policy)
	if err != nil {
		return err
	}
	vsEdges, err := r.listManagedVirtualServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(drEdges) == 0 && len(vsEdges) == 0 {
		log.Info("No managed DestinationRule or VirtualService to restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseRestoring
	policy.Status.RestoreStep = 0
	log.Info("Entered restoration ramp", "restoreStep", policy.Status.RestoreStep, "drEdges", len(drEdges), "vsEdges", len(vsEdges))
	return r.applyLatencyErrorRestoreStep(ctx, policy, drEdges, vsEdges, 0)
}

func (r *CascadePolicyReconciler) advanceRestoreLatencyError(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	drEdges, err := r.listManagedDestinationRuleEdges(ctx, policy)
	if err != nil {
		return err
	}
	vsEdges, err := r.listManagedVirtualServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	if len(drEdges) == 0 && len(vsEdges) == 0 {
		log.Info("Managed DestinationRule and VirtualService both gone during restore; returning to Normal")
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		policy.Status.RestoreStep = 0
		return nil
	}

	if policy.Status.RestoreStep >= mitigation.RestoreFinalStep {
		if err := r.completeLatencyErrorRestore(ctx, policy, drEdges, vsEdges); err != nil {
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
	return r.applyLatencyErrorRestoreStep(ctx, policy, drEdges, vsEdges, next)
}

func (r *CascadePolicyReconciler) applyLatencyErrorRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	drEdges []managedDREdge,
	vsEdges []managedVSEdge,
	step int32,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would loosen DestinationRule outlierDetection and VirtualService timeout",
			"restoreStep", step,
			"drEdges", len(drEdges),
			"vsEdges", len(vsEdges),
		)
		return nil
	}
	for _, e := range drEdges {
		if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(e.dr, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule during restore %s: %w", e.host, err)
		}
	}
	for _, e := range vsEdges {
		if err := mitigation.ApplyLatencyErrorTimeoutRestoreStep(e.vs, step, policy.Spec.Thresholds.LatencyP99Ms); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := r.Update(ctx, e.vs); err != nil {
			return fmt.Errorf("update VirtualService during restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (r *CascadePolicyReconciler) completeLatencyErrorRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	drEdges []managedDREdge,
	vsEdges []managedVSEdge,
) error {
	log := logf.FromContext(ctx)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original outlierDetection/timeout and drop annotations",
			"drEdges", len(drEdges),
			"vsEdges", len(vsEdges),
		)
		return nil
	}
	for _, e := range drEdges {
		if err := mitigation.CompleteLatencyErrorOutlierRestore(e.dr); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := r.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule completing restore %s: %w", e.host, err)
		}
	}
	for _, e := range vsEdges {
		if err := mitigation.CompleteLatencyErrorTimeoutRestore(e.vs); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := r.Update(ctx, e.vs); err != nil {
			return fmt.Errorf("update VirtualService completing restore %s: %w", e.host, err)
		}
	}
	restorationsCompletedTotal.WithLabelValues(string(cascadev1alpha1.SignatureLatencyErrorCascade)).Inc()
	r.notifyRestore(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
	return nil
}
