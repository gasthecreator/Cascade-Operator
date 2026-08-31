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
// Deliberately not shared even though both signatures' secondaries/
// primary touch the same sub-message on the same object kind: this
// signature only ever knows about, captures, and restores its own two
// fields (MaxRetries, Http1MaxPendingRequests), not fan-out's
// Http2MaxRequests or the sub-message as a whole. Http1MaxPendingRequests
// is also named by fan-out's primary — a same-field overlap PLAN.md §2.6's
// matrix lists but does not resolve; this slice implements the matrix as
// currently written and files that overlap in PROPOSALS.md rather than
// locking a direction here.
const AnnotationOriginalRetryConnectionPool = "cascade.gideonsanni.dev/original-retry-connection-pool"

// Trip-time values for retry storm's connectionPool.http secondary
// (PLAN.md §2.6: "connectionPool.http.maxRetries, http1MaxPendingRequests").
//
// TripRetryStormMaxRetries=0, not a tightened-but-nonzero value: Envoy's
// own circuit-breaker default for max_retries — confirmed against Envoy's
// actual proto doc (https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/circuit_breaker.proto,
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
//
// TripRetryStormMaxPendingRequests=1, not 0: this field is a general
// request bulkhead (not retry-specific — it caps *all* pending requests to
// the destination, retried or not), so it gets fan-out's own
// bulkhead-not-outage reasoning (connpool.go's TripHTTP1MaxPendingRequests
// doc comment) applied fresh here rather than reused directly: Envoy's own
// default when connectionPool.http is absent is 1024 (same source), and
// capping to exactly 1 rejects any burst beyond a single in-flight request
// immediately rather than piling more load onto a dependency already
// amplifying retries against itself — a bulkhead against the amplified
// *volume*, complementing the primary's cut to the amplification's
// *source*. A separate named constant from fan-out's
// TripHTTP1MaxPendingRequests on purpose, even though the values happen to
// coincide today: each signature's own trip value should be justifiable,
// and independently changeable, on its own terms.
const (
	TripRetryStormMaxRetries         = int32(0)
	TripRetryStormMaxPendingRequests = int32(1)
)

// originalRetryConnectionPoolJSON captures only the two fields this
// signature's secondary ever touches. Deliberately has no whole-block
// Unset flag, unlike originalConnectionPoolJSON (fan-out's twin, which
// owns the *entire* practically-used part of connectionPool.http for its
// own use case): MaxRetries and Http1MaxPendingRequests are plain proto3
// int32 scalars where 0 already means "not specified" at the wire level
// (same reasoning connpool.go's own doc comment gives for these exact
// fields), so capturing and restoring the literal scalar value is both
// necessary and sufficient — no separate "was the block absent" tracking
// needed. This also means restore never nils the surrounding http
// sub-message (contrast connpool_restore.go's clearConnectionPoolHTTP):
// with fan-out's Http2MaxRequests, and potentially a user-authored
// MaxRequestsPerConnection or IdleTimeout, possibly also live on that same
// sub-message, this signature must only ever read/write its own two
// fields and never clear or replace the message as a whole.
type originalRetryConnectionPoolJSON struct {
	MaxRetries              int32 `json:"maxRetries,omitempty"`
	Http1MaxPendingRequests int32 `json:"http1MaxPendingRequests,omitempty"`
}

// ApplyRetryStormConnectionPoolTrip mutates only connectionPool.http's
// MaxRetries and Http1MaxPendingRequests on dr (plus our annotations).
// Host, TLS, outlierDetection, connectionPool.tcp, and every other
// connectionPool.http field — including Http2MaxRequests, which fan-out's
// own primary also manages on this same sub-message — are left exactly as
// they were; see ensureConnectionPoolHTTP (connpool.go, shared) for why
// reading/creating the sub-message is safe to share across signatures
// while the fields written are not.
//
// The original annotation is captured only the first time *this* function
// touches dr — checked via AnnotationOriginalRetryConnectionPool's own
// presence, not managed-by's, the same defensive pattern every other trip
// function in this package now uses (outlier.go, connpool.go, retries.go,
// timeout.go): three other signature/field pairs can already set
// managed-by on this same DestinationRule (latency/error's
// outlierDetection, fan-out's connectionPool.http), and if any tripped
// first, managed-by is already ManagedByValue before this function ever
// captures its own baseline. Own-annotation-keyed capture is necessary
// and, for a *shared field* with fan-out (Http1MaxPendingRequests), not
// sufficient on its own — capturing "current" while the other signature
// still holds the object would snapshot *its* trip value as this
// signature's original. Reconcile's synchronous force-complete-on-handoff
// is what currently prevents that; whether the overlap should remain at
// all is the pending PROPOSALS.md entry, not a decision this function
// gets to make.
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
	http.Http1MaxPendingRequests = TripRetryStormMaxPendingRequests
}

func snapshotRetryConnectionPoolJSON(dr *networkingv1.DestinationRule) string {
	http := dr.Spec.GetTrafficPolicy().GetConnectionPool().GetHttp()
	snap := originalRetryConnectionPoolJSON{
		MaxRetries:              http.GetMaxRetries(),
		Http1MaxPendingRequests: http.GetHttp1MaxPendingRequests(),
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return "{}"
	}
	return string(b)
}
