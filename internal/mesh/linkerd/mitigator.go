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

package linkerd

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mesh"
	spv1alpha2 "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd/serviceprofile/v1alpha2"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Object-kind label values for mesh.TripOutcome.AppliedKinds — same purpose
// as istio.KindDestinationRule/KindVirtualService, one level over
// corev1.Service/ServiceProfile instead.
const (
	KindService        = "Service"
	KindServiceProfile = "ServiceProfile"
)

// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=linkerd.io,resources=serviceprofiles,verbs=get;list;watch;update;patch

// Mitigator implements mesh.Mitigator for Linkerd.
//
// Latency/error-cascade patches a Service's failure-accrual annotations
// (circuit breaking); retry storm patches a ServiceProfile's
// spec.retryBudget; fan-out amplification has no Linkerd primitive at all
// (no connection-pool/concurrency-limiting equivalent — confirmed against
// Linkerd's own docs in a prior session) and stays detect-only: ApplyTrip
// still resolves the dependency's Service (so DependencyObjectMissing
// stays meaningful) but never patches anything, in either PolicyMode.
//
// Both objects this Mitigator does patch are resolved by convention (Get
// only, matching every other signature/mesh in this project — the
// operator never creates or deletes the Service or ServiceProfile itself,
// only fields on ones already there): the Service by
// mitigation.ParseServiceFQDN's (name, namespace), the ServiceProfile by
// its own Linkerd-specific naming convention — its object name is the
// dependency's full Service FQDN, not the short Service name (confirmed
// live: the working ServiceProfile in this package's spike was named
// "inventory-service.linkerd-demo.svc.cluster.local", not "inventory-
// service") — in the same namespace as the Service.
//
// Known, real limitation, stated plainly rather than solved with new
// machinery: Linkerd's own docs state circuit breaking is ignored for as
// long as *any* ServiceProfile exists for a Service, regardless of that
// ServiceProfile's own field values — so a dependency that has a
// pre-provisioned ServiceProfile for retry storm's own mitigation to patch
// can never also receive latency/error-cascade's failure-accrual
// mitigation on this mesh, even after this Mitigator's own CompleteRestore
// has fully reset that ServiceProfile's retryBudget back to its true
// original: the *object's* mere existence is what blocks it, not its
// current field values, and this Mitigator does not (and, matching this
// project's own "never own object lifecycle" convention, should not)
// delete the ServiceProfile object to work around that. ApplyTrip's
// latency/error-cascade path checks for this and logs it, purely
// informationally — see applyLatencyErrorTrip's own doc comment. The
// same-policy handoff between the two signatures (one CascadePolicy
// tracks exactly one active signature at a time) is otherwise already
// fully handled by the existing, mesh-agnostic
// internal/controller.forceCompleteOutgoingRestore, which drives through
// this Mitigator's own HasManagedEdges/CompleteRestore — no new
// arbitration bookkeeping needed on top of implementing those two methods
// correctly for each signature below.
type Mitigator struct {
	Client client.Client
}

// NewMitigator constructs a Linkerd Mitigator around c.
func NewMitigator(c client.Client) *Mitigator {
	return &Mitigator{Client: c}
}

var _ mesh.Mitigator = (*Mitigator)(nil)

func isAbsent(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

// serviceProfileName is the dependency host's ServiceProfile object name —
// the full Service FQDN itself, confirmed live against this project's own
// working ServiceProfile fixture (see this file's own doc comment).
func serviceProfileName(host string) string {
	return strings.TrimSuffix(strings.TrimSpace(host), ".")
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
		return m.applyFanOutTrip(ctx, host)
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return m.applyLatencyErrorTrip(ctx, policy, host)
	case cascadev1alpha1.SignatureRetryStorm:
		return m.applyRetryStormTrip(ctx, policy, host)
	default:
		return mesh.TripOutcome{}, fmt.Errorf("linkerd.Mitigator.ApplyTrip: unknown signature %s", sig)
	}
}

// applyFanOutTrip resolves the dependency's Service so
// DependencyObjectMissing stays meaningful, but never patches anything —
// Linkerd has no fan-out/connection-pool primitive on this mesh (see this
// Mitigator's own doc comment). Explicit in both PolicyModes, not a silent
// skip: logged at Info level every call, regardless of Mode, since neither
// mode has anything to actually apply here.
func (m *Mitigator) applyFanOutTrip(ctx context.Context, host string) (mesh.TripOutcome, error) {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return mesh.TripOutcome{}, nil //nolint:nilerr // unresolvable FQDN is "not found," not a reconcile error — matches every other signature's own convention.
	}

	svc := &corev1.Service{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, svc)
	if isAbsent(err) {
		log.Info("Service missing; skipping fan-out check", "name", name, "namespace", ns)
		return mesh.TripOutcome{}, nil
	}
	if err != nil {
		return mesh.TripOutcome{}, fmt.Errorf("get Service %s/%s: %w", ns, name, err)
	}

	log.Info("Linkerd has no fan-out amplification mitigation primitive (no connection-pool/concurrency-limiting equivalent); detect-only on this mesh",
		"name", name, "namespace", ns,
	)
	return mesh.TripOutcome{PrimaryFound: true}, nil
}

// applyLatencyErrorTrip patches svc's failure-accrual annotations.
func (m *Mitigator) applyLatencyErrorTrip(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) (mesh.TripOutcome, error) {
	log := logf.FromContext(ctx)

	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return mesh.TripOutcome{}, nil //nolint:nilerr // unresolvable FQDN is "not found," not a reconcile error — matches every other signature's own convention.
	}

	svc := &corev1.Service{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, svc)
	if isAbsent(err) {
		log.Info("Service missing; skipping patch", "name", name, "namespace", ns)
		return mesh.TripOutcome{}, nil
	}
	if err != nil {
		return mesh.TripOutcome{}, fmt.Errorf("get Service %s/%s: %w", ns, name, err)
	}

	// Informational only — see this Mitigator's own doc comment for why a
	// pre-existing ServiceProfile silently defeats the annotations this
	// trip is about to write, and why that is not worked around here.
	sp := &spv1alpha2.ServiceProfile{}
	spErr := m.Client.Get(ctx, types.NamespacedName{Name: serviceProfileName(host), Namespace: ns}, sp)
	if spErr == nil {
		log.Info("ServiceProfile also exists for this dependency; Linkerd ignores failure-accrual annotations for as long as it exists",
			"service", name, "namespace", ns, "serviceProfile", sp.Name,
		)
	}

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would patch Service failure-accrual annotations",
			"name", name,
			"namespace", ns,
			"maxFailures", tripFailureAccrualMaxFailures,
			"minPenalty", tripFailureAccrualMinPenalty.String(),
		)
		return mesh.TripOutcome{PrimaryFound: true}, nil
	}

	applyFailureAccrualTrip(svc)
	if err := m.Client.Update(ctx, svc); err != nil {
		return mesh.TripOutcome{PrimaryFound: true}, fmt.Errorf("update Service %s/%s: %w", ns, name, err)
	}
	log.Info("patched Service failure-accrual annotations", "name", name, "namespace", ns)
	return mesh.TripOutcome{PrimaryFound: true, AppliedKinds: []string{KindService}}, nil
}

// applyRetryStormTrip patches sp's spec.retryBudget.
func (m *Mitigator) applyRetryStormTrip(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	host string,
) (mesh.TripOutcome, error) {
	log := logf.FromContext(ctx)

	_, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return mesh.TripOutcome{}, nil //nolint:nilerr // unresolvable FQDN is "not found," not a reconcile error — matches every other signature's own convention.
	}
	spName := serviceProfileName(host)

	sp := &spv1alpha2.ServiceProfile{}
	err = m.Client.Get(ctx, types.NamespacedName{Name: spName, Namespace: ns}, sp)
	if isAbsent(err) {
		log.Info("ServiceProfile missing; skipping patch", "name", spName, "namespace", ns)
		return mesh.TripOutcome{}, nil
	}
	if err != nil {
		return mesh.TripOutcome{}, fmt.Errorf("get ServiceProfile %s/%s: %w", ns, spName, err)
	}

	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would cut ServiceProfile retryBudget",
			"name", spName,
			"namespace", ns,
			"retryRatio", tripRetryRatio,
			"minRetriesPerSecond", tripMinRetriesPerSecond,
		)
		return mesh.TripOutcome{PrimaryFound: true}, nil
	}

	applyRetryBudgetTrip(sp)
	if err := m.Client.Update(ctx, sp); err != nil {
		return mesh.TripOutcome{PrimaryFound: true}, fmt.Errorf("update ServiceProfile %s/%s: %w", ns, spName, err)
	}
	log.Info("patched ServiceProfile retryBudget", "name", spName, "namespace", ns)
	return mesh.TripOutcome{PrimaryFound: true, AppliedKinds: []string{KindServiceProfile}}, nil
}

// serviceEdge pairs a dependsOn host with its resolved, operator-managed
// Service — the Linkerd-package-local twin of istio.drEdge.
type serviceEdge struct {
	host string
	svc  *corev1.Service
}

func (m *Mitigator) listManagedServiceEdges(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) ([]serviceEdge, error) {
	log := logf.FromContext(ctx)
	var out []serviceEdge
	for _, host := range policy.Spec.DependsOn {
		name, ns, err := mitigation.ParseServiceFQDN(host)
		if err != nil {
			log.Error(err, "cannot resolve Service from dependsOn FQDN", "host", host)
			continue
		}
		svc := &corev1.Service{}
		err = m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, svc)
		if isAbsent(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get Service %s/%s: %w", ns, name, err)
		}
		if isServiceOperatorManaged(svc) {
			out = append(out, serviceEdge{host: host, svc: svc})
		}
	}
	return out, nil
}

// serviceProfileEdge is listManagedServiceProfileEdges' element type.
type serviceProfileEdge struct {
	host string
	sp   *spv1alpha2.ServiceProfile
}

func (m *Mitigator) listManagedServiceProfileEdges(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) ([]serviceProfileEdge, error) {
	log := logf.FromContext(ctx)
	var out []serviceProfileEdge
	for _, host := range policy.Spec.DependsOn {
		_, ns, err := mitigation.ParseServiceFQDN(host)
		if err != nil {
			log.Error(err, "cannot resolve ServiceProfile from dependsOn FQDN", "host", host)
			continue
		}
		spName := serviceProfileName(host)
		sp := &spv1alpha2.ServiceProfile{}
		err = m.Client.Get(ctx, types.NamespacedName{Name: spName, Namespace: ns}, sp)
		if isAbsent(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get ServiceProfile %s/%s: %w", ns, spName, err)
		}
		if isServiceProfileOperatorManaged(sp) {
			out = append(out, serviceProfileEdge{host: host, sp: sp})
		}
	}
	return out, nil
}

// HasManagedEdges implements mesh.Mitigator.
func (m *Mitigator) HasManagedEdges(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	sig cascadev1alpha1.SignatureType,
) (bool, error) {
	switch sig {
	case cascadev1alpha1.SignatureFanOutAmplification:
		// No Linkerd primitive is ever applied for this signature — see
		// applyFanOutTrip's own doc comment — so there is never anything
		// to restore. The caller (internal/controller's beginRestoreFanOut/
		// advanceRestoreFanOut) snaps straight to Normal on false, without
		// ever calling ApplyRestoreStep/CompleteRestore below.
		return false, nil
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		edges, err := m.listManagedServiceEdges(ctx, policy)
		if err != nil {
			return false, err
		}
		return len(edges) > 0, nil
	case cascadev1alpha1.SignatureRetryStorm:
		edges, err := m.listManagedServiceProfileEdges(ctx, policy)
		if err != nil {
			return false, err
		}
		return len(edges) > 0, nil
	default:
		return false, fmt.Errorf("linkerd.Mitigator.HasManagedEdges: unknown signature %s", sig)
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
		// Nothing was ever applied — see HasManagedEdges, which always
		// returns false for this signature and so keeps the caller from
		// ever reaching here in practice. A no-op regardless, for
		// interface completeness.
		return nil
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return m.applyLatencyErrorRestoreStep(ctx, policy, step)
	case cascadev1alpha1.SignatureRetryStorm:
		return m.applyRetryStormRestoreStep(ctx, policy, step)
	default:
		return fmt.Errorf("linkerd.Mitigator.ApplyRestoreStep: unknown signature %s", sig)
	}
}

func (m *Mitigator) applyLatencyErrorRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, step int32) error {
	log := logf.FromContext(ctx)
	edges, err := m.listManagedServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would loosen Service failure-accrual annotations", "restoreStep", step, "edges", len(edges))
		return nil
	}
	for _, e := range edges {
		orig, err := parseOriginalFailureAccrual(e.svc.Annotations[annotationOriginalFailureAccrual])
		if err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if step >= mitigation.RestoreFinalStep {
			applyOriginalFailureAccrual(e.svc, orig)
		} else {
			applyFailureAccrualRestoreStep(e.svc, orig, step)
		}
		if err := m.Client.Update(ctx, e.svc); err != nil {
			return fmt.Errorf("update Service during restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (m *Mitigator) applyRetryStormRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, step int32) error {
	log := logf.FromContext(ctx)
	edges, err := m.listManagedServiceProfileEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would ramp ServiceProfile retryBudget", "restoreStep", step, "edges", len(edges))
		return nil
	}
	for _, e := range edges {
		orig, err := parseOriginalRetryBudget(e.sp.Annotations[annotationOriginalRetryBudget])
		if err != nil {
			return fmt.Errorf("restore step %d on %s: %w", step, e.host, err)
		}
		if step >= mitigation.RestoreFinalStep {
			applyOriginalRetryBudget(e.sp, orig)
		} else {
			applyRetryBudgetRestoreStep(e.sp, orig, step)
		}
		if err := m.Client.Update(ctx, e.sp); err != nil {
			return fmt.Errorf("update ServiceProfile during restore %s: %w", e.host, err)
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
		return nil
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		return m.completeLatencyErrorRestore(ctx, policy)
	case cascadev1alpha1.SignatureRetryStorm:
		return m.completeRetryStormRestore(ctx, policy)
	default:
		return fmt.Errorf("linkerd.Mitigator.CompleteRestore: unknown signature %s", sig)
	}
}

func (m *Mitigator) completeLatencyErrorRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := m.listManagedServiceEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original failure-accrual annotations and drop them", "edges", len(edges))
		return nil
	}
	for _, e := range edges {
		orig, err := parseOriginalFailureAccrual(e.svc.Annotations[annotationOriginalFailureAccrual])
		if err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		applyOriginalFailureAccrual(e.svc, orig)
		stripFailureAccrualManagedAnnotations(e.svc)
		if err := m.Client.Update(ctx, e.svc); err != nil {
			return fmt.Errorf("update Service completing restore %s: %w", e.host, err)
		}
	}
	return nil
}

func (m *Mitigator) completeRetryStormRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy) error {
	log := logf.FromContext(ctx)
	edges, err := m.listManagedServiceProfileEdges(ctx, policy)
	if err != nil {
		return err
	}
	if policy.Spec.Mode == cascadev1alpha1.PolicyModeDetectOnly {
		log.Info("DetectOnly: would restore original ServiceProfile retryBudget and drop annotations", "edges", len(edges))
		return nil
	}
	for _, e := range edges {
		orig, err := parseOriginalRetryBudget(e.sp.Annotations[annotationOriginalRetryBudget])
		if err != nil {
			return fmt.Errorf("complete restore on %s: %w", e.host, err)
		}
		applyOriginalRetryBudget(e.sp, orig)
		stripRetryBudgetManagedAnnotations(e.sp)
		if err := m.Client.Update(ctx, e.sp); err != nil {
			return fmt.Errorf("update ServiceProfile completing restore %s: %w", e.host, err)
		}
	}
	return nil
}
