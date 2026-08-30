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

// Reconcile observes the CR, optionally polls Prometheus per dependsOn host,
// and records a LatencyErrorCascade trip. No Istio patches this slice.
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

	origPhase := policy.Status.Phase
	origSig := policy.Status.LastSignature

	if policy.Status.Phase == "" {
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
	}

	if r.Metrics != nil {
		if host, v, ok := r.detectLatencyErrorCascade(ctx, policy); ok && v.Tripped {
			if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
				now := metav1.Now()
				policy.Status.LastTrippedAt = &now
			}
			policy.Status.Phase = cascadev1alpha1.PolicyPhaseTripped
			policy.Status.LastSignature = cascadev1alpha1.SignatureLatencyErrorCascade
			log.Info("latency/error cascade tripped",
				"dependency", host,
				"confidence", v.Confidence,
				"evidence", v.Evidence,
			)
		}
		// Not tripped: leave Tripped/Restoring alone — restoration is the next slice.
	}

	if policy.Status.Phase != origPhase || policy.Status.LastSignature != origSig {
		if err := r.Status().Update(ctx, policy); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: DefaultRequeueAfter}, nil
}

// detectLatencyErrorCascade evaluates each dependsOn edge. The first tripped
// host wins; query errors skip that edge rather than failing the reconcile.
func (r *CascadePolicyReconciler) detectLatencyErrorCascade(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
) (string, signatures.Verdict, bool) {
	log := logf.FromContext(ctx)
	window := windowOrDefault(policy.Spec.Thresholds.WindowSeconds)
	th := policy.Spec.Thresholds

	for _, host := range policy.Spec.DependsOn {
		latSnap, err := r.Metrics.Query(ctx, latencyP99Query(host, window))
		if err != nil {
			log.Error(err, "p99 latency query failed", "dependency", host)
			continue
		}
		errSnap, err := r.Metrics.Query(ctx, errorRateQuery(host, window))
		if err != nil {
			log.Error(err, "error-rate query failed", "dependency", host)
			continue
		}

		latency, latOK := snapshotMax(latSnap)
		errRate, errOK := snapshotMax(errSnap)
		if !latOK || !errOK {
			log.Info("incomplete metrics for dependency; skipping detector",
				"dependency", host,
				"haveP99", latOK,
				"haveErrorRate", errOK,
			)
			continue
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
		if v.Tripped {
			return host, v, true
		}
	}
	return "", signatures.Verdict{}, false
}

// SetupWithManager sets up the controller with the Manager.
func (r *CascadePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cascadev1alpha1.CascadePolicy{}).
		Named("cascadepolicy").
		Complete(r)
}
