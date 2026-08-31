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

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// applyFanOutMitigation delegates to the reconciler's Mitigator (PLAN.md §5
// Phase 6.3 — the first signature migrated behind mesh.Mitigator) to
// resolve and, in Mitigate mode, patch whatever primitive that mesh uses to
// bulkhead fan-out amplification for host. DependencyObjectMissing stays a
// controller-owned, mesh-agnostic concern: set when the Mitigator reports
// found=false, cleared otherwise — see fanout_restore.go's doc comment for
// why sharing a DestinationRule with latency/error-cascade (still
// unmigrated, calling internal/mitigation directly) is safe under the
// current one-signature-at-a-time status model.
func (r *CascadePolicyReconciler) applyFanOutMitigation(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) error {
	found, err := r.mitigator().ApplyTrip(ctx, policy, cascadev1alpha1.SignatureFanOutAmplification, host)
	if err != nil {
		return fmt.Errorf("apply fan-out trip for %q: %w", host, err)
	}
	if !found {
		setDependencyMissing(policy, fmt.Sprintf("no mitigation target found for dependsOn %q", host))
		return nil
	}
	clearDependencyMissing(policy)
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeMitigate {
		mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureFanOutAmplification), kindDestinationRule).Inc()
	}
	return nil
}
