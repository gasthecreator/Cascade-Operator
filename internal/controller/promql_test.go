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

package controller

import (
	"testing"

	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

// The four mesh-specific query-builder functions this file used to test
// directly (latencyP99Query etc.) moved to internal/mesh/istio (PLAN.md §5
// Phase 6.1) — see internal/mesh/istio/query_builder_test.go for their
// tests now. This file keeps only the mesh-agnostic helper.
func TestSnapshotMax(t *testing.T) {
	t.Parallel()
	if _, ok := snapshotMax(metrics.Snapshot{}); ok {
		t.Fatal("empty snapshot should be missing")
	}
	s := metrics.Snapshot{Samples: []metrics.Sample{{Value: 1}, {Value: 3}, {Value: 2}}}
	v, ok := snapshotMax(s)
	if !ok || v != 3 {
		t.Fatalf("snapshotMax = %v, %v; want 3, true", v, ok)
	}
}
