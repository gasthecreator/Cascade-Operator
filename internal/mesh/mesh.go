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
// Deliberately narrow scope for this slice: only QueryBuilder is defined
// and wired through the reconciler so far. A parallel Mitigator interface
// (trip/restore patch application, currently embedded directly in
// internal/mitigation's Istio-typed functions and internal/controller's
// Get/Update/Patch call sites across ~7 files) is real, larger surface
// that PLAN.md §5 Phase 6 still calls for, but refactoring it is its own
// dedicated slice — noted here, not silently assumed done alongside this
// one. See docs/worklog for the exact accounting of what Phase 6 covers so
// far vs. what remains.
package mesh

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
