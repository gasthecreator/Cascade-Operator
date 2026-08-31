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

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// AnnotationOriginalRetryConnectionPool is retry storm's own
// connectionPool.http original-value annotation — its own key and its own
// JSON shape, not a reuse of fan-out's AnnotationOriginalConnectionPool.
// This signature only ever captures and restores MaxRetries; fan-out's
// primary owns Http1MaxPendingRequests/Http2MaxRequests on the same
// sub-message (PLAN.md §2.6, overlap resolved 2026-08-30, direction 2).
const AnnotationOriginalRetryConnectionPool = "cascade.gideonsanni.dev/original-retry-connection-pool"

// TripRetryStormMaxRetries is the trip-time value for retry storm's
// connectionPool.http.maxRetries secondary (PLAN.md §2.6). 1, not 0:
// Istio Pilot's applyConnectionPool (istio/istio 1.30.4
// pilot/pkg/networking/core/cluster_traffic_policy.go L118–L120) only
// copies MaxRetries into Envoy's circuit_breakers when the value is > 0 —
// an explicit DestinationRule 0 is treated as unset and Envoy keeps
// Pilot's default math.MaxUint32 (4294967295). Confirmed live and in
// source (PROPOSALS.md, approved 2026-08-30, direction 2). 1 is the
// smallest value that actually reaches Envoy as a real outstanding-retry
// circuit-breaker cap. This is not "allow one retry per request" — the
// primary (retries.go) still fully disables retries.attempts; this
// secondary caps how many retries Envoy will allow outstanding to the
// cluster at once if anything is still retrying. Envoy's own unset
// default for max_retries is 3 (circuit_breaker.proto); Istio's
// DestinationRule comment ("Defaults to 2^32-1") describes Pilot's CDS
// default, not Envoy's.
const TripRetryStormMaxRetries = int32(1)

// originalRetryConnectionPoolJSON captures only MaxRetries, the one field
// this signature's secondary ever touches. Deliberately has no whole-block
// Unset flag, unlike originalConnectionPoolJSON (fan-out's twin, which
// owns the *entire* practically-used part of connectionPool.http for its
// own use case): MaxRetries is a plain proto3 int32 scalar where 0 already
// means "not specified" at the wire level (same reasoning connpool.go's
// own doc comment gives for these exact fields), so capturing and
// restoring the literal scalar is both necessary and sufficient. Sibling
// fields (fan-out's Http1MaxPendingRequests/Http2MaxRequests, a
// user-authored MaxRequestsPerConnection or IdleTimeout) are never
// captured or written. Restore *does* prune the http sub-message when it
// is empty after writing MaxRetries back to 0 — see
// applyOriginalRetryConnectionPool.
type originalRetryConnectionPoolJSON struct {
	MaxRetries int32 `json:"maxRetries,omitempty"`
}

// ApplyRetryStormConnectionPoolTrip mutates only connectionPool.http's
// MaxRetries on dr (plus our annotations). Host, TLS, outlierDetection,
// connectionPool.tcp, and every other connectionPool.http field —
// including Http1MaxPendingRequests and Http2MaxRequests, which fan-out's
// own primary manages on this same sub-message — are left exactly as they
// were; see ensureConnectionPoolHTTP (connpool.go, shared) for why
// reading/creating the sub-message is safe to share across signatures
// while the fields written are not.
//
// The original annotation is captured only the first time *this* function
// touches dr — checked via AnnotationOriginalRetryConnectionPool's own
// presence, not managed-by's, the same defensive pattern every other trip
// function in this package now uses (outlier.go, connpool.go, retries.go,
// timeout.go): other signatures can already set managed-by on this same
// DestinationRule (latency/error's outlierDetection, fan-out's
// connectionPool.http), and if any tripped first, managed-by is already
// ManagedByValue before this function ever captures its own baseline.
// With the overlap resolved, MaxRetries is disjoint from every other
// signature's fields, so capturing "current" is always this field's true
// original — force-complete-on-handoff is no longer load-bearing for this
// field's data integrity, only for not leaving a trip-time MaxRetries
// (and the annotation) behind when a different signature adopts the object.
//
// Callers must apply RetryStormMaxRetriesMergePatch via client.Patch
// rather than client.Update — same write path as the primary's attempts:0
// JSON Patch (PROPOSALS.md, approved 2026-08-30). TripRetryStormMaxRetries
// is now 1 (non-zero), so a typed Update would serialize it, but the
// patch path stays: already reviewed, keeps both of this signature's
// trip writes on one mechanism, and would still be required if the trip
// value ever returned to a JSON-zero. This function still mutates the
// struct so fake-client tests and later restore reads see the trip value.
func ApplyRetryStormConnectionPoolTrip(dr *networkingv1.DestinationRule) {
	if dr.Annotations == nil {
		dr.Annotations = map[string]string{}
	}
	if _, captured := dr.Annotations[AnnotationOriginalRetryConnectionPool]; !captured {
		dr.Annotations[AnnotationOriginalRetryConnectionPool] = snapshotRetryConnectionPoolJSON(dr)
	}
	dr.Annotations[AnnotationManagedBy] = ManagedByValue

	http := ensureConnectionPoolHTTP(dr)
	http.MaxRetries = TripRetryStormMaxRetries
}

// RetryStormMaxRetriesMergePatch is the JSON merge patch that puts
// TripRetryStormMaxRetries on connectionPool.http (and writes this
// signature's annotations). Built from maps (same mechanism as when the
// trip value was 0 and omitempty would have stripped a typed Update).
// Merge patch is safe here: trafficPolicy/connectionPool/http are nested
// objects, so this merges maxRetries in without replacing sibling fields
// (fan-out's http1/http2, outlierDetection, TLS).
func RetryStormMaxRetriesMergePatch(dr *networkingv1.DestinationRule) []byte {
	anns := map[string]string{}
	if dr.Annotations != nil {
		if v, ok := dr.Annotations[AnnotationManagedBy]; ok {
			anns[AnnotationManagedBy] = v
		}
		if v, ok := dr.Annotations[AnnotationOriginalRetryConnectionPool]; ok {
			anns[AnnotationOriginalRetryConnectionPool] = v
		}
	}
	patch := map[string]any{
		"metadata": map[string]any{"annotations": anns},
		jsonKeySpec: map[string]any{
			"trafficPolicy": map[string]any{
				"connectionPool": map[string]any{
					jsonKeyHTTP: map[string]any{
						"maxRetries": TripRetryStormMaxRetries,
					},
				},
			},
		},
	}
	b, err := json.Marshal(patch)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func snapshotRetryConnectionPoolJSON(dr *networkingv1.DestinationRule) string {
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	snap := originalRetryConnectionPoolJSON{
		MaxRetries: http.GetMaxRetries(),
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return "{}"
	}
	return string(b)
}
