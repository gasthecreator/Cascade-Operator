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
	"math"

	"google.golang.org/protobuf/types/known/durationpb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// istioDefaultRetryAttempts is the ramp target for a route whose original
// snapshot is Unset — same source as TripRetryAttempts's doc comment
// (vendored istio.io/api v1.30.4 proto doc, matching the pinned Kind
// cluster version): a route with no explicit retries block gets an
// implicit attempts=2 cluster-wide default.
const istioDefaultRetryAttempts = int32(2)

// IsVirtualServiceManaged reports whether this VirtualService was patched
// by us — the VirtualService twin of IsOperatorManaged.
func IsVirtualServiceManaged(vs *networkingv1.VirtualService) bool {
	if vs == nil || vs.Annotations == nil {
		return false
	}
	return vs.Annotations[AnnotationManagedBy] == ManagedByValue
}

func parseOriginalRetries(raw string) ([]originalRouteRetriesJSON, error) {
	if raw == "" {
		return nil, fmt.Errorf("original-retries annotation is empty")
	}
	var snaps []originalRouteRetriesJSON
	if err := json.Unmarshal([]byte(raw), &snaps); err != nil {
		return nil, fmt.Errorf("parse original-retries: %w", err)
	}
	return snaps, nil
}

func lerpI32(from, to int32, t float64) int32 {
	if t >= 1 {
		return to
	}
	return int32(math.Round(float64(from) + (float64(to)-float64(from))*t))
}

// applyInterpolatedRetries ramps attempts on every non-skipped route toward
// its restore target: the captured original for a route that had an
// explicit retries block, or istioDefaultRetryAttempts for a route that
// relied on Istio's implicit default pre-trip — mirrors
// applyInterpolatedOutlier's Unset handling (ramp toward the Istio default
// mid-ramp; only clear the block entirely at final completion, in
// applyOriginalRetries below). Skipped routes, and any route that somehow
// lost its trip-time retries block, are left untouched.
func applyInterpolatedRetries(vs *networkingv1.VirtualService, snaps []originalRouteRetriesJSON, t float64) {
	routes := vs.Spec.GetHttp()
	for i, route := range routes {
		if i >= len(snaps) {
			break
		}
		snap := snaps[i]
		if snap.Skipped || route.Retries == nil {
			continue
		}
		target := istioDefaultRetryAttempts
		if !snap.Unset {
			target = snap.Attempts
		}
		route.Retries.Attempts = lerpI32(TripRetryAttempts, target, t)
	}
}

// applyOriginalRetries writes each route's exact pre-trip state: Unset gets
// its retries block cleared entirely rather than an explicit attempts=2
// (don't invent config the user never had — same rule as outlierDetection's
// Unset restore), an explicit route gets attempts/retryOn/perTryTimeout/
// backoff back verbatim, and Skipped is left alone since it was never
// touched on trip.
func applyOriginalRetries(vs *networkingv1.VirtualService, snaps []originalRouteRetriesJSON) {
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
			route.Retries = nil
			continue
		}
		retries := &apinet.HTTPRetry{Attempts: snap.Attempts, RetryOn: snap.RetryOn}
		if d := durationOrDefault(snap.PerTryTimeout, 0); d > 0 {
			retries.PerTryTimeout = durationpb.New(d)
		}
		if d := durationOrDefault(snap.Backoff, 0); d > 0 {
			retries.Backoff = durationpb.New(d)
		}
		route.Retries = retries
	}
}

func stripRetryStormAnnotations(vs *networkingv1.VirtualService) {
	if vs.Annotations == nil {
		return
	}
	delete(vs.Annotations, AnnotationManagedBy)
	delete(vs.Annotations, AnnotationOriginalRetries)
	if len(vs.Annotations) == 0 {
		vs.Annotations = nil
	}
}

// ApplyRetryStormRestoreStep writes interpolated per-route retries.attempts
// for restoreStep 0–3, or each route's stored original at step 4 — mirrors
// ApplyLatencyErrorOutlierRestoreStep's step contract exactly, reusing the
// same restoreProgress(step) curve.
func ApplyRetryStormRestoreStep(vs *networkingv1.VirtualService, step int32) error {
	snaps, err := parseOriginalRetries(vs.Annotations[AnnotationOriginalRetries])
	if err != nil {
		return err
	}
	if step >= RestoreFinalStep {
		applyOriginalRetries(vs, snaps)
		return nil
	}
	applyInterpolatedRetries(vs, snaps, restoreProgress(step))
	return nil
}

// CompleteRetryStormRestore writes each route's stored original retries
// state and removes both operator annotations.
func CompleteRetryStormRestore(vs *networkingv1.VirtualService) error {
	snaps, err := parseOriginalRetries(vs.Annotations[AnnotationOriginalRetries])
	if err != nil {
		return err
	}
	applyOriginalRetries(vs, snaps)
	stripRetryStormAnnotations(vs)
	return nil
}
