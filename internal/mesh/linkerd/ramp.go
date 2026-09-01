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

package linkerd

import (
	"strconv"

	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// unsetOriginalJSON is the shared "nothing was captured, this was never
// touched pre-trip" sentinel across every original-value JSON blob this
// package stores (originalFailureAccrualJSON, originalRetryBudgetJSON) —
// one constant so goconst doesn't flag the literal repeating across both.
const unsetOriginalJSON = `{"unset":true}`

// restoreProgress maps restoreStep 0..mitigation.RestoreFinalStep onto
// (0, 1] — step 0 is the first loosening, the final step is the true
// original — identical shape and constant to
// internal/mitigation.restoreProgress (that function is unexported, so
// this is a small, deliberate duplication of the same five-step ramp
// rather than a shared dependency crossing a mesh-specific/mesh-agnostic
// package boundary the wrong way).
func restoreProgress(step int32) float64 {
	if step < 0 {
		step = 0
	}
	if step > mitigation.RestoreFinalStep {
		step = mitigation.RestoreFinalStep
	}
	return float64(step+1) / float64(mitigation.RestoreFinalStep+1)
}

func parseInt32(raw string) (int32, error) {
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(v), nil
}

func formatInt32(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}
