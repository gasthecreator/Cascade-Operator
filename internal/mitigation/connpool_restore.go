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

// istioDefaultMaxPendingRequests/istioDefaultMaxRequests are the ramp
// targets for a connectionPool.http that was Unset (or whose captured field
// read 0 — see originalConnectionPoolJSON's doc comment on why those are the
// same state here) pre-trip. Sourced from Envoy's own circuit-breaker proto
// doc comment (same "If not specified, the default is 1024" sourcing this
// file's trip constants cite), mirroring istioDefaultRetryAttempts's role in
// retry_restore.go.
const (
	istioDefaultMaxPendingRequests = int32(1024)
	istioDefaultMaxRequests        = int32(1024)
)

func parseOriginalConnectionPool(raw string) (originalConnectionPoolJSON, error) {
	var orig originalConnectionPoolJSON
	if raw == "" {
		return orig, fmt.Errorf("original-connection-pool annotation is empty")
	}
	if err := json.Unmarshal([]byte(raw), &orig); err != nil {
		return orig, fmt.Errorf("parse original-connection-pool: %w", err)
	}
	return orig, nil
}

// origMaxPendingTarget/origMaxRequestsTarget fall back to the Istio/Envoy
// implicit default whenever the captured field reads 0 — which is correct
// in all cases, not an approximation limited to the Unset (whole-block-nil)
// case: these are plain proto3 scalars, so 0 is indistinguishable from "not
// specified" at the wire level regardless of whether the surrounding
// connectionPool.http block existed for some other reason (e.g.
// maxRequestsPerConnection set, http1/http2 both left at their default).
func origMaxPendingTarget(orig originalConnectionPoolJSON) int32 {
	if orig.Http1MaxPendingRequests != 0 {
		return orig.Http1MaxPendingRequests
	}
	return istioDefaultMaxPendingRequests
}

func origMaxRequestsTarget(orig originalConnectionPoolJSON) int32 {
	if orig.Http2MaxRequests != 0 {
		return orig.Http2MaxRequests
	}
	return istioDefaultMaxRequests
}

func applyInterpolatedConnectionPool(dr *networkingv1.DestinationRule, orig originalConnectionPoolJSON, t float64) {
	http := ensureConnectionPoolHTTP(dr)
	http.Http1MaxPendingRequests = lerpI32(TripHTTP1MaxPendingRequests, origMaxPendingTarget(orig), t)
	http.Http2MaxRequests = lerpI32(TripHTTP2MaxRequests, origMaxRequestsTarget(orig), t)
}

func applyOriginalConnectionPool(dr *networkingv1.DestinationRule, orig originalConnectionPoolJSON) {
	if orig.Unset {
		clearConnectionPoolHTTP(dr)
		return
	}
	http := ensureConnectionPoolHTTP(dr)
	http.Http1MaxPendingRequests = orig.Http1MaxPendingRequests
	http.Http2MaxRequests = orig.Http2MaxRequests
}

// clearConnectionPoolHTTP removes only the http sub-message, then cascades
// the same "clear the parent if it is now empty" cleanup outlier.go's
// clearOutlierDetection/trafficPolicyEmpty already established:
// connectionPool.tcp survives untouched; connectionPool itself is nilled
// only if tcp is also absent; trafficPolicyEmpty (shared, restore.go) is
// reused as-is to decide whether trafficPolicy itself should be nilled too.
func clearConnectionPoolHTTP(dr *networkingv1.DestinationRule) {
	tp := dr.Spec.TrafficPolicy
	if tp == nil || tp.ConnectionPool == nil {
		return
	}
	tp.ConnectionPool.Http = nil
	if connectionPoolEmpty(tp.ConnectionPool) {
		tp.ConnectionPool = nil
	}
	if trafficPolicyEmpty(tp) {
		dr.Spec.TrafficPolicy = nil
	}
}

func connectionPoolEmpty(cp *apinet.ConnectionPoolSettings) bool {
	if cp == nil {
		return true
	}
	return cp.Tcp == nil && cp.Http == nil
}

func stripFanOutAnnotations(dr *networkingv1.DestinationRule) {
	if dr.Annotations == nil {
		return
	}
	delete(dr.Annotations, AnnotationManagedBy)
	delete(dr.Annotations, AnnotationOriginalConnectionPool)
	if len(dr.Annotations) == 0 {
		dr.Annotations = nil
	}
}

// ApplyFanOutConnectionPoolRestoreStep writes interpolated
// http1MaxPendingRequests/http2MaxRequests for restoreStep 0–3, or the
// stored original at step 4 — mirrors ApplyLatencyErrorOutlierRestoreStep's
// and ApplyRetryStormRestoreStep's step contract exactly, reusing the same
// restoreProgress(step) curve.
func ApplyFanOutConnectionPoolRestoreStep(dr *networkingv1.DestinationRule, step int32) error {
	orig, err := parseOriginalConnectionPool(dr.Annotations[AnnotationOriginalConnectionPool])
	if err != nil {
		return err
	}
	if step >= RestoreFinalStep {
		applyOriginalConnectionPool(dr, orig)
		return nil
	}
	applyInterpolatedConnectionPool(dr, orig, restoreProgress(step))
	return nil
}

// CompleteFanOutConnectionPoolRestore writes the stored original (or clears
// connectionPool.http if it was unset) and removes both operator
// annotations.
func CompleteFanOutConnectionPoolRestore(dr *networkingv1.DestinationRule) error {
	orig, err := parseOriginalConnectionPool(dr.Annotations[AnnotationOriginalConnectionPool])
	if err != nil {
		return err
	}
	applyOriginalConnectionPool(dr, orig)
	stripFanOutAnnotations(dr)
	return nil
}
