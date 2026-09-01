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

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
	"github.com/gasthecreator/Cascade-Operator/internal/signatures"
)

// tetragonKernelEventCountQuery counts kernel-level kprobe events Tetragon
// captured for host's own workload in the last windowSeconds (PLAN.md §5
// Phase 11) — confirmed live that Tetragon's own /metrics endpoint exports
// tetragon_events_total{namespace,workload,pod,type,binary}, a real
// Prometheus counter, so this is one more PromQL query through the exact
// same metrics.Querier every other signature already uses, not a new
// client or a new kind of dependency.
//
// Deliberately not part of mesh.QueryBuilder: Tetragon observes the
// kernel, not mesh-proxy metrics, so this one query is identical
// regardless of which mesh a policy selects — it would be a strange fit
// forcing a per-mesh interface to carry a mesh-agnostic query.
//
// Scoped by namespace+workload, not pod name: pods churn (restarts,
// rollouts), but tetragon_events_total's workload label is stable — same
// convention this project already uses everywhere a host needs resolving
// to a live object (mitigation.ParseServiceFQDN, assuming the workload
// shares its Service's name).
//
// type="PROCESS_KPROBE" is Tetragon's own event-type label, not scoped to
// a specific kprobe or TracingPolicy — confirmed live that
// tetragon_events_total carries no policy/function label to disambiguate
// which kprobe fired (only tetragon_missed_*_probes_total, a different,
// diagnostic-only metric family, carries an attach/policy label). In this
// project's current state that is an acceptable, honestly-stated
// limitation, not a hidden assumption: demo/tetragon/tcp-reset-policy.yaml
// (via demo/internal/depsvc's /control/reset) is the only mechanism that
// currently produces real kprobe events at all — tcp-retransmit-policy.yaml
// remains genuinely unexercised (PLAN.md §5's own prior worklog) — so any
// PROCESS_KPROBE event observed for a demo workload right now is, in
// practice, a TCP reset. If a future slice adds a real packet-loss
// mechanism too, this query would need refining to disambiguate them (via
// Tetragon's own per-event JSON export rather than this aggregate
// counter), not assumed to still be precise as-is.
func tetragonKernelEventCountQuery(host string, windowSeconds int32) string {
	name, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		// Degrades to a query that can never match a real namespace/workload
		// label pair (Kubernetes object names never contain dots) rather
		// than a malformed query — same convention as
		// internal/mesh/linkerd's own unparseable-host handling.
		name, ns = host, host
	}
	return fmt.Sprintf(
		`sum(increase(tetragon_events_total{namespace=%q,workload=%q,type="PROCESS_KPROBE"}[%ds]))`,
		ns, name, windowSeconds,
	)
}

// applyKernelCorroboration is a best-effort enrichment of an
// already-tripped Verdict — never fails the caller's own detection: a
// query error, no data (Tetragon absent or not scraped by whichever
// Prometheus this policy is configured against), or a healthy zero count
// all degrade to "no corroboration this tick," not a reconcile error.
// Only called by the eval*/detect call sites below when v.Tripped is
// already true, since a zero/negative count is a no-op in
// signatures.ApplyKernelCorroboration anyway — skipping the query
// entirely on every healthy tick avoids tripling Prometheus load for a
// signal that can only ever matter once something else has already
// tripped.
func (r *CascadePolicyReconciler) applyKernelCorroboration(
	ctx context.Context,
	v signatures.Verdict,
	host string,
	windowSeconds int32,
) signatures.Verdict {
	log := logf.FromContext(ctx)
	if r.Metrics == nil {
		return v
	}
	snap, err := r.Metrics.Query(ctx, tetragonKernelEventCountQuery(host, windowSeconds))
	if err != nil {
		log.Info("kernel-event corroboration query failed; proceeding without it",
			"dependency", host, "error", err.Error())
		return v
	}
	count, ok := snapshotMax(snap)
	if !ok {
		return v
	}
	corroborated := signatures.ApplyKernelCorroboration(v, count)
	if corroborated.Confidence != v.Confidence {
		log.Info("verdict corroborated by kernel-level TCP disruption",
			"dependency", host, "kernelEvents", count,
			"confidenceBefore", v.Confidence, "confidenceAfter", corroborated.Confidence,
		)
	}
	return corroborated
}
