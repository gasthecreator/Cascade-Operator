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

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/notify"
	"github.com/gasthecreator/Cascade-Operator/internal/signatures"
)

// notifyTrip and notifyRestore (below) both nil-check r.Notify and log any
// send failure rather than returning it — a notification is observability,
// not part of the mitigation correctness path, so it must never turn a
// webhook outage into a failed reconcile (PLAN.md §5 Phase 4).

func (r *CascadePolicyReconciler) notifyTrip(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
	host string,
	v signatures.Verdict,
) {
	if r.Notify == nil {
		return
	}
	log := logf.FromContext(ctx)
	err := r.Notify.NotifyTrip(ctx, notify.TripEvent{
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
		Signature:       string(sig),
		Dependency:      host,
		Confidence:      v.Confidence,
		Evidence:        v.Evidence,
	})
	if err != nil {
		log.Error(err, "failed to send trip notification", "signature", sig, "dependency", host)
	}
}

func (r *CascadePolicyReconciler) notifyRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
) {
	if r.Notify == nil {
		return
	}
	log := logf.FromContext(ctx)
	err := r.Notify.NotifyRestore(ctx, notify.RestoreEvent{
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
		Signature:       string(sig),
	})
	if err != nil {
		log.Error(err, "failed to send restore notification", "signature", sig)
	}
}
