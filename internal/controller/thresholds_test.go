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
	"testing"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

func basePolicyForThresholds() *cascadev1alpha1.CascadePolicy {
	return &cascadev1alpha1.CascadePolicy{
		Spec: cascadev1alpha1.CascadePolicySpec{
			Service:   patchServiceFQDN,
			DependsOn: []string{patchDepHost, inventoryDepHost},
			Thresholds: cascadev1alpha1.Thresholds{
				LatencyP99Ms:         500,
				ErrorRateFraction:    0.05,
				WindowSeconds:        30,
				RetryStormMultiplier: 3,
				FanOutMultiplier:     5,
			},
		},
	}
}

func TestEffectiveThresholdsReturnsPolicyWideWhenNoOverridesExist(t *testing.T) {
	t.Parallel()
	policy := basePolicyForThresholds()
	got := effectiveThresholds(policy, patchDepHost)
	if got != policy.Spec.Thresholds {
		t.Fatalf("effectiveThresholds = %+v, want unchanged policy.Spec.Thresholds %+v", got, policy.Spec.Thresholds)
	}
}

func TestEffectiveThresholdsReturnsPolicyWideForHostWithNoOverrideEntry(t *testing.T) {
	t.Parallel()
	policy := basePolicyForThresholds()
	latency := int32(100)
	policy.Spec.ThresholdOverrides = map[string]cascadev1alpha1.ThresholdOverrides{
		inventoryDepHost: {LatencyP99Ms: &latency},
	}
	// patchDepHost has no entry of its own — must be unaffected by the
	// override that exists for a *different* host.
	got := effectiveThresholds(policy, patchDepHost)
	if got != policy.Spec.Thresholds {
		t.Fatalf("effectiveThresholds for unrelated host = %+v, want unchanged %+v", got, policy.Spec.Thresholds)
	}
}

func TestEffectiveThresholdsAppliesOnlyOverriddenFields(t *testing.T) {
	t.Parallel()
	policy := basePolicyForThresholds()
	latency := int32(100)
	fanOut := 2.0
	policy.Spec.ThresholdOverrides = map[string]cascadev1alpha1.ThresholdOverrides{
		patchDepHost: {LatencyP99Ms: &latency, FanOutMultiplier: &fanOut},
	}

	got := effectiveThresholds(policy, patchDepHost)
	if got.LatencyP99Ms != 100 {
		t.Errorf("LatencyP99Ms = %d, want overridden 100", got.LatencyP99Ms)
	}
	if got.FanOutMultiplier != 2.0 {
		t.Errorf("FanOutMultiplier = %v, want overridden 2.0", got.FanOutMultiplier)
	}
	// Every field *not* named in the override must fall back to the
	// policy-wide value unchanged.
	if got.ErrorRateFraction != policy.Spec.Thresholds.ErrorRateFraction {
		t.Errorf("ErrorRateFraction = %v, want unmodified policy-wide %v", got.ErrorRateFraction, policy.Spec.Thresholds.ErrorRateFraction)
	}
	if got.WindowSeconds != policy.Spec.Thresholds.WindowSeconds {
		t.Errorf("WindowSeconds = %d, want unmodified policy-wide %d", got.WindowSeconds, policy.Spec.Thresholds.WindowSeconds)
	}
	if got.RetryStormMultiplier != policy.Spec.Thresholds.RetryStormMultiplier {
		t.Errorf("RetryStormMultiplier = %v, want unmodified policy-wide %v", got.RetryStormMultiplier, policy.Spec.Thresholds.RetryStormMultiplier)
	}
}

// TestEffectiveThresholdsDistinguishesExplicitZeroFromUnset is the whole
// reason ThresholdOverrides' fields are pointers, not plain scalars — the
// exact ambiguity this project's retry-storm bug thread spent an entire
// session fighting in the vendored Istio proto types, deliberately not
// repeated here since this CRD's schema is one this project controls.
func TestEffectiveThresholdsDistinguishesExplicitZeroFromUnset(t *testing.T) {
	t.Parallel()
	policy := basePolicyForThresholds()
	explicitZero := 0.0
	policy.Spec.ThresholdOverrides = map[string]cascadev1alpha1.ThresholdOverrides{
		patchDepHost: {ErrorRateFraction: &explicitZero},
	}

	got := effectiveThresholds(policy, patchDepHost)
	if got.ErrorRateFraction != 0 {
		t.Fatalf("ErrorRateFraction = %v, want explicit override 0", got.ErrorRateFraction)
	}
	// Every other field must still fall back to policy-wide, proving the
	// zero was read as "override this field to 0", not "override is unset".
	if got.LatencyP99Ms != policy.Spec.Thresholds.LatencyP99Ms {
		t.Errorf("LatencyP99Ms = %d, want unmodified policy-wide %d — explicit-zero override for a different field should not affect this one", got.LatencyP99Ms, policy.Spec.Thresholds.LatencyP99Ms)
	}
}
