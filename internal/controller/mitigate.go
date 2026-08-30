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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Shared across every dependsOn object-kind lookup (DestinationRule,
// VirtualService, ...) — the DependencyObjectMissing condition is generic,
// not tied to one Istio kind.
const (
	reasonDependencyObjectNotFound = "DependencyObjectNotFound"
	reasonDependencyObjectFound    = "DependencyObjectFound"
)

// applyLatencyErrorMitigation resolves the DestinationRule for host by
// convention and, in Mitigate mode, patches outlierDetection. Missing objects
// set DependencyObjectMissing and skip the edge; they do not fail Reconcile.
func (r *CascadePolicyReconciler) applyLatencyErrorMitigation(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) error {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		log.Error(err, "cannot resolve DestinationRule from dependsOn FQDN", "host", host)
		setDependencyMissing(policy, fmt.Sprintf("cannot parse dependsOn FQDN %q", host))
		return nil
	}

	dr := &networkingv1.DestinationRule{}
	err = r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
	if isAbsent(err) {
		log.Info("DestinationRule missing; skipping patch", "name", name, "namespace", ns)
		setDependencyMissing(policy, fmt.Sprintf("DestinationRule %s/%s not found for dependsOn %q", ns, name, host))
		return nil
	}
	if err != nil {
		return fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
	}
	clearDependencyMissing(policy)

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would patch DestinationRule outlierDetection",
			"name", name,
			"namespace", ns,
			"consecutive5xxErrors", mitigation.TripConsecutive5xx,
			"interval", mitigation.TripInterval.String(),
			"baseEjectionTime", mitigation.TripBaseEjection.String(),
		)
		return nil
	}

	mitigation.ApplyLatencyErrorOutlierTrip(dr)
	if err := r.Update(ctx, dr); err != nil {
		return fmt.Errorf("update DestinationRule %s/%s: %w", ns, name, err)
	}
	log.Info("patched DestinationRule outlierDetection", "name", name, "namespace", ns)
	return nil
}

func isAbsent(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

func setDependencyMissing(policy *cascadev1alpha1.CascadePolicy, message string) {
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    cascadev1alpha1.ConditionTypeDependencyObjectMissing,
		Status:  metav1.ConditionTrue,
		Reason:  reasonDependencyObjectNotFound,
		Message: message,
	})
}

func clearDependencyMissing(policy *cascadev1alpha1.CascadePolicy) {
	cond := meta.FindStatusCondition(policy.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return
	}
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    cascadev1alpha1.ConditionTypeDependencyObjectMissing,
		Status:  metav1.ConditionFalse,
		Reason:  reasonDependencyObjectFound,
		Message: "Dependency object resolved for the tripped dependency",
	})
}
