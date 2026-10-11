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
	"testing"
)

// Fuzz targets for the detectors' input handling. Without -fuzz, `go test`
// runs only the seeds (f.Add values plus testdata/fuzz/<Target>/ files), so
// these double as regression tests; run the fuzzer with, for example:
//
//	go test -fuzz=FuzzDetectLatencyError -fuzztime=30s ./internal/signatures
//
// Properties asserted for every detector:
//   - never panics, whatever the readings (NaN, +/-Inf, zero, negative, huge);
//   - deterministic: the same input always yields the same Verdict;
//   - verdict shape: not tripped implies Confidence 0; tripped implies
//     Confidence in [0.5, 1];
//   - monotonic: raising a reading that already tripped never un-trips the
//     verdict and never lowers its confidence.

// checkVerdictShape asserts the Verdict invariants documented on the type.
func checkVerdictShape(t *testing.T, v Verdict) {
	t.Helper()
	if !v.Tripped {
		if v.Confidence != 0 {
			t.Fatalf("not tripped but confidence = %v: %+v", v.Confidence, v)
		}
		return
	}
	if math.IsNaN(v.Confidence) || v.Confidence < 0.5 || v.Confidence > 1 {
		t.Fatalf("tripped with confidence %v outside [0.5, 1]: %+v", v.Confidence, v)
	}
}

// raise returns v moved up by |delta|, or v unchanged if that is not finite.
func raise(v, delta float64) float64 {
	r := v + math.Abs(delta)
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return v
	}
	return r
}

func FuzzDetectLatencyError(f *testing.F) {
	f.Add("payments", 500.0, 0.05, 500.0, 0.05, 0.0, 0.0)
	f.Add("payments", 1000.0, 0.5, 500.0, 0.05, 100.0, 0.1)
	f.Add("payments", math.NaN(), 0.5, 500.0, 0.05, 1.0, 1.0)
	f.Add("payments", math.Inf(1), math.Inf(-1), 500.0, 0.05, 1.0, 1.0)
	f.Add("payments", math.MaxFloat64, 1.0, math.SmallestNonzeroFloat64, 0.0, math.MaxFloat64, 1.0)
	f.Add("", -1.0, -1.0, 0.0, -0.01, 0.0, 0.0)

	f.Fuzz(func(t *testing.T, dep string, p99, errRate, latTh, errTh, dLat, dErr float64) {
		in := LatencyErrorInput{
			Dependency: dep, LatencyP99Ms: p99, ErrorRateFraction: errRate,
			LatencyThresholdMs: latTh, ErrorRateThreshold: errTh,
		}
		v := DetectLatencyError(in)
		if again := DetectLatencyError(in); again != v {
			t.Fatalf("non-deterministic: %+v vs %+v", v, again)
		}
		checkVerdictShape(t, v)

		if !v.Tripped {
			return
		}
		up := in
		up.LatencyP99Ms = raise(in.LatencyP99Ms, dLat)
		up.ErrorRateFraction = raise(in.ErrorRateFraction, dErr)
		w := DetectLatencyError(up)
		if !w.Tripped || w.Confidence < v.Confidence {
			t.Fatalf("raising readings lowered the verdict: %+v -> %+v", v, w)
		}
	})
}

func FuzzDetectRetryStorm(f *testing.F) {
	f.Add("inventory", 3.0, 3.0, 0.0)
	f.Add("inventory", 6.0, 3.0, 1.5)
	f.Add("inventory", 1.0, 3.0, 5.0)
	f.Add("inventory", math.NaN(), 3.0, 1.0)
	f.Add("inventory", math.Inf(1), 3.0, 1.0)
	f.Add("inventory", math.MaxFloat64, math.SmallestNonzeroFloat64, math.MaxFloat64)
	f.Add("", -5.0, 0.0, 0.0)

	f.Fuzz(func(t *testing.T, dep string, ratio, mult, delta float64) {
		in := RetryStormInput{Dependency: dep, DestSourceRatio: ratio, Multiplier: mult}
		v := DetectRetryStorm(in)
		if again := DetectRetryStorm(in); again != v {
			t.Fatalf("non-deterministic: %+v vs %+v", v, again)
		}
		// A NaN multiplier is outside the detector's contract (it cannot be
		// expressed in the CRD's JSON); see
		// docs/history/worklog/2026-10-10-repo-standards.md for the finding. Everything else must satisfy the shape and monotonicity
		// properties.
		if math.IsNaN(mult) {
			return
		}
		checkVerdictShape(t, v)

		if !v.Tripped {
			return
		}
		up := in
		up.DestSourceRatio = raise(in.DestSourceRatio, delta)
		w := DetectRetryStorm(up)
		if !w.Tripped || w.Confidence < v.Confidence {
			t.Fatalf("raising the ratio lowered the verdict: %+v -> %+v", v, w)
		}
	})
}

func FuzzDetectFanOut(f *testing.F) {
	f.Add("payments", 5.0, 5.0, 0.0)
	f.Add("payments", 12.0, 5.0, 3.0)
	f.Add("payments", 1.0, 5.0, 10.0)
	f.Add("payments", math.NaN(), 5.0, 1.0)
	f.Add("payments", math.Inf(-1), 5.0, 1.0)
	f.Add("payments", math.MaxFloat64, math.SmallestNonzeroFloat64, math.MaxFloat64)
	f.Add("", 0.0, -1.0, 0.0)

	f.Fuzz(func(t *testing.T, dep string, ratio, mult, delta float64) {
		in := FanOutInput{Dependency: dep, DependencyCallerRatio: ratio, Multiplier: mult}
		v := DetectFanOut(in)
		if again := DetectFanOut(in); again != v {
			t.Fatalf("non-deterministic: %+v vs %+v", v, again)
		}
		if math.IsNaN(mult) { // see FuzzDetectRetryStorm
			return
		}
		checkVerdictShape(t, v)

		if !v.Tripped {
			return
		}
		up := in
		up.DependencyCallerRatio = raise(in.DependencyCallerRatio, delta)
		w := DetectFanOut(up)
		if !w.Tripped || w.Confidence < v.Confidence {
			t.Fatalf("raising the ratio lowered the verdict: %+v -> %+v", v, w)
		}
	})
}

func FuzzApplyKernelCorroboration(f *testing.F) {
	f.Add(true, 0.5, 129.6)
	f.Add(true, 0.95, 1.0)
	f.Add(false, 0.0, 100.0)
	f.Add(true, 0.7, 0.0)
	f.Add(true, 0.7, -3.0)
	f.Add(true, 0.7, math.NaN())
	f.Add(true, 0.7, math.Inf(1))

	f.Fuzz(func(t *testing.T, tripped bool, conf, events float64) {
		// Confidence is documented as 0 (not tripped) or within [0.5, 1].
		if math.IsNaN(conf) || conf < 0 || conf > 1 {
			return
		}
		in := Verdict{Tripped: tripped, Confidence: conf, Evidence: "base"}
		out := ApplyKernelCorroboration(in, events)
		if again := ApplyKernelCorroboration(in, events); again != out {
			t.Fatalf("non-deterministic: %+v vs %+v", out, again)
		}

		if !tripped {
			if out != in {
				t.Fatalf("a verdict that did not trip must pass through untouched: %+v -> %+v", in, out)
			}
			return
		}
		if !out.Tripped {
			t.Fatalf("corroboration must never un-trip a verdict: %+v -> %+v", in, out)
		}
		if out.Confidence < in.Confidence || out.Confidence > 1 {
			t.Fatalf("confidence must not decrease and must stay <= 1: %+v -> %+v", in, out)
		}
		if events <= 0 || math.IsNaN(events) || math.IsInf(events, 0) {
			if out != in {
				t.Fatalf("a non-positive or non-finite count must be a no-op: %+v -> %+v", in, out)
			}
		}
	})
}
