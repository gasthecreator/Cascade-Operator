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

// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch

// applyRetryStormMitigation resolves the VirtualService for host by
// convention and, in Mitigate mode, cuts retries.attempts on every
// forwarding route. Missing objects set DependencyObjectMissing and skip the
// edge; they do not fail Reconcile. Same read-resolve-patch shape as
// applyLatencyErrorMitigation, one object kind over. Called from Reconcile
// on a retry-storm trip alongside VirtualService-aware restoration
// (restore.go/retry_restore.go) — the two landed together deliberately so a
// live patch is never left without a way back (see the retry-storm
// mitigation and restoration worklogs).
func (r *CascadePolicyReconciler) applyRetryStormMitigation(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) error {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		log.Error(err, "cannot resolve VirtualService from dependsOn FQDN", "host", host)
		setDependencyMissing(policy, fmt.Sprintf("cannot parse dependsOn FQDN %q", host))
		return nil
	}

	vs := &networkingv1.VirtualService{}
	err = r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)
	if isAbsent(err) {
		log.Info("VirtualService missing; skipping patch", "name", name, "namespace", ns)
		setDependencyMissing(policy, fmt.Sprintf("VirtualService %s/%s not found for dependsOn %q", ns, name, host))
		return nil
	}
	if err != nil {
		return fmt.Errorf("get VirtualService %s/%s: %w", ns, name, err)
	}
	clearDependencyMissing(policy)

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cut VirtualService retries.attempts",
			"name", name,
			"namespace", ns,
			"attempts", mitigation.TripRetryAttempts,
		)
		return nil
	}

	mitigation.ApplyRetryStormTrip(vs)
	if err := r.Update(ctx, vs); err != nil {
		return fmt.Errorf("update VirtualService %s/%s: %w", ns, name, err)
	}
	log.Info("patched VirtualService retries.attempts", "name", name, "namespace", ns)
	return nil
}
