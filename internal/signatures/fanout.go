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

// FanOutInput is one dependency's request-count ratio against the caller's
// own inbound request count, plus the policy multiplier. Cross-host, unlike
// DetectRetryStorm's same-host reporter split: the numerator is the
// dependency's own request count, the denominator is spec.Service's (the
// protected/caller service) — Prometheus owns the [Ns] window and the
// cross-host division; this function does not inspect PromQL or Snapshot
// types. Implicit baseline is 1 (one dependency call per inbound call),
// confirmed on a live scrape: checkout -> {payments, inventory} held exactly
// 1:1:1 when healthy (see the fan-out-demo-evidence worklog).
type FanOutInput struct {
	Dependency            string
	DependencyCallerRatio float64
	Multiplier            float64
}

// DetectFanOut trips when a dependency's request rate is at or above
// fanOutMultiplier times the caller's own inbound rate. Inclusive (>=),
// same convention as DetectLatencyError/DetectRetryStorm. NaN/Inf (empty
// reporter, divide-by-zero) do not trip.
func DetectFanOut(in FanOutInput) Verdict {
	evidence := fmt.Sprintf(
		"dependency=%s dependency_caller_ratio=%g (threshold %g)",
		in.Dependency, in.DependencyCallerRatio, in.Multiplier,
	)

	if !finite(in.DependencyCallerRatio) {
		return Verdict{Evidence: evidence + " incomplete readings"}
	}
	if in.Multiplier <= 0 {
		return Verdict{Evidence: evidence + " invalid threshold"}
	}
	if in.DependencyCallerRatio < in.Multiplier {
		return Verdict{Evidence: evidence + " below threshold"}
	}

	// At exactly the multiplier, ratio/threshold is 1 → confidence 0.5.
	// 2× the multiplier reaches 1. Single-signal analog of DetectRetryStorm.
	rel := in.DependencyCallerRatio / in.Multiplier
	confidence := 0.5 + 0.5*(rel-1)
	if confidence > 1 {
		confidence = 1
	}

	return Verdict{
		Tripped:    true,
		Confidence: confidence,
		Evidence:   evidence + " fan_out=true",
	}
}
