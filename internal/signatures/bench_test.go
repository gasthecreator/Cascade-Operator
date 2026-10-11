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

import "testing"

// Benchmarks for the three detectors, in the tripped and not-tripped cases
// (the tripped path also builds the longer evidence string). Run with:
//
//	go test -run='^$' -bench=. -benchmem ./internal/signatures
//
// The results sink prevents the compiler from discarding the calls.
const benchDep = "payments"

var benchSink Verdict

func BenchmarkDetectLatencyError(b *testing.B) {
	tripped := LatencyErrorInput{
		Dependency: benchDep, LatencyP99Ms: 900, ErrorRateFraction: 0.2,
		LatencyThresholdMs: 500, ErrorRateThreshold: 0.05,
	}
	healthy := tripped
	healthy.LatencyP99Ms, healthy.ErrorRateFraction = 120, 0.001

	b.Run("tripped", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchSink = DetectLatencyError(tripped)
		}
	})
	b.Run("healthy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchSink = DetectLatencyError(healthy)
		}
	})
}

func BenchmarkDetectRetryStorm(b *testing.B) {
	tripped := RetryStormInput{Dependency: depInventory, DestSourceRatio: 6, Multiplier: 3}
	healthy := RetryStormInput{Dependency: depInventory, DestSourceRatio: 1, Multiplier: 3}

	b.Run("tripped", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchSink = DetectRetryStorm(tripped)
		}
	})
	b.Run("healthy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchSink = DetectRetryStorm(healthy)
		}
	})
}

func BenchmarkDetectFanOut(b *testing.B) {
	tripped := FanOutInput{Dependency: benchDep, DependencyCallerRatio: 12, Multiplier: 5}
	healthy := FanOutInput{Dependency: benchDep, DependencyCallerRatio: 1, Multiplier: 5}

	b.Run("tripped", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchSink = DetectFanOut(tripped)
		}
	})
	b.Run("healthy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchSink = DetectFanOut(healthy)
		}
	})
}

func BenchmarkApplyKernelCorroboration(b *testing.B) {
	v := Verdict{Tripped: true, Confidence: 0.6, Evidence: "base"}
	b.ReportAllocs()
	for range b.N {
		benchSink = ApplyKernelCorroboration(v, 129.6)
	}
}
