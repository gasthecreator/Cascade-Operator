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

// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch

// applyRetryStormMitigation delegates to the reconciler's Mitigator
// (PLAN.md §5 Phase 6.5 — the last of the three signatures migrated) to
// resolve and, in Mitigate mode, patch whatever primitives that mesh uses
// for retry storm — the VirtualService primary (retries.attempts) and,
// independently, the DestinationRule secondary (connectionPool.http
// maxRetries). DependencyObjectMissing stays a controller-owned,
// mesh-agnostic concern, driven only by the Mitigator's PrimaryFound
// return value — the primary applies even if the secondary's object is
// missing and vice versa (PLAN.md §2.6).
func (r *CascadePolicyReconciler) applyRetryStormMitigation(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) error {
	outcome, err := r.mitigator().ApplyTrip(ctx, policy, cascadev1alpha1.SignatureRetryStorm, host)
	if err != nil {
		return fmt.Errorf("apply retry-storm trip for %q: %w", host, err)
	}
	if !outcome.PrimaryFound {
		setDependencyMissing(policy, fmt.Sprintf("no mitigation target found for dependsOn %q", host))
		return nil
	}
	clearDependencyMissing(policy)
	for _, kind := range outcome.AppliedKinds {
		mitigationPatchesAppliedTotal.WithLabelValues(string(cascadev1alpha1.SignatureRetryStorm), kind).Inc()
	}
	return nil
}
