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

	spv1alpha2 "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd/serviceprofile/v1alpha2"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

const annotationOriginalRetryBudget = "cascade.gideonsanni.dev/original-retry-budget"

// Trip-time values: retryRatio and minRetriesPerSecond both cut to 0,
// fully suppressing retries regardless of the pre-existing budget's own
// ratio/floor — mirrors internal/mitigation.TripRetryAttempts (cut to 0,
// not some smaller positive number: PLAN.md §2.6 found a 1-retry allowance
// can itself sit at or above a low retryStormMultiplier, so only full
// suppression reliably stops the storm). ttl is left at whatever it
// already was on the pre-existing budget (or a fixed default if the
// budget didn't exist yet at all — retryBudget's three fields are all
// required together per the live CRD schema, so a freshly-created budget
// needs *some* ttl even though this Mitigator never manages it directly)
// — the window size doesn't gate whether retries are suppressed, only the
// granularity of the ratio's own rolling measurement.
const (
	tripRetryRatio          = float32(0)
	tripMinRetriesPerSecond = int32(0)
	defaultRetryBudgetTTL   = "10s"
)

// originalRetryBudgetJSON is the restore-slice contract, same Unset-sentinel
// shape as originalFailureAccrualJSON/Istio's originalOutlierJSON. TTL is
// captured (not ramped, just carried through to CompleteRestore) so a
// restored budget is byte-identical to the pre-trip one, not merely
// "close enough on the two ramped fields."
type originalRetryBudgetJSON struct {
	Unset               bool     `json:"unset,omitempty"`
	RetryRatio          *float32 `json:"retryRatio,omitempty"`
	MinRetriesPerSecond *int32   `json:"minRetriesPerSecond,omitempty"`
	TTL                 *string  `json:"ttl,omitempty"`
}

func snapshotRetryBudgetJSON(sp *spv1alpha2.ServiceProfile) string {
	rb := sp.Spec.RetryBudget
	if rb == nil {
		return unsetOriginalJSON
	}
	ratio := rb.RetryRatio
	minRPS := rb.MinRetriesPerSecond
	ttl := rb.TTL
	snap := originalRetryBudgetJSON{RetryRatio: &ratio, MinRetriesPerSecond: &minRPS, TTL: &ttl}
	b, err := json.Marshal(snap)
	if err != nil {
		return unsetOriginalJSON
	}
	return string(b)
}

func parseOriginalRetryBudget(raw string) (originalRetryBudgetJSON, error) {
	var orig originalRetryBudgetJSON
	if raw == "" {
		return originalRetryBudgetJSON{Unset: true}, nil
	}
	if err := json.Unmarshal([]byte(raw), &orig); err != nil {
		return orig, err
	}
	return orig, nil
}

// applyRetryBudgetTrip patches sp's retryBudget to the trip values,
// creating the block if it was nil pre-trip (mirrors Istio's
// ensureOutlier — the containing object always pre-exists per this
// project's "operator patches fields, never creates/deletes the whole
// object" convention across every signature and mesh, but a nested,
// optional field within it can be freshly populated). Original capture
// keyed off annotationOriginalRetryBudget's own presence, same discipline
// as every other signature in this project.
func applyRetryBudgetTrip(sp *spv1alpha2.ServiceProfile) {
	if sp.Annotations == nil {
		sp.Annotations = map[string]string{}
	}
	if _, captured := sp.Annotations[annotationOriginalRetryBudget]; !captured {
		sp.Annotations[annotationOriginalRetryBudget] = snapshotRetryBudgetJSON(sp)
	}
	sp.Annotations[mitigation.AnnotationManagedBy] = mitigation.ManagedByValue

	ttl := defaultRetryBudgetTTL
	if sp.Spec.RetryBudget != nil {
		ttl = sp.Spec.RetryBudget.TTL
	}
	sp.Spec.RetryBudget = &spv1alpha2.RetryBudget{
		RetryRatio:          tripRetryRatio,
		MinRetriesPerSecond: tripMinRetriesPerSecond,
		TTL:                 ttl,
	}
}

// applyRetryBudgetRestoreStep interpolates retryRatio/minRetriesPerSecond
// from the trip values toward orig's targets at restoreProgress(step). TTL
// is set to orig's captured value (or left as the trip-time default) for
// every step, not ramped — see this file's own const doc comment for why
// ttl isn't a ramped field.
func applyRetryBudgetRestoreStep(sp *spv1alpha2.ServiceProfile, orig originalRetryBudgetJSON, step int32) {
	t := restoreProgress(step)
	ttl := defaultRetryBudgetTTL
	if orig.TTL != nil {
		ttl = *orig.TTL
	}
	sp.Spec.RetryBudget = &spv1alpha2.RetryBudget{
		RetryRatio:          lerpFloat32(tripRetryRatio, origRetryRatioTarget(orig), t),
		MinRetriesPerSecond: lerpInt32(tripMinRetriesPerSecond, origMinRetriesPerSecondTarget(orig), t),
		TTL:                 ttl,
	}
}

// applyOriginalRetryBudget writes back sp's true pre-trip retryBudget —
// nil entirely if orig.Unset, the captured values otherwise.
func applyOriginalRetryBudget(sp *spv1alpha2.ServiceProfile, orig originalRetryBudgetJSON) {
	if orig.Unset {
		sp.Spec.RetryBudget = nil
		return
	}
	ttl := defaultRetryBudgetTTL
	if orig.TTL != nil {
		ttl = *orig.TTL
	}
	sp.Spec.RetryBudget = &spv1alpha2.RetryBudget{
		RetryRatio:          origRetryRatioTarget(orig),
		MinRetriesPerSecond: origMinRetriesPerSecondTarget(orig),
		TTL:                 ttl,
	}
}

func stripRetryBudgetManagedAnnotations(sp *spv1alpha2.ServiceProfile) {
	if sp.Annotations == nil {
		return
	}
	delete(sp.Annotations, mitigation.AnnotationManagedBy)
	delete(sp.Annotations, annotationOriginalRetryBudget)
	if len(sp.Annotations) == 0 {
		sp.Annotations = nil
	}
}

func isServiceProfileOperatorManaged(sp *spv1alpha2.ServiceProfile) bool {
	if sp == nil || sp.Annotations == nil {
		return false
	}
	return sp.Annotations[mitigation.AnnotationManagedBy] == mitigation.ManagedByValue
}

func origRetryRatioTarget(orig originalRetryBudgetJSON) float32 {
	if orig.RetryRatio != nil {
		return *orig.RetryRatio
	}
	return tripRetryRatio
}

func origMinRetriesPerSecondTarget(orig originalRetryBudgetJSON) int32 {
	if orig.MinRetriesPerSecond != nil {
		return *orig.MinRetriesPerSecond
	}
	return tripMinRetriesPerSecond
}

func lerpFloat32(from, to float32, t float64) float32 {
	if t >= 1 {
		return to
	}
	return float32(float64(from) + (float64(to)-float64(from))*t)
}
