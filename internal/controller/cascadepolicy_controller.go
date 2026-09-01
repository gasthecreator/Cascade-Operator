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
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mesh"
	istiomesh "github.com/gasthecreator/Cascade-Operator/internal/mesh/istio"
	linkerdmesh "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
	"github.com/gasthecreator/Cascade-Operator/internal/notify"
	"github.com/gasthecreator/Cascade-Operator/internal/signatures"
)

// DefaultRequeueAfter is the reconcile tick used to poll Prometheus (PLAN.md §2.4).
// Watch events still trigger an immediate reconcile.
const DefaultRequeueAfter = 10 * time.Second

// CascadePolicyReconciler reconciles a CascadePolicy object
type CascadePolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Metrics is optional. Nil disables polling (no --prometheus-url).
	Metrics metrics.Querier
	// Notify is optional. Nil disables trip/restore notifications (no
	// --notify-webhook-url). A failure to notify is logged, never
	// propagated as a reconcile error — same reasoning as Metrics being
	// nil-able: an optional dependency, not a required one.
	Notify notify.Notifier
	// QueryBuilder overrides mesh selection entirely when set (every
	// existing test that injects a fakeQuerier alongside a specific mesh's
	// query shapes relies on this) — leave nil in production so each
	// policy's own spec.mesh picks the implementation via queryBuilder()
	// below (PLAN.md §5 Phase 6.6). Unlike Metrics/Notify this is not
	// optional infrastructure — detection cannot run without it — so
	// queryBuilder() always returns something even when this field and
	// spec.mesh are both unset (Istio, spec.mesh's own default).
	QueryBuilder mesh.QueryBuilder
	// Mitigator overrides mesh selection entirely when set — same
	// override-vs-per-policy-dispatch relationship as QueryBuilder, see
	// mitigator() below.
	Mitigator mesh.Mitigator
}

// queryBuilder returns r.QueryBuilder when set (a full override, used by
// tests), otherwise dispatches on policy.Spec.Mesh (PLAN.md §5 Phase
// 6.6) — Istio for MeshIstio or the unset zero value (spec.mesh's own
// +kubebuilder:default=Istio), Linkerd for MeshLinkerd. See the
// QueryBuilder field's own doc comment for why an explicit override takes
// priority over the policy's own choice rather than the reverse.
func (r *CascadePolicyReconciler) queryBuilder(policy *cascadev1alpha1.CascadePolicy) mesh.QueryBuilder {
	if r.QueryBuilder != nil {
		return r.QueryBuilder
	}
	if policy.Spec.Mesh == cascadev1alpha1.MeshLinkerd {
		return linkerdmesh.QueryBuilder{}
	}
	return istiomesh.QueryBuilder{}
}

// mitigator returns r.Mitigator when set (a full override, used by
// tests), otherwise dispatches on policy.Spec.Mesh, each built fresh
// around this reconciler's own client — same dispatch as queryBuilder.
func (r *CascadePolicyReconciler) mitigator(policy *cascadev1alpha1.CascadePolicy) mesh.Mitigator {
	if r.Mitigator != nil {
		return r.Mitigator
	}
	if policy.Spec.Mesh == cascadev1alpha1.MeshLinkerd {
		return linkerdmesh.NewMitigator(r.Client)
	}
	return istiomesh.NewMitigator(r.Client)
}

// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.istio.io,resources=destinationrules,verbs=get;list;watch;update;patch

// Reconcile observes the CR, polls Prometheus per dependsOn host, and patches
// DestinationRule outlierDetection on a latency/error trip, VirtualService
// retries.attempts on a retry-storm trip, or DestinationRule
// connectionPool.http on a fan-out trip — then ramps the matching patch back
// when healthy. Restoration dispatches by status.LastSignature (see
// restore.go). Latency/error and fan-out both patch DestinationRule, but
// disjoint field sets (outlierDetection vs. connectionPool.http); see
// fanout_restore.go's doc comment for why sharing that object kind is safe.
// If a same-host signature handoff happens mid-Tripped/Restoring — the
// outgoing signature's condition clears the same tick the incoming one
// trips, no healthy tick in between — the outgoing signature's restore is
// force-completed synchronously before the incoming trip is applied (see
// forceCompleteOutgoingRestore, restore.go).
func (r *CascadePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	policy := &cascadev1alpha1.CascadePolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("reconciling CascadePolicy",
		"generation", policy.Generation,
		"mode", policy.Spec.Mode,
		"service", policy.Spec.Service,
	)

	origStatus := policy.Status.DeepCopy()

	if policy.Status.Phase == "" {
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
	}

	var mitErr error
	if r.Metrics != nil {
		host, v, sig, tripped, evaluated := r.detectSignatures(ctx, policy)
		if tripped {
			// Same-object signature handoff (PROPOSALS.md, approved
			// 2026-08-30): if a different signature was already
			// Tripped/Restoring on this policy, force its restore to true
			// completion *before* adopting the new one, so its fields and
			// its own original-* annotation are never left orphaned. Read
			// the outgoing signature before it gets overwritten below. A
			// fresh CR (outgoingSig == "") or a same-signature re-trip
			// (outgoingSig == sig) needs none of this; Phase == Normal
			// covers both "first trip ever" and "already fully restored,
			// nothing left to complete".
			outgoingSig := policy.Status.LastSignature
			handoff := outgoingSig != "" && outgoingSig != sig && policy.Status.Phase != cascadev1alpha1.PolicyPhaseNormal
			// A regression is the same signature re-tripping while its own
			// restoration ramp is still in progress (PLAN.md §2.6: "a
			// regression during ramp re-trips immediately and resets to
			// step 0") — distinct from handoff, which is a *different*
			// signature tripping. Read before Phase is overwritten below.
			regression := !handoff && outgoingSig == sig && policy.Status.Phase == cascadev1alpha1.PolicyPhaseRestoring
			if handoff {
				mitErr = r.forceCompleteOutgoingRestore(ctx, policy, outgoingSig)
			}
			if mitErr == nil {
				signaturesDetectedTotal.WithLabelValues(string(sig), host).Inc()
				if regression {
					restorationRegressionsTotal.WithLabelValues(string(sig)).Inc()
				}
				if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
					now := metav1.Now()
					policy.Status.LastTrippedAt = &now
				}
				policy.Status.Phase = cascadev1alpha1.PolicyPhaseTripped
				policy.Status.RestoreStep = 0
				policy.Status.LastSignature = sig
				log.Info("cascade signature tripped",
					"signature", sig,
					"dependency", host,
					"confidence", v.Confidence,
					"evidence", v.Evidence,
				)
				r.notifyTrip(ctx, policy, sig, host, v)
				switch sig {
				case cascadev1alpha1.SignatureLatencyErrorCascade:
					mitErr = r.applyLatencyErrorMitigation(ctx, policy, host)
				case cascadev1alpha1.SignatureRetryStorm:
					mitErr = r.applyRetryStormMitigation(ctx, policy, host)
				case cascadev1alpha1.SignatureFanOutAmplification:
					mitErr = r.applyFanOutMitigation(ctx, policy, host)
				}
			}
		} else if evaluated > 0 {
			switch policy.Status.Phase {
			case cascadev1alpha1.PolicyPhaseTripped:
				mitErr = r.beginRestore(ctx, policy)
			case cascadev1alpha1.PolicyPhaseRestoring:
				mitErr = r.advanceRestore(ctx, policy)
			}
		}
	}

	if !equality.Semantic.DeepEqual(origStatus, &policy.Status) {
		if err := r.Status().Update(ctx, policy); err != nil {
			return ctrl.Result{}, err
		}
	}
	if mitErr != nil {
		return ctrl.Result{}, mitErr
	}

	return ctrl.Result{RequeueAfter: DefaultRequeueAfter}, nil
}

// detectSignatures evaluates each dependsOn edge. Per host, latency/error
// cascade runs first, then retry storm, then fan-out amplification; the
// first trip wins (status tracks one lastSignature). Query errors skip that
// detector rather than failing the reconcile. evaluated is the number of
// hosts that produced a complete reading on at least one detector — restore
// only advances when this is > 0, so a Prometheus outage does not look
// healthy.
func (r *CascadePolicyReconciler) detectSignatures(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
) (string, signatures.Verdict, cascadev1alpha1.SignatureType, bool, int) {
	evaluated := 0
	for _, host := range policy.Spec.DependsOn {
		th := cascadev1alpha1.EffectiveThresholds(policy, host)
		window := windowOrDefault(th.WindowSeconds)

		latV, latOK := r.evalLatencyError(ctx, policy, th, host, window)
		if latOK {
			evaluated++
			if latV.Tripped {
				return host, latV, cascadev1alpha1.SignatureLatencyErrorCascade, true, evaluated
			}
		}

		rsV, rsOK := r.evalRetryStorm(ctx, policy, th, host, window)
		if rsOK {
			if !latOK {
				evaluated++
			}
			if rsV.Tripped {
				return host, rsV, cascadev1alpha1.SignatureRetryStorm, true, evaluated
			}
		}

		foV, foOK := r.evalFanOut(ctx, policy, th, host, window)
		if foOK {
			if !latOK && !rsOK {
				evaluated++
			}
			if foV.Tripped {
				return host, foV, cascadev1alpha1.SignatureFanOutAmplification, true, evaluated
			}
		}
	}
	return "", signatures.Verdict{}, "", false, evaluated
}

func (r *CascadePolicyReconciler) evalLatencyError(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	th cascadev1alpha1.Thresholds,
	host string,
	window int32,
) (signatures.Verdict, bool) {
	log := logf.FromContext(ctx)

	latSnap, err := r.Metrics.Query(ctx, r.queryBuilder(policy).LatencyP99Query(host, window))
	if err != nil {
		log.Error(err, "p99 latency query failed", "dependency", host)
		return signatures.Verdict{}, false
	}
	errSnap, err := r.Metrics.Query(ctx, r.queryBuilder(policy).ErrorRateQuery(host, window))
	if err != nil {
		log.Error(err, "error-rate query failed", "dependency", host)
		return signatures.Verdict{}, false
	}

	latency, latOK := snapshotMax(latSnap)
	errRate, errOK := snapshotMax(errSnap)
	if !latOK || !errOK {
		log.Info("incomplete metrics for dependency; skipping latency/error detector",
			"dependency", host,
			"haveP99", latOK,
			"haveErrorRate", errOK,
		)
		return signatures.Verdict{}, false
	}

	v := signatures.DetectLatencyError(signatures.LatencyErrorInput{
		Dependency:         host,
		LatencyP99Ms:       latency,
		ErrorRateFraction:  errRate,
		LatencyThresholdMs: float64(th.LatencyP99Ms),
		ErrorRateThreshold: th.ErrorRateFraction,
	})
	log.Info("latency/error cascade evaluation",
		"dependency", host,
		"tripped", v.Tripped,
		"confidence", v.Confidence,
		"evidence", v.Evidence,
	)
	return v, true
}

func (r *CascadePolicyReconciler) evalRetryStorm(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	th cascadev1alpha1.Thresholds,
	host string,
	window int32,
) (signatures.Verdict, bool) {
	log := logf.FromContext(ctx)

	snap, err := r.Metrics.Query(ctx, r.queryBuilder(policy).RetryStormRatioQuery(host, window))
	if err != nil {
		log.Error(err, "retry-storm ratio query failed", "dependency", host)
		return signatures.Verdict{}, false
	}
	ratio, ok := snapshotMax(snap)
	if !ok {
		log.Info("incomplete metrics for dependency; skipping retry-storm detector",
			"dependency", host,
		)
		return signatures.Verdict{}, false
	}

	v := signatures.DetectRetryStorm(signatures.RetryStormInput{
		Dependency:      host,
		DestSourceRatio: ratio,
		Multiplier:      th.RetryStormMultiplier,
	})
	log.Info("retry storm evaluation",
		"dependency", host,
		"tripped", v.Tripped,
		"confidence", v.Confidence,
		"evidence", v.Evidence,
	)
	return v, true
}

func (r *CascadePolicyReconciler) evalFanOut(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	th cascadev1alpha1.Thresholds,
	host string,
	window int32,
) (signatures.Verdict, bool) {
	log := logf.FromContext(ctx)

	snap, err := r.Metrics.Query(ctx, r.queryBuilder(policy).FanOutRatioQuery(host, policy.Spec.Service, window))
	if err != nil {
		log.Error(err, "fan-out ratio query failed", "dependency", host)
		return signatures.Verdict{}, false
	}
	ratio, ok := snapshotMax(snap)
	if !ok {
		log.Info("incomplete metrics for dependency; skipping fan-out detector",
			"dependency", host,
		)
		return signatures.Verdict{}, false
	}

	v := signatures.DetectFanOut(signatures.FanOutInput{
		Dependency:            host,
		DependencyCallerRatio: ratio,
		Multiplier:            th.FanOutMultiplier,
	})
	log.Info("fan-out evaluation",
		"dependency", host,
		"tripped", v.Tripped,
		"confidence", v.Confidence,
		"evidence", v.Evidence,
	)
	return v, true
}

// SetupWithManager sets up the controller with the Manager.
func (r *CascadePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cascadev1alpha1.CascadePolicy{}).
		Named("cascadepolicy").
		Complete(r)
}
