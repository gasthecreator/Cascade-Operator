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
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// RestoreFinalStep is restoreStep 4 — the last of five ramp positions (0–4).
const RestoreFinalStep int32 = 4

// Istio defaults, used only as interpolation *targets* when a field (or the
// whole outlierDetection block) was unset pre-trip. A completed restore of
// an unset original still clears the block rather than writing these.
const (
	istioDefaultConsecutive5xx = uint32(5)
	istioDefaultInterval       = 10 * time.Second
	istioDefaultBaseEjection   = 30 * time.Second
)

// IsOperatorManaged reports whether this DestinationRule was patched by us.
func IsOperatorManaged(dr *networkingv1.DestinationRule) bool {
	if dr == nil || dr.Annotations == nil {
		return false
	}
	return dr.Annotations[AnnotationManagedBy] == ManagedByValue
}

// restoreProgress maps restoreStep 0–4 onto (0, 1]: step 0 is 20% of the
// way from trip values toward the original (the first loosening), step 4 is
// 100% (the original itself).
func restoreProgress(step int32) float64 {
	if step < 0 {
		step = 0
	}
	if step > RestoreFinalStep {
		step = RestoreFinalStep
	}
	return float64(step+1) / float64(RestoreFinalStep+1)
}

func parseOriginalOutlier(raw string) (originalOutlierJSON, error) {
	var orig originalOutlierJSON
	if raw == "" {
		return orig, fmt.Errorf("original-outlier-detection annotation is empty")
	}
	if err := json.Unmarshal([]byte(raw), &orig); err != nil {
		return orig, fmt.Errorf("parse original-outlier-detection: %w", err)
	}
	return orig, nil
}

func durationOrDefault(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func lerpU32(from, to uint32, t float64) uint32 {
	if t >= 1 {
		return to
	}
	return uint32(math.Round(float64(from) + (float64(to)-float64(from))*t))
}

func lerpDuration(from, to time.Duration, t float64) time.Duration {
	if t >= 1 {
		return to
	}
	return from + time.Duration(float64(to-from)*t)
}

func origConsecutiveTarget(orig originalOutlierJSON) uint32 {
	if orig.Consecutive5xxErrors != nil {
		return *orig.Consecutive5xxErrors
	}
	return istioDefaultConsecutive5xx
}

func origIntervalTarget(orig originalOutlierJSON) time.Duration {
	return durationOrDefault(orig.Interval, istioDefaultInterval)
}

func origBaseEjectionTarget(orig originalOutlierJSON) time.Duration {
	return durationOrDefault(orig.BaseEjectionTime, istioDefaultBaseEjection)
}

func ensureOutlier(dr *networkingv1.DestinationRule) *apinet.OutlierDetection {
	if dr.Spec.TrafficPolicy == nil {
		dr.Spec.TrafficPolicy = &apinet.TrafficPolicy{}
	}
	if dr.Spec.TrafficPolicy.OutlierDetection == nil {
		dr.Spec.TrafficPolicy.OutlierDetection = &apinet.OutlierDetection{}
	}
	return dr.Spec.TrafficPolicy.OutlierDetection
}

func applyInterpolatedOutlier(dr *networkingv1.DestinationRule, orig originalOutlierJSON, t float64) {
	od := ensureOutlier(dr)
	od.Consecutive_5XxErrors = wrapperspb.UInt32(lerpU32(TripConsecutive5xx, origConsecutiveTarget(orig), t))
	od.Interval = durationpb.New(lerpDuration(TripInterval, origIntervalTarget(orig), t))
	od.BaseEjectionTime = durationpb.New(lerpDuration(TripBaseEjection, origBaseEjectionTarget(orig), t))
}

func applyOriginalOutlier(dr *networkingv1.DestinationRule, orig originalOutlierJSON) {
	if orig.Unset {
		clearOutlierDetection(dr)
		return
	}
	od := ensureOutlier(dr)
	if orig.Consecutive5xxErrors != nil {
		od.Consecutive_5XxErrors = wrapperspb.UInt32(*orig.Consecutive5xxErrors)
	} else {
		od.Consecutive_5XxErrors = nil
	}
	if orig.Interval != "" {
		od.Interval = durationpb.New(durationOrDefault(orig.Interval, istioDefaultInterval))
	} else {
		od.Interval = nil
	}
	if orig.BaseEjectionTime != "" {
		od.BaseEjectionTime = durationpb.New(durationOrDefault(orig.BaseEjectionTime, istioDefaultBaseEjection))
	} else {
		od.BaseEjectionTime = nil
	}
}

func clearOutlierDetection(dr *networkingv1.DestinationRule) {
	tp := dr.Spec.TrafficPolicy
	if tp == nil {
		return
	}
	tp.OutlierDetection = nil
	if trafficPolicyEmpty(tp) {
		dr.Spec.TrafficPolicy = nil
	}
}

func trafficPolicyEmpty(tp *apinet.TrafficPolicy) bool {
	if tp == nil {
		return true
	}
	return tp.LoadBalancer == nil &&
		tp.ConnectionPool == nil &&
		tp.OutlierDetection == nil &&
		tp.Tls == nil &&
		len(tp.PortLevelSettings) == 0 &&
		tp.Tunnel == nil &&
		tp.ProxyProtocol == nil &&
		tp.RetryBudget == nil
}

func stripOperatorAnnotations(dr *networkingv1.DestinationRule) {
	if dr.Annotations == nil {
		return
	}
	delete(dr.Annotations, AnnotationManagedBy)
	delete(dr.Annotations, AnnotationOriginalOutlier)
	if len(dr.Annotations) == 0 {
		dr.Annotations = nil
	}
}

// ApplyLatencyErrorOutlierRestoreStep writes interpolated outlierDetection
// for restoreStep 0–3, or the stored original at step 4. Annotations stay;
// CompleteLatencyErrorOutlierRestore removes them after the last healthy gate.
func ApplyLatencyErrorOutlierRestoreStep(dr *networkingv1.DestinationRule, step int32) error {
	orig, err := parseOriginalOutlier(dr.Annotations[AnnotationOriginalOutlier])
	if err != nil {
		return err
	}
	if step >= RestoreFinalStep {
		applyOriginalOutlier(dr, orig)
		return nil
	}
	applyInterpolatedOutlier(dr, orig, restoreProgress(step))
	return nil
}

// CompleteLatencyErrorOutlierRestore writes the stored original (or clears
// outlierDetection if it was unset) and removes both operator annotations.
func CompleteLatencyErrorOutlierRestore(dr *networkingv1.DestinationRule) error {
	orig, err := parseOriginalOutlier(dr.Annotations[AnnotationOriginalOutlier])
	if err != nil {
		return err
	}
	applyOriginalOutlier(dr, orig)
	stripOperatorAnnotations(dr)
	return nil
}
