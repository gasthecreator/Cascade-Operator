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
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// istioDefaultTimeout is the ramp target for a route whose original
// snapshot is Unset. Istio's own doc comment on HTTPRoute.Timeout just says
// "default is disabled" (no numeric default of Istio's own) — but Istio
// passes this field straight through to Envoy's RouteAction.timeout, whose
// documented default when unspecified is 15s (Envoy's RouteAction proto doc
// and https://www.envoyproxy.io/docs/envoy/latest/faq/configuration/timeouts,
// both confirmed 2026-08-30). Same role as istioDefaultRetryAttempts:
// something finite to ramp *toward* mid-restore for a route that relied on
// the implicit default pre-trip; the block is still cleared entirely (not
// written as this value) at true completion — same rule as everywhere else.
const istioDefaultTimeout = 15 * time.Second

func parseOriginalTimeout(raw string) ([]originalRouteTimeoutJSON, error) {
	if raw == "" {
		return nil, fmt.Errorf("original-timeout annotation is empty")
	}
	var snaps []originalRouteTimeoutJSON
	if err := json.Unmarshal([]byte(raw), &snaps); err != nil {
		return nil, fmt.Errorf("parse original-timeout: %w", err)
	}
	return snaps, nil
}

// applyInterpolatedTimeout ramps route.timeout on every non-skipped route
// toward its restore target: the captured original for a route that had an
// explicit timeout, or istioDefaultTimeout for a route that relied on the
// implicit default pre-trip — mirrors applyInterpolatedRetries exactly, one
// field over. tripTimeout is always the *fixed* trip-time value (latencyP99Ms
// at the moment of trip), not the route's current live value: restoreProgress
// computes t as total progress from trip toward original, recomputed fresh
// every step, so interpolating from anything other than the original fixed
// trip anchor would compound across steps instead of producing the same
// linear-in-t ramp every other field in this package uses.
func applyInterpolatedTimeout(vs *networkingv1.VirtualService, snaps []originalRouteTimeoutJSON, t float64, tripTimeout time.Duration) {
	routes := vs.Spec.GetHttp()
	for i, route := range routes {
		if i >= len(snaps) {
			break
		}
		snap := snaps[i]
		if snap.Skipped || route.Timeout == nil {
			continue
		}
		target := istioDefaultTimeout
		if !snap.Unset {
			target = durationOrDefault(snap.Timeout, istioDefaultTimeout)
		}
		route.Timeout = durationpb.New(lerpDuration(tripTimeout, target, t))
	}
}

// applyOriginalTimeout writes each route's exact pre-trip state: Unset gets
// its timeout cleared entirely (nil — "disabled", not some invented
// duration), an explicit route gets its captured duration back verbatim,
// and Skipped is left alone since it was never touched on trip.
func applyOriginalTimeout(vs *networkingv1.VirtualService, snaps []originalRouteTimeoutJSON) {
	routes := vs.Spec.GetHttp()
	for i, route := range routes {
		if i >= len(snaps) {
			break
		}
		snap := snaps[i]
		if snap.Skipped {
			continue
		}
		if snap.Unset {
			route.Timeout = nil
			continue
		}
		route.Timeout = durationpb.New(durationOrDefault(snap.Timeout, istioDefaultTimeout))
	}
}

func stripTimeoutAnnotations(vs *networkingv1.VirtualService) {
	if vs.Annotations == nil {
		return
	}
	delete(vs.Annotations, AnnotationManagedBy)
	delete(vs.Annotations, AnnotationOriginalTimeout)
	if len(vs.Annotations) == 0 {
		vs.Annotations = nil
	}
}

// ApplyLatencyErrorTimeoutRestoreStep writes interpolated per-route
// route.timeout for restoreStep 0–3, or each route's stored original at
// step 4 — mirrors ApplyRetryStormRestoreStep's step contract exactly,
// reusing the same restoreProgress(step) curve. tripLatencyP99Ms is the
// policy's own threshold at the time of trip (the same value
// ApplyLatencyErrorTimeoutTrip wrote to route.timeout) — unlike the other
// signatures' restore-step functions, this one needs it explicitly: the
// trip value here is a per-policy CRD field, not a package constant, and
// it is not itself part of the original-state snapshot (that annotation
// only ever holds the pre-trip state, not the trip-time one).
func ApplyLatencyErrorTimeoutRestoreStep(vs *networkingv1.VirtualService, step int32, tripLatencyP99Ms int32) error {
	snaps, err := parseOriginalTimeout(vs.Annotations[AnnotationOriginalTimeout])
	if err != nil {
		return err
	}
	if step >= RestoreFinalStep {
		applyOriginalTimeout(vs, snaps)
		return nil
	}
	tripTimeout := time.Duration(tripLatencyP99Ms) * time.Millisecond
	applyInterpolatedTimeout(vs, snaps, restoreProgress(step), tripTimeout)
	return nil
}

// CompleteLatencyErrorTimeoutRestore writes each route's stored original
// timeout state and removes both operator annotations — the VirtualService
// secondary's twin of CompleteLatencyErrorOutlierRestore.
func CompleteLatencyErrorTimeoutRestore(vs *networkingv1.VirtualService) error {
	snaps, err := parseOriginalTimeout(vs.Annotations[AnnotationOriginalTimeout])
	if err != nil {
		return err
	}
	applyOriginalTimeout(vs, snaps)
	stripTimeoutAnnotations(vs)
	return nil
}
