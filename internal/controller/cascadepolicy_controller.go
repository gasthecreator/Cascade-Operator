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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

// DefaultRequeueAfter is the reconcile tick used to poll Prometheus (PLAN.md §2.4).
// Watch events still trigger an immediate reconcile.
const DefaultRequeueAfter = 10 * time.Second

// CascadePolicyReconciler reconciles a CascadePolicy object
type CascadePolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Metrics is optional. Nil disables polling (no --prometheus-url). Detectors
	// are not wired this slice; Query will be issued from this tick later.
	Metrics metrics.Querier
}

// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cascade.gideonsanni.dev,resources=cascadepolicies/finalizers,verbs=update

// Reconcile is the CascadePolicy loop. It observes the CR and requeues on
// DefaultRequeueAfter. Prometheus Query is not issued yet — the next slice
// wires metrics → detectors on this same tick. No Istio patches yet.
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

	if policy.Status.Phase == "" {
		policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
		if err := r.Status().Update(ctx, policy); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: DefaultRequeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CascadePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cascadev1alpha1.CascadePolicy{}).
		Named("cascadepolicy").
		Complete(r)
}
