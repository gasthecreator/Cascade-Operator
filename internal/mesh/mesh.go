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

// Package mesh defines the seam between cascade-detection/mitigation logic
// and a specific service mesh implementation (Istio, Linkerd, ...) —
// PLAN.md §5 Phase 6.1's adapter interface, modeled directly on the
// existing precedent in internal/metrics.Querier ("the detector package
// depends only on Snapshot/Sample — never on this interface or on
// net/http"). Istio's existing detection queries become the reference
// implementation of QueryBuilder (internal/mesh/istio), not a special case;
// a Linkerd implementation is a second, equally first-class package
// alongside it.
//
// Status as of Phase 6.3 (see docs/worklog for the exact dated accounting):
// QueryBuilder is fully wired through the reconciler. Mitigator is defined
// and implemented for Istio's fan-out-amplification signature only —
// latency/error-cascade and retry storm (both of which manage a secondary
// object kind alongside their primary, unlike fan-out) still call
// internal/mitigation directly from internal/controller's own per-signature
// functions, not through this interface yet. That migration is real,
// larger surface, left as its own follow-up slice rather than attempted
// alongside fan-out's — see internal/mesh/istio/mitigator.go's own doc
// comment for why fan-out went first.
package mesh

import (
	"context"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// QueryBuilder constructs the PromQL (or, for a future non-Prometheus
// mesh, whatever query language that mesh's metrics use) needed to
// evaluate each of the three cascade signatures for one dependency host.
// Every method's signature matches the corresponding
// internal/signatures.*Input field names exactly, so a caller's shape is
// "build the query, run it through metrics.Querier, feed the result into
// the matching signatures.Detect* function" regardless of which
// implementation is wired in.
type QueryBuilder interface {
	// LatencyP99Query returns the query for a dependency's client-perceived
	// (source-reported) p99 latency in milliseconds over windowSeconds.
	LatencyP99Query(host string, windowSeconds int32) string

	// ErrorRateQuery returns the query for a dependency's error-rate
	// fraction (5xx / total) over windowSeconds.
	ErrorRateQuery(host string, windowSeconds int32) string

	// RetryStormRatioQuery returns the query for a dependency's
	// destination:source request-count ratio over windowSeconds — the
	// retry-amplification signal (implicit healthy baseline is 1).
	RetryStormRatioQuery(host string, windowSeconds int32) string

	// FanOutRatioQuery returns the query for one dependency's request rate
	// against the calling service's own inbound request rate over
	// windowSeconds — the fan-out amplification signal (implicit healthy
	// baseline is 1).
	FanOutRatioQuery(dependencyHost, callerHost string, windowSeconds int32) string
}

// Mitigator applies and restores one signature's mesh-level mitigation.
// Unlike QueryBuilder (a pure string builder), a Mitigator needs a
// Kubernetes client to resolve and patch objects — implementations are
// constructed with one (e.g. istio.NewMitigator(client.Client)).
//
// Scoped deliberately narrow: this interface owns only the mesh-specific
// object mutation (which object(s) exist for a host, how to patch them,
// how to capture/restore their pre-trip originals). It does not own
// DependencyObjectMissing (a CascadePolicy-status concern identical
// regardless of mesh — the caller sets/clears it from ApplyTrip's found
// return value), Prometheus metrics, trip/restore notifications, or
// status.Phase/RestoreStep transitions — all of those stay in
// internal/controller, called once per reconcile regardless of which
// Mitigator is wired in.
type Mitigator interface {
	// ApplyTrip mitigates sig on host, per policy.Spec.Mode (DetectOnly:
	// logs what it would do, writes nothing). found reports whether the
	// signature's primary mesh object was resolvable for host — false
	// means "nothing to patch," not an error; the caller decides what
	// that means for DependencyObjectMissing.
	ApplyTrip(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, sig cascadev1alpha1.SignatureType, host string) (found bool, err error)

	// HasManagedEdges reports whether sig has any previously-mitigated
	// edge left to restore, across every policy.Spec.DependsOn host — the
	// caller snaps straight to Normal when false, without entering the
	// ramp.
	HasManagedEdges(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, sig cascadev1alpha1.SignatureType) (bool, error)

	// ApplyRestoreStep ramps sig's managed edges toward their true
	// original values at step (0..mitigation.RestoreFinalStep). At
	// RestoreFinalStep it writes the true original values but does not
	// strip this signature's annotations or report completion — that is
	// CompleteRestore's job, called by the caller as a separate, explicit
	// next action (mirrors the existing two-tick shape: reach true-
	// original values on one tick, confirm+strip on the next).
	ApplyRestoreStep(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, sig cascadev1alpha1.SignatureType, step int32) error

	// CompleteRestore restores every managed edge's true original values
	// and strips sig's own annotations, unconditionally — called both by
	// the ramp's own confirming tick and by a same-object signature
	// handoff's eager force-complete.
	CompleteRestore(ctx context.Context, policy *cascadev1alpha1.CascadePolicy, sig cascadev1alpha1.SignatureType) error
}
