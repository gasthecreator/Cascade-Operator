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

import "fmt"

// RetryStormInput is one dependency's destination:source request-count ratio
// plus the policy multiplier. Prometheus owns the [Ns] window and the
// dest/source division; this function does not inspect PromQL or Snapshot
// types. Implicit baseline is 1 (one destination attempt per source request).
type RetryStormInput struct {
	Dependency      string
	DestSourceRatio float64
	Multiplier      float64
}

// DetectRetryStorm trips when dest/source request rate is at or above
// retryStormMultiplier. Inclusive (>=) matches DetectLatencyError. NaN/Inf
// (empty rates, divide-by-zero) do not trip — Prometheus can return those
// when a reporter has no observations in the window.
func DetectRetryStorm(in RetryStormInput) Verdict {
	evidence := fmt.Sprintf(
		"dependency=%s dest_source_ratio=%g (threshold %g)",
		in.Dependency, in.DestSourceRatio, in.Multiplier,
	)

	if !finite(in.DestSourceRatio) {
		return Verdict{Evidence: evidence + " incomplete readings"}
	}
	if in.Multiplier <= 0 {
		return Verdict{Evidence: evidence + " invalid threshold"}
	}
	if in.DestSourceRatio < in.Multiplier {
		return Verdict{Evidence: evidence + " below threshold"}
	}

	// At exactly the multiplier, ratio/threshold is 1 → confidence 0.5.
	// 2× the multiplier reaches 1. Single-signal analog of DetectLatencyError.
	rel := in.DestSourceRatio / in.Multiplier
	confidence := 0.5 + 0.5*(rel-1)
	if confidence > 1 {
		confidence = 1
	}

	return Verdict{
		Tripped:    true,
		Confidence: confidence,
		Evidence:   evidence + " retry_storm=true",
	}
}
