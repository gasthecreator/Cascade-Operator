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

package mitigation

import (
	"encoding/json"
	"fmt"

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// AnnotationOriginalRetries is VirtualService-specific: the outlier-detection
// original annotation only makes sense for a DestinationRule, and a
// VirtualService can have several http[] routes, so this stores a JSON array
// (one entry per route, same order as spec.http) rather than a single object.
const AnnotationOriginalRetries = "cascade.gideonsanni.dev/original-retries"

// TripRetryAttempts is the retry-storm primary (PLAN.md §2.6): cut
// retries.attempts to 0, not 1. Istio's implicit cluster-wide default (no
// explicit retries block) is attempts=2 on connect-failure/refused-stream/
// unavailable/cancelled — confirmed against the vendored istio.io/api v1.30.4
// proto doc comment (the same version pinned on the Kind cluster) and Istio's
// own reference docs, since the live cluster was unreachable when this was
// written (Docker Desktop's VM had OOM-killed around the time of writing;
// see the worklog). 1 would still let a request through a second time —
// under the dest:source ratio detector, a 2x amplification can itself sit at
// or above a low retryStormMultiplier, so it would not reliably stop the
// storm. 0 fully disables retries, matching the aggressive-trip/gradual-
// restore pattern the outlier-detection primary already established.
const TripRetryAttempts = int32(0)

// originalRouteRetriesJSON captures one spec.http[i]'s pre-trip retries
// state. Skipped is true for a route with no destinations (Redirect,
// DirectResponse, or Delegate) — retries are meaningless there, so it is
// never touched on trip and never needs restoring. Unset is true when the
// route had no explicit retries block at all, i.e. it was relying on
// Istio's implicit cluster-wide default rather than an authored policy.
type originalRouteRetriesJSON struct {
	Skipped       bool   `json:"skipped,omitempty"`
	Unset         bool   `json:"unset,omitempty"`
	Attempts      int32  `json:"attempts,omitempty"`
	RetryOn       string `json:"retryOn,omitempty"`
	PerTryTimeout string `json:"perTryTimeout,omitempty"`
	Backoff       string `json:"backoff,omitempty"`
}

// ApplyRetryStormTrip sets retries.attempts to 0 on every forwarding route
// in vs (plus our annotations) and clears that route's retryOn/
// perTryTimeout/backoff (PROPOSALS.md, approved 2026-08-30). Match, route
// destinations, timeout, fault injection, and any other per-route field are
// left as they were. Clearing the other retry-policy fields alongside
// attempts:0 is not optional: Istio's validating webhook rejects
// attempts:0 combined with a non-empty retryOn/perTryTimeout/backoff
// outright ("http retry policy configured when attempts are set to 0
// (disabled)"), confirmed live against a route that already had them —
// exactly the case a real retry storm's pre-existing policy always is,
// since retryOn being set lenient is what let the storm amplify in the
// first place. Nothing is lost by clearing them here: the full pre-trip
// block (including these three fields) is captured in
// AnnotationOriginalRetries before this loop runs, and
// applyOriginalRetries/CompleteRetryStormRestore restore that whole
// captured block, not just attempts, at completion. Every http[] route gets
// an explicit retries block — including routes that had none — because
// Istio's implicit default (attempts=2) would otherwise keep retrying
// unmitigated on exactly the routes this patch is meant to stop.
//
// Captured via this annotation's own presence, not managed-by's
// (originally written the other way; fixed in the same slice that added
// latency/error-cascade's VirtualService secondary, timeout.go). VS is now
// a shared object kind across two signatures — retry storm's own retries
// here, latency/error-cascade's timeout there, disjoint fields — the same
// shape ApplyLatencyErrorOutlierTrip's doc comment already worked through
// for DestinationRule. Keying off managed-by alone would treat "already
// managed by a *different* signature's own secondary" as "already managed
// by me, skip capture", losing the true pre-trip retries block. Safe in
// practice today because Reconcile's signature-handoff force-complete
// always fully strips the outgoing signature's own annotations before a
// different signature's trip ever runs against the same object (PLAN.md
// §2.6) — but that safety depends on force-complete always running
// correctly, and this check should hold on its own regardless.
//
// The in-memory Attempts=0 write is not what reaches the API server:
// HTTPRetry.Attempts is a plain int32 with json:"attempts,omitempty", so a
// typed Update() strips the zero before it hits the wire (PROPOSALS.md,
// approved 2026-08-30). Callers must apply RetryStormAttemptsJSONPatch
// via client.Patch rather than client.Update. This function still mutates
// the struct so fake-client tests and later restore reads see the trip
// values; the patch payload is what actually transmits them.
func ApplyRetryStormTrip(vs *networkingv1.VirtualService) {
	if vs.Annotations == nil {
		vs.Annotations = map[string]string{}
	}
	if _, captured := vs.Annotations[AnnotationOriginalRetries]; !captured {
		vs.Annotations[AnnotationOriginalRetries] = snapshotRoutesRetriesJSON(vs)
	}
	vs.Annotations[AnnotationManagedBy] = ManagedByValue

	for _, route := range vs.Spec.GetHttp() {
		if len(route.GetRoute()) == 0 {
			continue
		}
		if route.Retries == nil {
			route.Retries = &apinet.HTTPRetry{}
		}
		route.Retries.Attempts = TripRetryAttempts
		route.Retries.RetryOn = ""
		route.Retries.PerTryTimeout = nil
		route.Retries.Backoff = nil
	}
}

// RetryStormAttemptsJSONPatch is the RFC 6902 JSON Patch that puts an
// explicit "attempts":0 on every forwarding route (and writes this
// signature's annotations). Built from maps, not the typed HTTPRetry
// struct, so encoding/json cannot strip the zero via omitempty. JSON
// Patch rather than merge-patch: spec.http is an array, and a JSON merge
// patch would replace the whole list. Each retries op replaces that
// route's retries object with {attempts:0}, which is also what clears
// retryOn/perTryTimeout/backoff for the webhook.
func RetryStormAttemptsJSONPatch(vs *networkingv1.VirtualService) []byte {
	ops := make([]map[string]any, 0, 1+len(vs.Spec.GetHttp()))
	if vs.Annotations != nil {
		ops = append(ops, map[string]any{
			"op":    "add",
			"path":  "/metadata/annotations",
			"value": vs.Annotations,
		})
	}
	for i, route := range vs.Spec.GetHttp() {
		if len(route.GetRoute()) == 0 {
			continue
		}
		ops = append(ops, map[string]any{
			"op":   "add",
			"path": fmt.Sprintf("/spec/http/%d/retries", i),
			"value": map[string]any{
				"attempts": TripRetryAttempts,
			},
		})
	}
	b, err := json.Marshal(ops)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func snapshotRoutesRetriesJSON(vs *networkingv1.VirtualService) string {
	routes := vs.Spec.GetHttp()
	snaps := make([]originalRouteRetriesJSON, len(routes))
	for i, route := range routes {
		if len(route.GetRoute()) == 0 {
			snaps[i] = originalRouteRetriesJSON{Skipped: true}
			continue
		}
		r := route.GetRetries()
		if r == nil {
			snaps[i] = originalRouteRetriesJSON{Unset: true}
			continue
		}
		snap := originalRouteRetriesJSON{Attempts: r.GetAttempts(), RetryOn: r.GetRetryOn()}
		if d := r.GetPerTryTimeout(); d != nil {
			snap.PerTryTimeout = d.AsDuration().String()
		}
		if d := r.GetBackoff(); d != nil {
			snap.Backoff = d.AsDuration().String()
		}
		snaps[i] = snap
	}
	b, err := json.Marshal(snaps)
	if err != nil {
		return "[]"
	}
	return string(b)
}
