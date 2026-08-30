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
	"github.com/prometheus/client_golang/prometheus"

	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Object-kind label values for mitigationPatchesAppliedTotal — the two
// Istio object kinds the mitigation package ever patches (PLAN.md §2.6).
const (
	kindDestinationRule = "DestinationRule"
	kindVirtualService  = "VirtualService"
)

// Label names shared across the four metrics below (goconst: "signature"
// alone would otherwise repeat four times).
const (
	labelSignature  = "signature"
	labelDependency = "dependency"
	labelKind       = "kind"
)

// The operator's own instrumentation, registered on controller-runtime's
// existing metrics registry (sigs.k8s.io/controller-runtime/pkg/metrics) —
// the same registry cmd/main.go's manager already exposes on the metrics
// server (see metricsserver.Options in cmd/main.go), not a second
// registration path. Label cardinality is bounded deliberately: "signature"
// is the CRD's own three-value enum (cascadev1alpha1.SignatureType), "kind"
// is one of two Istio object kinds, and "dependency" is bounded by however
// many dependsOn hosts a single policy declares (a handful at most, the
// same set already used as a log field on every detector evaluation) — not
// an arbitrary/unbounded string.
var (
	// signaturesDetectedTotal counts every confirmed trip a detector
	// reports to Reconcile, labeled by which signature and which
	// dependency host tripped it — incremented once per trip, whether
	// that trip is a fresh one, a same-signature regression during an
	// active restoration ramp, or the first half of a same-object
	// signature handoff. Mirrors the "cascade signature tripped" log
	// line's own guard (only after a same-tick handoff force-complete,
	// if any, has succeeded) so a failed handoff never double-counts a
	// trip that Reconcile is about to retry.
	signaturesDetectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cascade_signatures_detected_total",
		Help: "Total cascade failure signatures detected (trips reported to Reconcile), by signature type and dependency host.",
	}, []string{labelSignature, labelDependency})

	// mitigationPatchesAppliedTotal counts a successful Update of the
	// Istio object each signature's primary patch touches — incremented
	// alongside each apply*Mitigation function's existing "patched ..."
	// log line, i.e. only on an actual write (never in DetectOnly mode,
	// never when the dependency object was missing).
	mitigationPatchesAppliedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cascade_mitigation_patches_applied_total",
		Help: "Total Istio object patches applied as cascade mitigation, by signature type and Istio object kind.",
	}, []string{labelSignature, labelKind})

	// restorationsCompletedTotal counts a signature's restoration
	// reaching its true pre-trip state — incremented inside each
	// complete*Restore function (restore.go, retry_restore.go,
	// fanout_restore.go), which is the single call site each signature's
	// gradual ramp (its final step) and a same-object signature handoff's
	// force-complete both already funnel through, so this one increment
	// site naturally covers both paths without duplicating it per caller.
	restorationsCompletedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cascade_restorations_completed_total",
		Help: "Total times a signature's mitigation was fully restored to its pre-trip state, by signature type.",
	}, []string{labelSignature})

	// restorationRegressionsTotal counts a signature re-tripping while its
	// own restoration ramp was still in progress (Phase Restoring, same
	// signature as the one already active) — the case PLAN.md §2.6 calls
	// "a regression during ramp re-trips immediately and resets to step
	// 0". Deliberately excludes a same-object signature handoff (a
	// different signature tripping) — that is a distinct, already-handled
	// case (forceCompleteOutgoingRestore), not a regression of the
	// outgoing signature's own ramp.
	restorationRegressionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cascade_restoration_regressions_total",
		Help: "Total times a signature re-tripped while its own restoration ramp was still in progress, by signature type.",
	}, []string{labelSignature})
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		signaturesDetectedTotal,
		mitigationPatchesAppliedTotal,
		restorationsCompletedTotal,
		restorationRegressionsTotal,
	)
}
