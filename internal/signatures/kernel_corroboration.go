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

// KernelCorroborationBoost is the fixed confidence increment
// ApplyKernelCorroboration applies (PLAN.md §5 Phase 11) — a flat, capped
// nudge rather than a proportional formula. Corroboration is a modifier on
// a verdict some other, independent signal already tripped; it must never
// be strong enough in principle to read as "the kernel signal decided
// this," only as "and a real kernel-level disruption was also observed."
const KernelCorroborationBoost = 0.15

// ApplyKernelCorroboration boosts an already-tripped Verdict's confidence
// (capped at 1) and appends evidence when kernelEventCount is a real,
// positive count of kernel-level TCP disruptions (Tetragon, watching
// tcp_send_active_reset/tcp_retransmit_skb kprobes — see
// demo/tetragon/*.yaml) observed for the same dependency in the same
// window as the readings that already tripped it.
//
// A no-op whenever v isn't already Tripped, or kernelEventCount is zero,
// negative, or non-finite: this function only ever adjusts a verdict some
// other, independent signal already tripped on its own — it can never be
// what trips a signature by itself. That is deliberate, not an
// oversight — PLAN.md §5 Phase 11 requires detection to work identically
// with Tetragon absent (kernelEventCount is always 0 in that case), and a
// kernel event alone, without a corresponding Prometheus-metric-based
// signal also crossing its own threshold, is not itself one of this
// project's three defined cascade signatures.
func ApplyKernelCorroboration(v Verdict, kernelEventCount float64) Verdict {
	if !v.Tripped || kernelEventCount <= 0 || !finite(kernelEventCount) {
		return v
	}
	v.Confidence += KernelCorroborationBoost
	if v.Confidence > 1 {
		v.Confidence = 1
	}
	v.Evidence += fmt.Sprintf(" kernel_corroboration=true kernel_events=%g", kernelEventCount)
	return v
}
