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
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
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
}

// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.istio.io,resources=destinationrules,verbs=get;list;watch;update;patch

// Reconcile observes the CR, polls Prometheus per dependsOn host, patches
// DestinationRule outlierDetection on a latency/error trip or VirtualService
// retries.attempts on a retry-storm trip, and ramps the matching patch back
// when healthy — restoration dispatches by status.LastSignature (see
// restore.go).
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
			switch sig {
			case cascadev1alpha1.SignatureLatencyErrorCascade:
				mitErr = r.applyLatencyErrorMitigation(ctx, policy, host)
			case cascadev1alpha1.SignatureRetryStorm:
				mitErr = r.applyRetryStormMitigation(ctx, policy, host)
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
// cascade runs first, then retry storm; the first trip wins (status tracks
// one lastSignature). Query errors skip that detector rather than failing the
// reconcile. evaluated is the number of hosts that produced a complete
// reading on at least one detector — restore only advances when this is > 0,
// so a Prometheus outage does not look healthy.
func (r *CascadePolicyReconciler) detectSignatures(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
) (string, signatures.Verdict, cascadev1alpha1.SignatureType, bool, int) {
	window := windowOrDefault(policy.Spec.Thresholds.WindowSeconds)

	evaluated := 0
	for _, host := range policy.Spec.DependsOn {
		latV, latOK := r.evalLatencyError(ctx, policy, host, window)
		if latOK {
			evaluated++
			if latV.Tripped {
				return host, latV, cascadev1alpha1.SignatureLatencyErrorCascade, true, evaluated
			}
		}

		rsV, rsOK := r.evalRetryStorm(ctx, policy, host, window)
		if rsOK {
			if !latOK {
				evaluated++
			}
			if rsV.Tripped {
				return host, rsV, cascadev1alpha1.SignatureRetryStorm, true, evaluated
			}
		}
	}
	return "", signatures.Verdict{}, "", false, evaluated
}

func (r *CascadePolicyReconciler) evalLatencyError(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
	window int32,
) (signatures.Verdict, bool) {
	log := logf.FromContext(ctx)
	th := policy.Spec.Thresholds

	latSnap, err := r.Metrics.Query(ctx, latencyP99Query(host, window))
	if err != nil {
		log.Error(err, "p99 latency query failed", "dependency", host)
		return signatures.Verdict{}, false
	}
	errSnap, err := r.Metrics.Query(ctx, errorRateQuery(host, window))
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
	host string,
	window int32,
) (signatures.Verdict, bool) {
	log := logf.FromContext(ctx)

	snap, err := r.Metrics.Query(ctx, retryStormRatioQuery(host, window))
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
		Multiplier:      policy.Spec.Thresholds.RetryStormMultiplier,
	})
	log.Info("retry storm evaluation",
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
