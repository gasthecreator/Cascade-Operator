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

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// istioDefaultMaxRetries is the ramp target for a captured MaxRetries of 0
// (meaning "was 0 or absent pre-trip" — indistinguishable at the wire
// level, same reasoning connpool_restore.go's istioDefaultMaxPendingRequests
// doc comment gives for its own two fields). Sourced from Envoy's actual
// circuit-breaker proto doc, not the vendored istio.io/api v1.30.4
// DestinationRule field's own comment ("Defaults to 2^32-1") — confirmed
// those disagree and Envoy's is the one that governs real behavior, the
// same class of mismatch istioDefaultTimeout (timeout_restore.go) already
// found and resolved the same way for HTTPRoute.Timeout vs.
// RouteAction.timeout. See
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/circuit_breaker.proto:
// "max_retries ... If not specified, the default is 3."
const istioDefaultMaxRetries = int32(3)

func parseOriginalRetryConnectionPool(raw string) (originalRetryConnectionPoolJSON, error) {
	var orig originalRetryConnectionPoolJSON
	if raw == "" {
		return orig, fmt.Errorf("original-retry-connection-pool annotation is empty")
	}
	if err := json.Unmarshal([]byte(raw), &orig); err != nil {
		return orig, fmt.Errorf("parse original-retry-connection-pool: %w", err)
	}
	return orig, nil
}

// origRetryMaxRetriesTarget/origRetryMaxPendingTarget fall back to the
// Envoy/Istio implicit default whenever the captured field reads 0 — same
// rule connpool_restore.go's origMaxPendingTarget/origMaxRequestsTarget
// already apply to Http1MaxPendingRequests/Http2MaxRequests, extended to
// MaxRetries.
func origRetryMaxRetriesTarget(orig originalRetryConnectionPoolJSON) int32 {
	if orig.MaxRetries != 0 {
		return orig.MaxRetries
	}
	return istioDefaultMaxRetries
}

func origRetryMaxPendingTarget(orig originalRetryConnectionPoolJSON) int32 {
	if orig.Http1MaxPendingRequests != 0 {
		return orig.Http1MaxPendingRequests
	}
	return istioDefaultMaxPendingRequests
}

// applyInterpolatedRetryConnectionPool ramps MaxRetries/Http1MaxPendingRequests
// toward their restore targets. Only ever reads or writes these two fields
// on the http sub-message — never Http2MaxRequests (fan-out's own field on
// the same sub-message) and never the sub-message itself, so a concurrent
// (or, in the handoff case, just-restored) fan-out value on that sibling
// field is never touched by this function, in either direction.
func applyInterpolatedRetryConnectionPool(dr *networkingv1.DestinationRule, orig originalRetryConnectionPoolJSON, t float64) {
	http := ensureConnectionPoolHTTP(dr)
	http.MaxRetries = lerpI32(TripRetryStormMaxRetries, origRetryMaxRetriesTarget(orig), t)
	http.Http1MaxPendingRequests = lerpI32(TripRetryStormMaxPendingRequests, origRetryMaxPendingTarget(orig), t)
}

// applyOriginalRetryConnectionPool writes back the literal captured
// scalars (0 correctly means "restore to absent/default", per
// originalRetryConnectionPoolJSON's own doc comment) and, deliberately,
// never calls clearConnectionPoolHTTP or touches the surrounding
// sub-message: unlike fan-out's whole-block restore, this signature does
// not own the whole block and must never nil siblings it never captured.
func applyOriginalRetryConnectionPool(dr *networkingv1.DestinationRule, orig originalRetryConnectionPoolJSON) {
	http := ensureConnectionPoolHTTP(dr)
	http.MaxRetries = orig.MaxRetries
	http.Http1MaxPendingRequests = orig.Http1MaxPendingRequests
}

func stripRetryConnectionPoolAnnotations(dr *networkingv1.DestinationRule) {
	if dr.Annotations == nil {
		return
	}
	delete(dr.Annotations, AnnotationManagedBy)
	delete(dr.Annotations, AnnotationOriginalRetryConnectionPool)
	if len(dr.Annotations) == 0 {
		dr.Annotations = nil
	}
}

// ApplyRetryStormConnectionPoolRestoreStep writes interpolated
// MaxRetries/Http1MaxPendingRequests for restoreStep 0–3, or the stored
// original at step 4 — mirrors every other *RestoreStep function's step
// contract exactly, reusing the same restoreProgress(step) curve.
func ApplyRetryStormConnectionPoolRestoreStep(dr *networkingv1.DestinationRule, step int32) error {
	orig, err := parseOriginalRetryConnectionPool(dr.Annotations[AnnotationOriginalRetryConnectionPool])
	if err != nil {
		return err
	}
	if step >= RestoreFinalStep {
		applyOriginalRetryConnectionPool(dr, orig)
		return nil
	}
	applyInterpolatedRetryConnectionPool(dr, orig, restoreProgress(step))
	return nil
}

// CompleteRetryStormConnectionPoolRestore writes the stored original
// MaxRetries/Http1MaxPendingRequests and removes both operator
// annotations. Like every other stripAnnotations helper in this package,
// this unconditionally deletes the single shared AnnotationManagedBy key,
// not just this signature's own AnnotationOriginalRetryConnectionPool —
// safe only because the single-active-signature invariant (status.LastSignature,
// PLAN.md §2.6) guarantees this function never runs while a *different*
// signature is still the one actively managing this same object; a
// handoff always force-completes whichever signature is outgoing first
// (Reconcile, restore.go's forceCompleteOutgoingRestore) before any other
// signature's trip — including this one's — ever sets managed-by again.
func CompleteRetryStormConnectionPoolRestore(dr *networkingv1.DestinationRule) error {
	orig, err := parseOriginalRetryConnectionPool(dr.Annotations[AnnotationOriginalRetryConnectionPool])
	if err != nil {
		return err
	}
	applyOriginalRetryConnectionPool(dr, orig)
	stripRetryConnectionPoolAnnotations(dr)
	return nil
}
