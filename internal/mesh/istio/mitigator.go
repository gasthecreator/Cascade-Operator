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

// Object-kind label values for mesh.TripOutcome.AppliedKinds — matching
// internal/controller/metrics.go's existing kindDestinationRule/
// kindVirtualService constants exactly, so the Prometheus label values
// this migration produces are byte-identical to what the pre-migration
// code emitted. Exported since internal/controller reads them when
// incrementing its own metric (this Mitigator reports what it touched;
// the metric itself stays controller-owned — see mesh.Mitigator's doc
// comment).
const (
	KindDestinationRule = "DestinationRule"
	KindVirtualService  = "VirtualService"
)

// Mitigator implements mesh.Mitigator for Istio.
//
// Fan-out amplification and latency/error-cascade are migrated (PLAN.md §5
// Phases 6.3/6.4). Retry storm is not — its restore path has the JSON-
// Patch-vs-merge-patch zero-value subtlety this project spent real effort
// getting right (PLAN.md §2.6), and migrating it needs its own dedicated,
// careful pass rather than being folded into this one. ApplyTrip/
// HasManagedEdges/ApplyRestoreStep/CompleteRestore below return an error
// for SignatureRetryStorm, which is safe today because internal/controller
// never calls this Mitigator for it yet, not because it's unreachable in
// principle.
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

// drEdge pairs a dependsOn host with its resolved, operator-managed
// DestinationRule — the Istio-package-local twin of
// internal/controller's managedDREdge. Kept as its own type here rather
// than sharing that one directly: internal/controller's copy is still
// used by retry storm's own restore path (not yet migrated), and this
// package must not reach into internal/controller's unexported symbols.
// The duplication is temporary — once retry storm is also migrated,
// internal/controller's edge-listing helpers become dead code and can be
// deleted, leaving this package's copy as the only one.
type drEdge struct {
	host string
	dr   *networkingv1.DestinationRule
}

// listManagedDREdges resolves every policy.Spec.DependsOn host to an
// operator-managed DestinationRule, skipping hosts with no resolvable or
// no operator-managed object — mirrors internal/controller's
// listManagedDestinationRuleEdges exactly (see drEdge's doc comment for
// why this isn't shared directly yet). Shared by both fan-out and
// latency/error-cascade's primary — the listing logic itself only checks
// the generic managed-by annotation, not which signature actually
// patched the object, exactly like the pre-migration controller code.
func (m *Mitigator) listManagedDREdges(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) ([]drEdge, error) {
	log := logf.FromContext(ctx)
	var out []drEdge
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
			out = append(out, drEdge{host: host, dr: dr})
		}
	}
	return out, nil
}

// vsEdge is listManagedVSEdges' element type — the twin of drEdge for
// VirtualService, same temporary-duplication reasoning (internal/controller's
// own managedVSEdge is still used by retry storm's unmigrated restore path).
type vsEdge struct {
	host string
	vs   *networkingv1.VirtualService
}

// listManagedVSEdges is listManagedDREdges' twin for VirtualService —
// same convention-based resolution, same managed-by filter
// (mitigation.IsVirtualServiceManaged), one object kind over.
func (m *Mitigator) listManagedVSEdges(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) ([]vsEdge, error) {
	log := logf.FromContext(ctx)
	var out []vsEdge
	for _, host := range policy.Spec.DependsOn {
		name, ns, err := mitigation.ParseServiceFQDN(host)
		if err != nil {
			log.Error(err, "cannot resolve VirtualService from dependsOn FQDN", "host", host)
			continue
		}
		vs := &networkingv1.VirtualService{}
		err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)
		if isAbsent(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get VirtualService %s/%s: %w", ns, name, err)
		}
		if mitigation.IsVirtualServiceManaged(vs) {
			out = append(out, vsEdge{host: host, vs: vs})
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
) (mesh.TripOutcome, error) {
	switch sig {
	case cascadev1alpha1.SignatureFanOutAmplification:
		return m.applyFanOutTrip(ctx, policy, host)
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return m.applyLatencyErrorTrip(ctx, policy, host)
	default:
		return mesh.TripOutcome{}, fmt.Errorf("istio.Mitigator.ApplyTrip: signature %s not yet migrated to mesh.Mitigator", sig)
	}
}

func (m *Mitigator) applyFanOutTrip(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) (mesh.TripOutcome, error) {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return mesh.TripOutcome{}, nil //nolint:nilerr // unresolvable FQDN is "not found," not a reconcile error — matches the pre-migration behavior in fanout_mitigate.go.
	}

	dr := &networkingv1.DestinationRule{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
	if isAbsent(err) {
		log.Info("DestinationRule missing; skipping patch", "name", name, "namespace", ns)
		return mesh.TripOutcome{}, nil
	}
	if err != nil {
		return mesh.TripOutcome{}, fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
	}

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cap DestinationRule connectionPool.http",
			"name", name,
			"namespace", ns,
			"http1MaxPendingRequests", mitigation.TripHTTP1MaxPendingRequests,
			"http2MaxRequests", mitigation.TripHTTP2MaxRequests,
		)
		return mesh.TripOutcome{PrimaryFound: true}, nil
	}

	mitigation.ApplyFanOutConnectionPoolTrip(dr)
	if err := m.Client.Update(ctx, dr); err != nil {
		return mesh.TripOutcome{PrimaryFound: true}, fmt.Errorf("update DestinationRule %s/%s: %w", ns, name, err)
	}
	log.Info("patched DestinationRule connectionPool.http", "name", name, "namespace", ns)
	return mesh.TripOutcome{PrimaryFound: true, AppliedKinds: []string{KindDestinationRule}}, nil
}

// applyLatencyErrorTrip patches the DestinationRule primary
// (outlierDetection) and, independently, the VirtualService secondary
// (route timeout) — mirrors mitigate.go's applyLatencyErrorMitigation:
// the primary applies even if the secondary's VirtualService is missing,
// and vice versa (PLAN.md §2.6), and only the primary's found/absent
// status feeds into DependencyObjectMissing.
func (m *Mitigator) applyLatencyErrorTrip(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) (mesh.TripOutcome, error) {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return mesh.TripOutcome{}, nil //nolint:nilerr // unresolvable FQDN is "not found," not a reconcile error — matches the pre-migration behavior in mitigate.go.
	}

	var outcome mesh.TripOutcome

	dr := &networkingv1.DestinationRule{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dr)
	switch {
	case isAbsent(err):
		log.Info("DestinationRule missing; skipping primary patch", "name", name, "namespace", ns)
	case err != nil:
		return mesh.TripOutcome{}, fmt.Errorf("get DestinationRule %s/%s: %w", ns, name, err)
	default:
		outcome.PrimaryFound = true
		if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
			log.Info("DetectOnly: would patch DestinationRule outlierDetection",
				"name", name,
				"namespace", ns,
				"consecutive5xxErrors", mitigation.TripConsecutive5xx,
				"interval", mitigation.TripInterval.String(),
				"baseEjectionTime", mitigation.TripBaseEjection.String(),
			)
		} else {
			mitigation.ApplyLatencyErrorOutlierTrip(dr)
			if err := m.Client.Update(ctx, dr); err != nil {
				return outcome, fmt.Errorf("update DestinationRule %s/%s: %w", ns, name, err)
			}
			log.Info("patched DestinationRule outlierDetection", "name", name, "namespace", ns)
			outcome.AppliedKinds = append(outcome.AppliedKinds, KindDestinationRule)
		}
	}

	vs := &networkingv1.VirtualService{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)
	switch {
	case isAbsent(err):
		log.Info("VirtualService missing; skipping secondary patch", "name", name, "namespace", ns)
	case err != nil:
		return outcome, fmt.Errorf("get VirtualService %s/%s: %w", ns, name, err)
	default:
		th := cascadev1alpha1.EffectiveThresholds(policy, host)
		if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
			log.Info("DetectOnly: would cap VirtualService route timeout",
				"name", name,
				"namespace", ns,
				"timeoutMs", th.LatencyP99Ms,
			)
		} else {
			mitigation.ApplyLatencyErrorTimeoutTrip(vs, th.LatencyP99Ms)
			if err := m.Client.Update(ctx, vs); err != nil {
				return outcome, fmt.Errorf("update VirtualService %s/%s: %w", ns, name, err)
			}
			log.Info("patched VirtualService timeout", "name", name, "namespace", ns)
			outcome.AppliedKinds = append(outcome.AppliedKinds, KindVirtualService)
		}
	}

	return outcome, nil
}

// HasManagedEdges implements mesh.Mitigator.
func (m *Mitigator) HasManagedEdges(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
) (bool, error) {
	switch sig {
	case cascadev1alpha1.SignatureFanOutAmplification:
		edges, err := m.listManagedDREdges(ctx, policy)
		if err != nil {
			return false, err
		}
		return len(edges) > 0, nil
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		drEdges, err := m.listManagedDREdges(ctx, policy)
		if err != nil {
			return false, err
		}
		vsEdges, err := m.listManagedVSEdges(ctx, policy)
		if err != nil {
			return false, err
		}
		return len(drEdges) > 0 || len(vsEdges) > 0, nil
	default:
		return false, fmt.Errorf("istio.Mitigator.HasManagedEdges: signature %s not yet migrated to mesh.Mitigator", sig)
	}
}

// ApplyRestoreStep implements mesh.Mitigator.
func (m *Mitigator) ApplyRestoreStep(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
	step int32,
) error {
	switch sig {
	case cascadev1alpha1.SignatureFanOutAmplification:
		return m.applyFanOutRestoreStep(ctx, policy, step)
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return m.applyLatencyErrorRestoreStep(ctx, policy, step)
	default:
		return fmt.Errorf("istio.Mitigator.ApplyRestoreStep: signature %s not yet migrated to mesh.Mitigator", sig)
	}
}

func (m *Mitigator) applyFanOutRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, step int32) error {
	log := logf.FromContext(ctx)
	edges, err := m.listManagedDREdges(ctx, policy)
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

func (m *Mitigator) applyLatencyErrorRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, step int32) error {
	log := logf.FromContext(ctx)
	drEdges, err := m.listManagedDREdges(ctx, policy)
	if err != nil {
		return err
	}
	vsEdges, err := m.listManagedVSEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would loosen DestinationRule outlierDetection and VirtualService timeout",
			"restoreStep", step, "drEdges", len(drEdges), "vsEdges", len(vsEdges))
		return nil
	}
	for _, e := range drEdges {
		if err := mitigation.ApplyLatencyErrorOutlierRestoreStep(e.dr, step); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := m.Client.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule during restore %s: %w", e.host, err)
		}
	}
	for _, e := range vsEdges {
		latencyP99Ms := cascadev1alpha1.EffectiveThresholds(policy, e.host).LatencyP99Ms
		if err := mitigation.ApplyLatencyErrorTimeoutRestoreStep(e.vs, step, latencyP99Ms); err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if err := m.Client.Update(ctx, e.vs); err != nil {
			return fmt.Errorf("update VirtualService during restore %s: %w", e.host, err)
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
	switch sig {
	case cascadev1alpha1.SignatureFanOutAmplification:
		return m.completeFanOutRestore(ctx, policy)
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return m.completeLatencyErrorRestore(ctx, policy)
	default:
		return fmt.Errorf("istio.Mitigator.CompleteRestore: signature %s not yet migrated to mesh.Mitigator", sig)
	}
}

func (m *Mitigator) completeFanOutRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := m.listManagedDREdges(ctx, policy)
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

func (m *Mitigator) completeLatencyErrorRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	drEdges, err := m.listManagedDREdges(ctx, policy)
	if err != nil {
		return err
	}
	vsEdges, err := m.listManagedVSEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original outlierDetection/timeout and drop annotations",
			"drEdges", len(drEdges), "vsEdges", len(vsEdges))
		return nil
	}
	for _, e := range drEdges {
		if err := mitigation.CompleteLatencyErrorOutlierRestore(e.dr); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := m.Client.Update(ctx, e.dr); err != nil {
			return fmt.Errorf("update DestinationRule completing restore %s: %w", e.host, err)
		}
	}
	for _, e := range vsEdges {
		if err := mitigation.CompleteLatencyErrorTimeoutRestore(e.vs); err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		if err := m.Client.Update(ctx, e.vs); err != nil {
			return fmt.Errorf("update VirtualService completing restore %s: %w", e.host, err)
		}
	}
	return nil
}
