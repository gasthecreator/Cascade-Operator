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
// connectionPool.http.maxRetries secondary (PLAN.md §2.6). 0, not a
// tightened-but-nonzero value: Envoy's own circuit-breaker default for
// max_retries — confirmed against Envoy's actual proto doc
// (https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/circuit_breaker.proto,
// "If not specified, the default is 3"; the vendored istio.io/api v1.30.4
// DestinationRule field's own comment, "Defaults to 2^32-1", describes a
// different thing and is not the number this package's restore target
// should use, same class of doc-comment mismatch the timeout secondary's
// istioDefaultTimeout already worked through for HTTPRoute.Timeout) —
// caps the number of *retries* Envoy will allow outstanding to this
// cluster at once, a narrower, retry-specific circuit breaker, not a
// general request bulkhead. Retry storm's primary (retries.go) already
// fully disables retries.attempts on every managed route; capping this
// field to a nonzero "tightened" value here would be internally
// inconsistent with that choice — the whole point of this signature's
// mitigation is eliminating retry-driven amplification, not moderating
// it, so this secondary backs the primary up with the same all-the-way
// value rather than contradicting it with a partial one.
const TripRetryStormMaxRetries = int32(0)

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
// field's data integrity, only for not leaving a trip-time MaxRetries=0
// (and the annotation) behind when a different signature adopts the object.
//
// The in-memory MaxRetries=0 write is not what reaches the API server:
// HTTPSettings.MaxRetries is a plain int32 with json:"maxRetries,omitempty",
// so a typed Update() strips the zero before it hits the wire
// (PROPOSALS.md, approved 2026-08-30). Callers must apply
// RetryStormMaxRetriesMergePatch via client.Patch rather than
// client.Update. This function still mutates the struct so fake-client
// tests and later restore reads see the trip value; the patch payload is
// what actually transmits it.
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

// RetryStormMaxRetriesMergePatch is the JSON merge patch that puts an
// explicit "maxRetries":0 on connectionPool.http (and writes this
// signature's annotations). Built from maps, not the typed HTTPSettings
// struct, so encoding/json cannot strip the zero via omitempty. Merge
// patch is safe here: trafficPolicy/connectionPool/http are nested
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
		"spec": map[string]any{
			"trafficPolicy": map[string]any{
				"connectionPool": map[string]any{
					"http": map[string]any{
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
