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
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
)

// Annotation keys on operator-patched DestinationRules (PLAN.md §2.6).
const (
	AnnotationManagedBy       = "cascade.gideonsanni.dev/managed-by"
	ManagedByValue            = "cascade-operator"
	AnnotationOriginalOutlier = "cascade.gideonsanni.dev/original-outlier-detection"
	// OriginalOutlierUnsetJSON is stored when trafficPolicy.outlierDetection
	// was nil before the first patch. The restore slice reads this sentinel
	// instead of inventing a "loose" default.
	OriginalOutlierUnsetJSON = `{"unset":true}`
)

// Trip-time outlier detection. Istio defaults are consecutive5xx=5,
// interval=10s, baseEjectionTime=30s. We tighten the first two so Envoy
// ejects inside the typical 30s PromQL window, and keep ejection at one
// window so the next reconcile tick can observe the effect.
const (
	TripConsecutive5xx = uint32(3)
	TripInterval       = 5 * time.Second
	TripBaseEjection   = 30 * time.Second
)

// originalOutlierJSON is the restore-slice contract stored on first patch.
// Unset means trafficPolicy.outlierDetection was nil before we touched it.
type originalOutlierJSON struct {
	Unset                bool    `json:"unset,omitempty"`
	Consecutive5xxErrors *uint32 `json:"consecutive5xxErrors,omitempty"`
	Interval             string  `json:"interval,omitempty"`
	BaseEjectionTime     string  `json:"baseEjectionTime,omitempty"`
}

// ApplyLatencyErrorOutlierTrip mutates only outlierDetection on dr (plus our
// annotations). Host, TLS, connection pool, subsets, and other outlier
// fields (maxEjectionPercent, …) are left as they were. The original
// annotation is written only when managed-by is not already set.
func ApplyLatencyErrorOutlierTrip(dr *networkingv1.DestinationRule) {
	if dr.Annotations == nil {
		dr.Annotations = map[string]string{}
	}
	if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
		dr.Annotations[AnnotationOriginalOutlier] = snapshotOutlierJSON(dr)
		dr.Annotations[AnnotationManagedBy] = ManagedByValue
	}

	if dr.Spec.TrafficPolicy == nil {
		dr.Spec.TrafficPolicy = &apinet.TrafficPolicy{}
	}
	if dr.Spec.TrafficPolicy.OutlierDetection == nil {
		dr.Spec.TrafficPolicy.OutlierDetection = &apinet.OutlierDetection{}
	}
	od := dr.Spec.TrafficPolicy.OutlierDetection
	od.Consecutive_5XxErrors = wrapperspb.UInt32(TripConsecutive5xx)
	od.Interval = durationpb.New(TripInterval)
	od.BaseEjectionTime = durationpb.New(TripBaseEjection)
}

func snapshotOutlierJSON(dr *networkingv1.DestinationRule) string {
	od := dr.Spec.GetTrafficPolicy().GetOutlierDetection()
	if od == nil {
		return OriginalOutlierUnsetJSON
	}
	snap := originalOutlierJSON{}
	if v := od.GetConsecutive_5XxErrors(); v != nil {
		u := v.GetValue()
		snap.Consecutive5xxErrors = &u
	}
	if d := od.GetInterval(); d != nil {
		snap.Interval = d.AsDuration().String()
	}
	if d := od.GetBaseEjectionTime(); d != nil {
		snap.BaseEjectionTime = d.AsDuration().String()
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return OriginalOutlierUnsetJSON
	}
	return string(b)
}
