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

package istio

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mesh"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Mitigator implements mesh.Mitigator for Istio.
//
// Fan-out amplification only, for now (PLAN.md §5 Phase 6.3): it is the
// simplest of the three signatures to migrate — one managed object kind
// (DestinationRule), no secondary object, and no two-object-kind restore
// bookkeeping — so it went first, as a real, fully-working, fully-tested
// proof that this interface shape is sound, rather than attempting all
// three signatures (each with more moving parts) in one pass.
// Latency/error-cascade (DestinationRule primary + VirtualService
// secondary) and retry storm (VirtualService primary + DestinationRule
// secondary) still call internal/mitigation directly from
// internal/controller's own per-signature functions — ApplyTrip/
// HasManagedEdges/ApplyRestoreStep/CompleteRestore below return an error
// for any other SignatureType, which is safe today because
// internal/controller never calls this Mitigator for those two yet, not
// because they're unreachable in principle.
type Mitigator struct {
	Client client.Client
}

// NewMitigator constructs an Istio Mitigator around c.
func NewMitigator(c client.Client) *Mitigator {
	return &Mitigator{Client: c}
}

var _ mesh.Mitigator = (*Mitigator)(nil)

func isAbsent(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

// fanOutEdge pairs a dependsOn host with its resolved, operator-managed
// DestinationRule — the Istio-package-local twin of
// internal/controller's managedDREdge. Kept as its own type here rather
// than sharing that one directly: internal/controller's copy is still
// used by latency/error-cascade's own restore path (not yet migrated),
// and this package must not reach into internal/controller's unexported
// symbols. The duplication is temporary — once latency/error-cascade and
// retry storm are also migrated, internal/controller's edge-listing
// helpers become dead code and can be deleted, leaving this package's
// copy as the only one.
type fanOutEdge struct {
	host string
	dr   *networkingv1.DestinationRule
}

// listFanOutEdges resolves every policy.Spec.DependsOn host to an
// operator-managed DestinationRule, skipping hosts with no resolvable or
// no operator-managed object — mirrors internal/controller's
// listManagedDestinationRuleEdges exactly (see fanOutEdge's doc comment
// for why this isn't shared directly yet).
func (m *Mitigator) listFanOutEdges(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) ([]fanOutEdge, error) {
	log := logf.FromContext(ctx)
	var out []fanOutEdge
	for _, host := range policy.Spec.DependsOn {
		name, ns, err := mitigation.ParseServiceFQDN(host)
		if err != nil {
			log.Error(err, "cannot resolve DestinationRule from dependsOn FQDN", "host", host)
			continue
		}
		dr := &networkingv1.DestinationRule{}
		err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
		if isAbsent(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
		}
		if mitigation.IsOperatorManaged(dr) {
			out = append(out, fanOutEdge{host: host, dr: dr})
		}
	}
	return out, nil
}

// ApplyTrip implements mesh.Mitigator.
func (m *Mitigator) ApplyTrip(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
	host string,
) (bool, error) {
	if sig != cascadev1alpha1.SignatureFanOutAmplification {
		return false, fmt.Errorf("istio.Mitigator.ApplyTrip: signature %s not yet migrated to mesh.Mitigator", sig)
	}
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return false, nil //nolint:nilerr // unresolvable FQDN is "not found," not a reconcile error — matches the pre-migration behavior in fanout_mitigate.go.
	}

	dr := &networkingv1.DestinationRule{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
	if isAbsent(err) {
		log.Info("DestinationRule missing; skipping patch", "name", name, "namespace", ns)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
	}

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cap DestinationRule connectionPool.http",
			"name", name,
			"namespace", ns,
			"http1MaxPendingRequests", mitigation.TripHTTP1MaxPendingRequests,
			"http2MaxRequests", mitigation.TripHTTP2MaxRequests,
		)
		return true, nil
	}

	mitigation.ApplyFanOutConnectionPoolTrip(dr)
	if err := m.Client.Update(ctx, dr); err != nil {
		return true, fmt.Errorf("update DestinationRule %s/%s: %w", ns, name, err)
	}
	log.Info("patched DestinationRule connectionPool.http", "name", name, "namespace", ns)
	return true, nil
}

// HasManagedEdges implements mesh.Mitigator.
func (m *Mitigator) HasManagedEdges(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
) (bool, error) {
	if sig != cascadev1alpha1.SignatureFanOutAmplification {
		return false, fmt.Errorf("istio.Mitigator.HasManagedEdges: signature %s not yet migrated to mesh.Mitigator", sig)
	}
	edges, err := m.listFanOutEdges(ctx, policy)
	if err != nil {
		return false, err
	}
	return len(edges) > 0, nil
}

// ApplyRestoreStep implements mesh.Mitigator.
func (m *Mitigator) ApplyRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
	step int32,
) error {
	if sig != cascadev1alpha1.SignatureFanOutAmplification {
		return fmt.Errorf("istio.Mitigator.ApplyRestoreStep: signature %s not yet migrated to mesh.Mitigator", sig)
	}
	log := logf.FromContext(ctx)

	edges, err := m.listFanOutEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would loosen DestinationRule connectionPool.http", "restoreStep", step, "edges", len(edges))
		return nil
	}
	for _, e := range edges {
		if err := mitigation.ApplyFanOutConnectionPoolRestoreStep(e.dr, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := m.Client.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule during restore %s: %w", e.host, err)
		}
	}
	return nil
}

// CompleteRestore implements mesh.Mitigator.
func (m *Mitigator) CompleteRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
) error {
	if sig != cascadev1alpha1.SignatureFanOutAmplification {
		return fmt.Errorf("istio.Mitigator.CompleteRestore: signature %s not yet migrated to mesh.Mitigator", sig)
	}
	log := logf.FromContext(ctx)

	edges, err := m.listFanOutEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original connectionPool.http and drop annotations", "edges", len(edges))
		return nil
	}
	for _, e := range edges {
		if err := mitigation.CompleteFanOutConnectionPoolRestore(e.dr); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := m.Client.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule completing restore %s: %w", e.host, err)
		}
	}
	return nil
}
