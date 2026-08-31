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
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// AnnotationOriginalTimeout is VirtualService-specific, same shape as
// AnnotationOriginalRetries: a JSON array, one entry per spec.http[] route,
// in the same order — a VirtualService can have several routes, and each
// one's pre-trip timeout is independent.
const AnnotationOriginalTimeout = "cascade.gideonsanni.dev/original-timeout"

// originalRouteTimeoutJSON captures one spec.http[i]'s pre-trip timeout
// state. Skipped mirrors originalRouteRetriesJSON's: a route with no
// destinations (Redirect, DirectResponse, or Delegate) has no meaningful
// timeout either, so it is never touched on trip and never needs restoring.
// Unset means the route had no explicit timeout pre-trip — Istio's own doc
// comment on this field just says "default is disabled", but Istio passes
// this straight through to Envoy's RouteAction.timeout, whose own default
// (confirmed against Envoy's docs, since this field carries no Istio-side
// default of its own to check against) is 15s.
type originalRouteTimeoutJSON struct {
	Skipped bool   `json:"skipped,omitempty"`
	Unset   bool   `json:"unset,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

// ApplyLatencyErrorTimeoutTrip is latency/error-cascade's secondary
// (PLAN.md §2.6): it sets route.timeout to latencyP99Ms — the policy's own
// CRD threshold, not a constant this package invents — on every forwarding
// route in vs, plus our annotations. This is an unconditional set, not
// min(existing, latencyP99Ms): the whole point of the backstop is "once
// this dependency is confirmed to be the kind of unhealthy that defines a
// latency/error cascade, no request to it should be allowed to run longer
// than the threshold that defines that cascade" — a pre-trip timeout that
// was already shorter than latencyP99Ms doesn't need loosening (a tighter
// pre-existing backstop is not a problem this trip needs to fix), and one
// that was longer or unset needs cutting down to the threshold. Match,
// route destinations, retries, fault injection, and any other per-route
// field are left as they were.
//
// Captured via this annotation's own presence, not managed-by's — the same
// reasoning ApplyLatencyErrorOutlierTrip's doc comment gives for
// DestinationRule, now also true for VirtualService: retry storm's own
// primary (retries.go) can manage the very same VirtualService (disjoint
// field set — retries vs. timeout), and if it trips first, managed-by is
// already ManagedByValue before latency/error's secondary ever captures its
// own baseline. See retries.go's own doc comment update (same slice) for
// the fix that makes retry storm's own capture check consistent with this.
func ApplyLatencyErrorTimeoutTrip(vs *networkingv1.VirtualService, latencyP99Ms int32) {
	if vs.Annotations == nil {
		vs.Annotations = map[string]string{}
	}
	if _, captured := vs.Annotations[AnnotationOriginalTimeout]; !captured {
		vs.Annotations[AnnotationOriginalTimeout] = snapshotRoutesTimeoutJSON(vs)
	}
	vs.Annotations[AnnotationManagedBy] = ManagedByValue

	tripTimeout := durationpb.New(time.Duration(latencyP99Ms) * time.Millisecond)
	for _, route := range vs.Spec.GetHttp() {
		if len(route.GetRoute()) == 0 {
			continue
		}
		route.Timeout = tripTimeout
	}
}

func snapshotRoutesTimeoutJSON(vs *networkingv1.VirtualService) string {
	routes := vs.Spec.GetHttp()
	snaps := make([]originalRouteTimeoutJSON, len(routes))
	for i, route := range routes {
		if len(route.GetRoute()) == 0 {
			snaps[i] = originalRouteTimeoutJSON{Skipped: true}
			continue
		}
		d := route.GetTimeout()
		if d == nil {
			snaps[i] = originalRouteTimeoutJSON{Unset: true}
			continue
		}
		snaps[i] = originalRouteTimeoutJSON{Timeout: d.AsDuration().String()}
	}
	b, err := json.Marshal(snaps)
	if err != nil {
		return "[]"
	}
	return string(b)
}
