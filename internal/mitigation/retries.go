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

// ApplyRetryStormTrip mutates only retries.attempts on every forwarding
// route in vs (plus our annotations). Match, route destinations, timeout,
// fault injection, and any other per-route field are left as they were; if
// a route already had an explicit retries block, its retryOn/perTryTimeout/
// backoff are preserved and only attempts changes. Every http[] route gets
// an explicit retries block — including routes that had none — because
// Istio's implicit default (attempts=2) would otherwise keep retrying
// unmitigated on exactly the routes this patch is meant to stop. The
// original annotation is written only when managed-by is not already set.
func ApplyRetryStormTrip(vs *networkingv1.VirtualService) {
	if vs.Annotations == nil {
		vs.Annotations = map[string]string{}
	}
	if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
		vs.Annotations[AnnotationOriginalRetries] = snapshotRoutesRetriesJSON(vs)
		vs.Annotations[AnnotationManagedBy] = ManagedByValue
	}

	for _, route := range vs.Spec.GetHttp() {
		if len(route.GetRoute()) == 0 {
			continue
		}
		if route.Retries == nil {
			route.Retries = &apinet.HTTPRetry{}
		}
		route.Retries.Attempts = TripRetryAttempts
	}
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
