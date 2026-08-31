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

// applyFanOutMitigation resolves the DestinationRule for host by convention
// and, in Mitigate mode, caps connectionPool.http (a bulkhead). Missing
// objects set DependencyObjectMissing and skip the edge; they do not fail
// Reconcile. Same read-resolve-patch shape as applyLatencyErrorMitigation,
// same object kind (DestinationRule) but a disjoint field set
// (connectionPool.http vs. outlierDetection) — see fanout_restore.go's doc
// comment for why sharing that object kind with latency/error-cascade is
// safe under the current one-signature-at-a-time status model.
func (r *CascadePolicyReconciler) applyFanOutMitigation(
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
		log.Info("DetectOnly: would cap DestinationRule connectionPool.http",
			"name", name,
			"namespace", ns,
			"http1MaxPendingRequests", mitigation.TripHTTP1MaxPendingRequests,
			"http2MaxRequests", mitigation.TripHTTP2MaxRequests,
		)
		return nil
	}

	mitigation.ApplyFanOutConnectionPoolTrip(dr)
	if err := r.Update(ctx, dr); err != nil {
		return fmt.Errorf("update DestinationRule %s/%s: %w", ns, name, err)
	}
	mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification), kindDestinationRule).Inc()
	log.Info("patched DestinationRule connectionPool.http", "name", name, "namespace", ns)
	return nil
}
