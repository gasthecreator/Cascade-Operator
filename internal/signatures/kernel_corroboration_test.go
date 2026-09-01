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

func TestApplyKernelCorroborationNoOpWhenNotTripped(t *testing.T) {
	t.Parallel()
	v := Verdict{Tripped: false, Confidence: 0, Evidence: "below threshold"}
	got := ApplyKernelCorroboration(v, 42)
	if got != v {
		t.Errorf("ApplyKernelCorroboration on an untripped verdict = %+v, want unchanged %+v", got, v)
	}
}

func TestApplyKernelCorroborationNoOpWhenNoKernelEvents(t *testing.T) {
	t.Parallel()
	v := Verdict{Tripped: true, Confidence: 0.6, Evidence: "latency_spike=true error_rise=true"}
	for _, count := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		got := ApplyKernelCorroboration(v, count)
		if got != v {
			t.Errorf("ApplyKernelCorroboration(tripped, %v) = %+v, want unchanged %+v", count, got, v)
		}
	}
}

func TestApplyKernelCorroborationBoostsConfidenceAndAppendsEvidence(t *testing.T) {
	t.Parallel()
	v := Verdict{Tripped: true, Confidence: 0.6, Evidence: "latency_spike=true error_rise=true"}
	got := ApplyKernelCorroboration(v, 12)

	if want := 0.6 + KernelCorroborationBoost; got.Confidence != want {
		t.Errorf("Confidence = %v, want %v", got.Confidence, want)
	}
	if !got.Tripped {
		t.Error("corroboration must not clear Tripped")
	}
	if !strings.Contains(got.Evidence, "kernel_corroboration=true") {
		t.Errorf("Evidence = %q, want it to mention kernel_corroboration=true", got.Evidence)
	}
	if !strings.Contains(got.Evidence, "kernel_events=12") {
		t.Errorf("Evidence = %q, want it to mention the kernel event count", got.Evidence)
	}
	if !strings.HasPrefix(got.Evidence, v.Evidence) {
		t.Errorf("Evidence = %q, want the original evidence preserved as a prefix", got.Evidence)
	}
}

func TestApplyKernelCorroborationCapsConfidenceAtOne(t *testing.T) {
	t.Parallel()
	v := Verdict{Tripped: true, Confidence: 0.95, Evidence: "some_signature_tripped=true"}
	got := ApplyKernelCorroboration(v, 5)
	if got.Confidence != 1 {
		t.Errorf("Confidence = %v, want capped at 1", got.Confidence)
	}
}
