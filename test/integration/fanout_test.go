//go:build integration

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

package integration

import (
	"bytes"
	"testing"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// TestFanOutTripAndRestoreWireFormat mirrors the other two signatures'
// wire-format discipline for fan-out amplification's primary (and only —
// PLAN.md §2.6 names no secondary for v1alpha1): DestinationRule
// connectionPool.http bulkhead on inventory-service.
func TestFanOutTripAndRestoreWireFormat(t *testing.T) {
	root := repoRoot(t)
	ctx, cl, scheme := initCheck(t)

	t.Cleanup(func() {
		baselineTeardown(t, ctx, cl, root)
	})

	baselineSetup(t, ctx, cl, root)
	logHeader(t, "baseline applied")

	tripQ := &hostAwareQuerier{inventoryFanOut: true}
	r := newReconciler(cl, scheme, tripQ)

	reconcilePolicy(t, r, ctx)

	policy := getPolicy(t, cl, ctx)
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("after trip reconcile: phase=%s sig=%s, want Tripped/FanOutAmplification",
			policy.Status.Phase, policy.Status.LastSignature)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureFanOutAmplification {
		t.Fatalf("lastSignature = %s, want FanOutAmplification", policy.Status.LastSignature)
	}

	drSpec := resourceSpecJSON(t, cl, ctx, drGVK, inventoryName)
	t.Logf("DestinationRule spec after trip (raw JSON from apiserver):\n%s", formatJSONForLog(drSpec))

	if !bytes.Contains(drSpec, []byte(`"http1MaxPendingRequests":1`)) {
		t.Fatalf("DestinationRule raw JSON missing literal \"http1MaxPendingRequests\":1: %s", drSpec)
	}
	if !bytes.Contains(drSpec, []byte(`"http2MaxRequests":1`)) {
		t.Fatalf("DestinationRule raw JSON missing literal \"http2MaxRequests\":1: %s", drSpec)
	}

	// Restore: healthy metrics, six reconciles (begin + steps 0–4 complete).
	healthyQ := &hostAwareQuerier{healthy: true}
	r.Metrics = healthyQ
	for i := 0; i < 6; i++ {
		reconcilePolicy(t, r, ctx)
		p := getPolicy(t, cl, ctx)
		t.Logf("restore tick %d: phase=%s restoreStep=%d", i, p.Status.Phase, p.Status.RestoreStep)
	}

	assertPolicyPhase(t, cl, ctx, cascadev1alpha1.PolicyPhaseNormal)

	drSpec = resourceSpecJSON(t, cl, ctx, drGVK, inventoryName)
	t.Logf("DestinationRule spec after restore (raw JSON from apiserver):\n%s", formatJSONForLog(drSpec))

	assertNoTrafficPolicy(t, drSpec)

	policy = getPolicy(t, cl, ctx)
	if policy.Status.LastSignature != cascadev1alpha1.SignatureFanOutAmplification {
		t.Fatalf("lastSignature cleared unexpectedly: %s", policy.Status.LastSignature)
	}
}
