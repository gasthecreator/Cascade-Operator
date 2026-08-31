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

func origRetryMaxRetriesTarget(orig originalRetryConnectionPoolJSON) int32 {
	if orig.MaxRetries != 0 {
		return orig.MaxRetries
	}
	return istioDefaultMaxRetries
}

// applyInterpolatedRetryConnectionPool ramps MaxRetries toward its restore
// target. Only ever reads or writes that one field on the http
// sub-message — never Http1MaxPendingRequests/Http2MaxRequests (fan-out's
// own fields on the same sub-message) and never the sub-message itself.
func applyInterpolatedRetryConnectionPool(dr *networkingv1.DestinationRule, orig originalRetryConnectionPoolJSON, t float64) {
	http := ensureConnectionPoolHTTP(dr)
	http.MaxRetries = lerpI32(TripRetryStormMaxRetries, origRetryMaxRetriesTarget(orig), t)
}

// applyOriginalRetryConnectionPool writes back the literal captured
// MaxRetries (0 correctly means "restore to absent/default") and then, if
// the http sub-message has no remaining fields, prunes it the same way
// fan-out's Unset path does via clearConnectionPoolHTTP. That prune is
// safe now that this signature only owns MaxRetries: an empty http after
// writing 0 means neither this signature nor fan-out (nor a user) has
// anything left on the block, which is exactly the live-observed empty
// `connectionPool.http: {}` shell this restores away. A sibling field
// still set — fan-out's Http2MaxRequests, a user-authored
// MaxRequestsPerConnection — keeps the block.
func applyOriginalRetryConnectionPool(dr *networkingv1.DestinationRule, orig originalRetryConnectionPoolJSON) {
	http := ensureConnectionPoolHTTP(dr)
	http.MaxRetries = orig.MaxRetries
	if httpSettingsEmpty(http) {
		clearConnectionPoolHTTP(dr)
	}
}

func httpSettingsEmpty(http *apinet.ConnectionPoolSettings_HTTPSettings) bool {
	if http == nil {
		return true
	}
	return http.GetHttp1MaxPendingRequests() == 0 &&
		http.GetHttp2MaxRequests() == 0 &&
		http.GetMaxRequestsPerConnection() == 0 &&
		http.GetMaxRetries() == 0 &&
		http.GetIdleTimeout() == nil &&
		http.GetH2UpgradePolicy() == 0 &&
		!http.GetUseClientProtocol() &&
		http.GetMaxConcurrentStreams() == 0
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

// ApplyRetryStormConnectionPoolRestoreStep writes interpolated MaxRetries
// for restoreStep 0–3, or the stored original at step 4 — mirrors every
// other *RestoreStep function's step contract exactly, reusing the same
// restoreProgress(step) curve.
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
// MaxRetries (pruning an empty http sub-message) and removes both operator
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
