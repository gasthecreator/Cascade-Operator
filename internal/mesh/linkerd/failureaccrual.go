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

package linkerd

import (
	"encoding/json"
	"math"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Failure-accrual Service annotations (linkerd.io/2-edge/reference/circuit-breaking) —
// confirmed against Linkerd's own reference docs 2026-08-31 (this package's
// worklog has the fetched text). "consecutive" mode only — "unified"
// (success-rate-based) mode's annotations are not used, mirroring this
// project's own outlier-detection choice of a consecutive-failure-count
// model over a rolling success-rate one (internal/mitigation/outlier.go).
const (
	annotationFailureAccrual            = "balancer.linkerd.io/failure-accrual"
	annotationFailureAccrualMaxFailures = "balancer.linkerd.io/failure-accrual-consecutive-max-failures"
	annotationFailureAccrualMinPenalty  = "balancer.linkerd.io/failure-accrual-consecutive-min-penalty"
	failureAccrualModeConsecutive       = "consecutive"
	annotationOriginalFailureAccrual    = "cascade.gideonsanni.dev/original-failure-accrual"
)

// Trip-time values. Numerically mirrors internal/mitigation.TripConsecutive5xx
// (3) and TripBaseEjection (30s) — same aggressive-trip intent (eject
// inside the typical 30s PromQL detection window), applied to Linkerd's own
// knobs rather than Istio's. max-penalty is deliberately left unset at trip
// time (and never restored/ramped): Linkerd's own default (1m) is already a
// sensible ceiling, and this Mitigator's ramp only needs to move the two
// fields that actually gate *when* a backend is ejected (max-failures,
// min-penalty) — not the probe-interval ceiling, which doesn't affect
// whether ejection triggers at all, only how slowly probing backs off once
// it has.
const (
	tripFailureAccrualMaxFailures = int32(3)
	tripFailureAccrualMinPenalty  = 30 * time.Second
)

// originalFailureAccrualJSON is the restore-slice contract stored on first
// patch — same "Unset sentinel vs. captured original" shape as Istio's
// originalOutlierJSON (internal/mitigation/outlier.go), applied to a plain
// annotation-keyed primitive instead of a typed proto field. MaxFailures/
// MinPenalty are pointers (not plain int32/string) so an explicitly-captured
// original of the empty string or 0 is distinguishable from "annotation was
// absent" — this struct is this project's own, so there's no reason to
// repeat the zero-value-vs-omitempty ambiguity this project's Istio retry-
// storm bug thread had to work around in *vendored* proto types.
type originalFailureAccrualJSON struct {
	Unset       bool    `json:"unset,omitempty"`
	MaxFailures *int32  `json:"maxFailures,omitempty"`
	MinPenalty  *string `json:"minPenalty,omitempty"`
}

// snapshotFailureAccrualJSON captures svc's pre-trip failure-accrual
// annotations. Only the two ramped fields are captured (mode itself,
// annotationFailureAccrual, is a fixed on/off marker restored via
// stripFailureAccrualAnnotations, not interpolated) — same asymmetry as
// Istio's outlier restore, which ramps consecutive5xx/interval/
// baseEjectionTime but treats the whole block's presence as binary.
func snapshotFailureAccrualJSON(svc *corev1.Service) string {
	maxFailuresRaw, hasMax := svc.Annotations[annotationFailureAccrualMaxFailures]
	minPenaltyRaw, hasMin := svc.Annotations[annotationFailureAccrualMinPenalty]
	if !hasMax && !hasMin {
		return unsetOriginalJSON
	}
	snap := originalFailureAccrualJSON{}
	if hasMax {
		if v, err := parseInt32(maxFailuresRaw); err == nil {
			snap.MaxFailures = &v
		}
	}
	if hasMin {
		minPenalty := minPenaltyRaw
		snap.MinPenalty = &minPenalty
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return unsetOriginalJSON
	}
	return string(b)
}

func parseOriginalFailureAccrual(raw string) (originalFailureAccrualJSON, error) {
	var orig originalFailureAccrualJSON
	if raw == "" {
		return originalFailureAccrualJSON{Unset: true}, nil
	}
	if err := json.Unmarshal([]byte(raw), &orig); err != nil {
		return orig, err
	}
	return orig, nil
}

// applyFailureAccrualTrip patches svc's failure-accrual annotations to the
// trip values, capturing the pre-trip state on first touch (checked via
// annotationOriginalFailureAccrual's own presence — see
// ApplyLatencyErrorOutlierTrip's doc comment for why this project always
// keys capture off the original-value annotation's own presence, not
// managed-by's: on Linkerd, corev1.Service is never shared with another
// signature the way DestinationRule is on Istio, so this is stricter than
// this particular object needs, but keeps the same discipline everywhere
// rather than a one-off exception).
func applyFailureAccrualTrip(svc *corev1.Service) {
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	if _, captured := svc.Annotations[annotationOriginalFailureAccrual]; !captured {
		svc.Annotations[annotationOriginalFailureAccrual] = snapshotFailureAccrualJSON(svc)
	}
	svc.Annotations[mitigation.AnnotationManagedBy] = mitigation.ManagedByValue
	svc.Annotations[annotationFailureAccrual] = failureAccrualModeConsecutive
	svc.Annotations[annotationFailureAccrualMaxFailures] = formatInt32(tripFailureAccrualMaxFailures)
	svc.Annotations[annotationFailureAccrualMinPenalty] = tripFailureAccrualMinPenalty.String()
}

// applyFailureAccrualRestoreStep interpolates max-failures/min-penalty from
// the trip values toward orig's targets at restoreProgress(step) — same
// lerp shape as Istio's applyInterpolatedOutlier, one fraction shared
// across every ramped field.
func applyFailureAccrualRestoreStep(svc *corev1.Service, orig originalFailureAccrualJSON, step int32) {
	t := restoreProgress(step)
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Annotations[annotationFailureAccrual] = failureAccrualModeConsecutive
	svc.Annotations[annotationFailureAccrualMaxFailures] = formatInt32(lerpInt32(tripFailureAccrualMaxFailures, origMaxFailuresTarget(orig), t))
	svc.Annotations[annotationFailureAccrualMinPenalty] = lerpDuration(tripFailureAccrualMinPenalty, origMinPenaltyTarget(orig), t).String()
}

// applyOriginalFailureAccrual writes back svc's true pre-trip state: clears
// every failure-accrual annotation entirely if orig.Unset, otherwise
// restores the captured values.
func applyOriginalFailureAccrual(svc *corev1.Service, orig originalFailureAccrualJSON) {
	if svc.Annotations == nil {
		return
	}
	if orig.Unset {
		delete(svc.Annotations, annotationFailureAccrual)
		delete(svc.Annotations, annotationFailureAccrualMaxFailures)
		delete(svc.Annotations, annotationFailureAccrualMinPenalty)
		return
	}
	svc.Annotations[annotationFailureAccrual] = failureAccrualModeConsecutive
	if orig.MaxFailures != nil {
		svc.Annotations[annotationFailureAccrualMaxFailures] = formatInt32(*orig.MaxFailures)
	} else {
		delete(svc.Annotations, annotationFailureAccrualMaxFailures)
	}
	if orig.MinPenalty != nil {
		svc.Annotations[annotationFailureAccrualMinPenalty] = *orig.MinPenalty
	} else {
		delete(svc.Annotations, annotationFailureAccrualMinPenalty)
	}
}

func stripFailureAccrualManagedAnnotations(svc *corev1.Service) {
	if svc.Annotations == nil {
		return
	}
	delete(svc.Annotations, mitigation.AnnotationManagedBy)
	delete(svc.Annotations, annotationOriginalFailureAccrual)
	if len(svc.Annotations) == 0 {
		svc.Annotations = nil
	}
}

func origMaxFailuresTarget(orig originalFailureAccrualJSON) int32 {
	if orig.MaxFailures != nil {
		return *orig.MaxFailures
	}
	return tripFailureAccrualMaxFailures
}

func origMinPenaltyTarget(orig originalFailureAccrualJSON) time.Duration {
	if orig.MinPenalty != nil {
		if d, err := time.ParseDuration(*orig.MinPenalty); err == nil {
			return d
		}
	}
	return tripFailureAccrualMinPenalty
}

func isServiceOperatorManaged(svc *corev1.Service) bool {
	if svc == nil || svc.Annotations == nil {
		return false
	}
	return svc.Annotations[mitigation.AnnotationManagedBy] == mitigation.ManagedByValue
}

func lerpInt32(from, to int32, t float64) int32 {
	if t >= 1 {
		return to
	}
	return int32(math.Round(float64(from) + (float64(to)-float64(from))*t))
}

func lerpDuration(from, to time.Duration, t float64) time.Duration {
	if t >= 1 {
		return to
	}
	return from + time.Duration(float64(to-from)*t)
}
