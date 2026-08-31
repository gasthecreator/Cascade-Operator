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

// AnnotationOriginalConnectionPool is fan-out's original-value annotation —
// its own key, not a reuse of AnnotationOriginalOutlier: that JSON shape is
// specific to outlierDetection's fields and doesn't fit connectionPool.http's.
// OriginalConnectionPoolUnsetJSON is the sentinel stored when
// trafficPolicy.connectionPool.http was nil before the first patch, same
// role as OriginalOutlierUnsetJSON.
const (
	AnnotationOriginalConnectionPool = "cascade.gideonsanni.dev/original-connection-pool"
	OriginalConnectionPoolUnsetJSON  = `{"unset":true}`
)

// Trip-time connection pool bulkhead. Envoy's own defaults for both fields
// (when connectionPool.http is absent entirely) are 1024 — see
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/circuit_breaker.proto,
// "max_pending_requests"/"max_requests": "If not specified, the default is
// 1024." Capping to 1 caps in-flight calls to the dependency at exactly one
// at a time: any burst beyond that (the fan-out signature's own signal) is
// rejected immediately (503 overflow) rather than piling requests onto an
// already-degraded downstream. 1, not 0: 0 would mean no requests get
// through at all — a full outage of the dependency, not a bulkhead — which
// is a materially different mitigation than "cap in-flight calls" (§2.6).
// This mirrors outlier.go's "tighten, don't fully disable" choice
// (TripConsecutive5xx=3, not 1) rather than retries.go's "fully disable"
// choice (TripRetryAttempts=0) — a bulkhead's job is capping concurrency,
// not cutting the dependency off entirely.
const (
	TripHTTP1MaxPendingRequests = int32(1)
	TripHTTP2MaxRequests        = int32(1)
)

// originalConnectionPoolJSON is the restore-slice contract stored on first
// patch. Unset means trafficPolicy.connectionPool.http was nil before we
// touched it. Http1MaxPendingRequests/Http2MaxRequests are plain proto3
// int32 scalars (unlike outlierDetection's Consecutive_5XxErrors, which is a
// nullable wrapper): there is no wire-level way to distinguish "explicitly
// authored as 0" from "not specified" for these two fields specifically —
// Envoy's own doc comment confirms 0 and absent mean the same thing ("If not
// specified, the default is 1024"). So, unlike originalRouteRetriesJSON
// (which needs an explicit per-route Unset because Attempts=0 inside an
// existing retries block is a real, distinct state from no block at all),
// this type only needs Unset at the whole-block level, for deciding whether
// to clear connectionPool.http entirely on final restore versus leave it
// present with these two fields written back.
type originalConnectionPoolJSON struct {
	Unset                   bool  `json:"unset,omitempty"`
	Http1MaxPendingRequests int32 `json:"http1MaxPendingRequests,omitempty"`
	Http2MaxRequests        int32 `json:"http2MaxRequests,omitempty"`
}

// ApplyFanOutConnectionPoolTrip mutates only connectionPool.http's
// http1MaxPendingRequests/http2MaxRequests on dr (plus our annotations).
// Host, TLS, outlierDetection, connectionPool.tcp,
// connectionPool.http.maxRequestsPerConnection, subsets, and other fields
// are left as they were. The original annotation is captured only the first
// time *this* function touches dr — checked via
// AnnotationOriginalConnectionPool's own presence, not managed-by: latency/
// error-cascade can also manage this same DestinationRule (disjoint field
// set, outlierDetection), and if it trips first, managed-by is already
// ManagedByValue before fan-out ever captures its own connectionPool.http
// baseline. Keying off managed-by alone would then skip the capture and
// lose the true pre-trip connectionPool state — this is the exact bug this
// slice's own cross-signature test caught; see PROPOSALS.md for the fuller
// writeup of what is (and isn't) still a risk after this fix.
func ApplyFanOutConnectionPoolTrip(dr *networkingv1.DestinationRule) {
	if dr.Annotations == nil {
		dr.Annotations = map[string]string{}
	}
	if _, captured := dr.Annotations[AnnotationOriginalConnectionPool]; !captured {
		dr.Annotations[AnnotationOriginalConnectionPool] = snapshotConnectionPoolJSON(dr)
	}
	dr.Annotations[AnnotationManagedBy] = ManagedByValue

	http := ensureConnectionPoolHTTP(dr)
	http.Http1MaxPendingRequests = TripHTTP1MaxPendingRequests
	http.Http2MaxRequests = TripHTTP2MaxRequests
}

func snapshotConnectionPoolJSON(dr *networkingv1.DestinationRule) string {
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	if http == nil {
		return OriginalConnectionPoolUnsetJSON
	}
	snap := originalConnectionPoolJSON{
		Http1MaxPendingRequests: http.GetHttp1MaxPendingRequests(),
		Http2MaxRequests:        http.GetHttp2MaxRequests(),
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return OriginalConnectionPoolUnsetJSON
	}
	return string(b)
}

func ensureConnectionPoolHTTP(dr *networkingv1.DestinationRule) *apinet.ConnectionPoolSettings_HTTPSettings {
	if dr.Spec.TrafficPolicy == nil {
		dr.Spec.TrafficPolicy = &apinet.TrafficPolicy{}
	}
	if dr.Spec.TrafficPolicy.ConnectionPool == nil {
		dr.Spec.TrafficPolicy.ConnectionPool = &apinet.ConnectionPoolSettings{}
	}
	if dr.Spec.TrafficPolicy.ConnectionPool.Http == nil {
		dr.Spec.TrafficPolicy.ConnectionPool.Http = &apinet.ConnectionPoolSettings_HTTPSettings{}
	}
	return dr.Spec.TrafficPolicy.ConnectionPool.Http
}
