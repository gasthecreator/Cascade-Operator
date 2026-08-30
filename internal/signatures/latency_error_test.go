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

package signatures

import (
	"math"
	"strings"
	"testing"
)

const evidenceIncompleteReadings = "incomplete readings"

func TestDetectLatencyError(t *testing.T) {
	t.Parallel()

	const (
		dep   = "payments"
		latTh = 500.0
		errTh = 0.05
	)

	cases := []struct {
		name        string
		in          LatencyErrorInput
		wantTrip    bool
		wantConf    float64
		confDelta   float64
		evidenceHas []string
	}{
		{
			name: "both well below",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: 80, ErrorRateFraction: 0.001,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
		},
		{
			name: "latency spike only",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: 900, ErrorRateFraction: 0.01,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
			evidenceHas: []string{"latency_spike=true", "error_rise=false"},
		},
		{
			name: "error rise only",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: 100, ErrorRateFraction: 0.4,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
			evidenceHas: []string{"latency_spike=false", "error_rise=true"},
		},
		{
			name: "both exactly at threshold",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: 500, ErrorRateFraction: 0.05,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
			wantTrip:  true,
			wantConf:  0.5,
			confDelta: 1e-9,
		},
		{
			name: "both just below threshold",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: 499.9, ErrorRateFraction: 0.049,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
		},
		{
			name: "both well above",
			in: LatencyErrorInput{
				Dependency: "inventory", LatencyP99Ms: 1500, ErrorRateFraction: 0.2,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
			wantTrip:  true,
			wantConf:  1.0,
			confDelta: 1e-9,
		},
		{
			name: "NaN latency does not trip",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: math.NaN(), ErrorRateFraction: 0.9,
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
			evidenceHas: []string{evidenceIncompleteReadings},
		},
		{
			name: "NaN error rate does not trip",
			in: LatencyErrorInput{
				Dependency: dep, LatencyP99Ms: 900, ErrorRateFraction: math.NaN(),
				LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
			},
			evidenceHas: []string{evidenceIncompleteReadings},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DetectLatencyError(tc.in)
			if got.Tripped != tc.wantTrip {
				t.Errorf("Tripped = %v, want %v; evidence=%q", got.Tripped, tc.wantTrip, got.Evidence)
			}
			if tc.wantTrip {
				if math.Abs(got.Confidence-tc.wantConf) > tc.confDelta {
					t.Errorf("Confidence = %v, want %v", got.Confidence, tc.wantConf)
				}
			} else if got.Confidence != 0 {
				t.Errorf("Confidence = %v, want 0 when not tripped", got.Confidence)
			}
			if !strings.Contains(got.Evidence, tc.in.Dependency) {
				t.Errorf("Evidence %q: want dependency %q", got.Evidence, tc.in.Dependency)
			}
			for _, s := range tc.evidenceHas {
				if !strings.Contains(got.Evidence, s) {
					t.Errorf("Evidence %q: want substring %q", got.Evidence, s)
				}
			}
		})
	}
}
