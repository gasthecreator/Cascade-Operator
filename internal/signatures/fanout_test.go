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

func TestDetectFanOut(t *testing.T) {
	t.Parallel()

	const (
		dep = "payments"
		th  = 3.0
	)

	cases := []struct {
		name        string
		in          FanOutInput
		wantTrip    bool
		wantConf    float64
		confDelta   float64
		evidenceHas []string
	}{
		{
			name: "well below (healthy 1:1:1 baseline)",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: 1.0, Multiplier: th,
			},
			evidenceHas: []string{evidenceBelowThreshold},
		},
		{
			name: "just below threshold",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: 2.99, Multiplier: th,
			},
			evidenceHas: []string{evidenceBelowThreshold},
		},
		{
			name: "exactly at threshold (matches the live payments:checkout ratio observed during the fan-out evidence slice)",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: 3.0, Multiplier: th,
			},
			wantTrip:    true,
			wantConf:    0.5,
			confDelta:   1e-9,
			evidenceHas: []string{"fan_out=true"},
		},
		{
			name: "well above (8x)",
			in: FanOutInput{
				Dependency: depInventory, DependencyCallerRatio: 8.0, Multiplier: th,
			},
			wantTrip:    true,
			wantConf:    1.0,
			confDelta:   1e-9,
			evidenceHas: []string{"fan_out=true"},
		},
		{
			name: "NaN ratio does not trip",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: math.NaN(), Multiplier: th,
			},
			evidenceHas: []string{evidenceIncompleteReadings},
		},
		{
			name: "+Inf ratio does not trip",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: math.Inf(1), Multiplier: th,
			},
			evidenceHas: []string{evidenceIncompleteReadings},
		},
		{
			name: "-Inf ratio does not trip",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: math.Inf(-1), Multiplier: th,
			},
			evidenceHas: []string{evidenceIncompleteReadings},
		},
		{
			name: "invalid multiplier does not trip",
			in: FanOutInput{
				Dependency: dep, DependencyCallerRatio: 4.0, Multiplier: 0,
			},
			evidenceHas: []string{"invalid threshold"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DetectFanOut(tc.in)
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
