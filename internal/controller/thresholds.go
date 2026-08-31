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

package controller

import (
	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// effectiveThresholds merges spec.thresholds with host's entry in
// spec.thresholdOverrides, if any (PLAN.md §5, PROPOSALS.md 2026-08-31 —
// purely additive: a policy with no thresholdOverrides, or a host with no
// entry in it, gets spec.thresholds back completely unchanged). Every
// ThresholdOverrides field is a pointer specifically so "not overridden"
// is distinguishable from "explicitly overridden to 0" — unlike the
// vendored Istio proto types the retry-storm bug thread spent a whole
// session working around, this project owns this CRD outright, so there's
// no reason to repeat that ambiguity here.
func effectiveThresholds(policy *cascadev1alpha1.CascadePolicy, host string) cascadev1alpha1.Thresholds {
	th := policy.Spec.Thresholds
	override, ok := policy.Spec.ThresholdOverrides[host]
	if !ok {
		return th
	}
	if override.LatencyP99Ms != nil {
		th.LatencyP99Ms = *override.LatencyP99Ms
	}
	if override.ErrorRateFraction != nil {
		th.ErrorRateFraction = *override.ErrorRateFraction
	}
	if override.WindowSeconds != nil {
		th.WindowSeconds = *override.WindowSeconds
	}
	if override.RetryStormMultiplier != nil {
		th.RetryStormMultiplier = *override.RetryStormMultiplier
	}
	if override.FanOutMultiplier != nil {
		th.FanOutMultiplier = *override.FanOutMultiplier
	}
	return th
}
