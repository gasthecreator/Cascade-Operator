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
	"fmt"
	"math"
)

// LatencyErrorInput is one dependency's windowed p99 and error rate, plus the
// policy thresholds. Readings are already-evaluated numbers — Prometheus owns
// the [Ns] window; this function does not inspect PromQL or Snapshot types.
type LatencyErrorInput struct {
	Dependency         string
	LatencyP99Ms       float64
	ErrorRateFraction  float64
	LatencyThresholdMs float64
	ErrorRateThreshold float64
}

// DetectLatencyError trips only when both a p99 spike and an error-rate rise
// are at or above threshold. That AND is the cascade: latency without errors
// is a slow-but-healthy dependency; errors without latency is a fast fail, not
// a propagating stall. Inclusive (>=) so a policy of 500ms / 0.05 trips at
// those values, not only strictly above.
func DetectLatencyError(in LatencyErrorInput) Verdict {
	evidence := fmt.Sprintf(
		"dependency=%s p99_ms=%g (threshold %g) error_rate=%g (threshold %g)",
		in.Dependency, in.LatencyP99Ms, in.LatencyThresholdMs, in.ErrorRateFraction, in.ErrorRateThreshold,
	)

	if !finite(in.LatencyP99Ms) || !finite(in.ErrorRateFraction) {
		return Verdict{Evidence: evidence + " incomplete readings"}
	}
	if in.LatencyThresholdMs <= 0 || in.ErrorRateThreshold < 0 {
		return Verdict{Evidence: evidence + " invalid thresholds"}
	}

	latencySpike := in.LatencyP99Ms >= in.LatencyThresholdMs
	errorRise := in.ErrorRateFraction >= in.ErrorRateThreshold
	if !latencySpike || !errorRise {
		return Verdict{
			Evidence: evidence + fmt.Sprintf(" latency_spike=%t error_rise=%t", latencySpike, errorRise),
		}
	}

	latRatio := in.LatencyP99Ms / in.LatencyThresholdMs
	errRatio := 0.0
	if in.ErrorRateThreshold == 0 {
		errRatio = 1
	} else {
		errRatio = in.ErrorRateFraction / in.ErrorRateThreshold
	}
	// At exactly both thresholds, ratios are 1 → confidence 0.5. Each signal
	// at 2× threshold adds 0.25, capped at 1.
	confidence := 0.5 + 0.25*((latRatio-1)+(errRatio-1))
	if confidence > 1 {
		confidence = 1
	}
	if confidence < 0.5 {
		confidence = 0.5
	}

	return Verdict{
		Tripped:    true,
		Confidence: confidence,
		Evidence:   evidence + " latency_spike=true error_rise=true",
	}
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
