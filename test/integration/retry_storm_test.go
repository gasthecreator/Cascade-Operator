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

// TestRetryStormTripAndRestoreWireFormat drives CascadePolicyReconciler.Reconcile
// directly against the dev Kind+Istio cluster (no operator subprocess). Prometheus
// is stubbed via hostAwareQuerier so detection is deterministic; the patches hit
// the real apiserver and are read back as unstructured JSON so a typed struct
// cannot hide omitempty or Istio translation gaps.
func TestRetryStormTripAndRestoreWireFormat(t *testing.T) {
	root := repoRoot(t)
	ctx, cl, scheme := initCheck(t)

	t.Cleanup(func() {
		baselineTeardown(t, ctx, cl, root)
	})

	baselineSetup(t, ctx, cl, root)
	logHeader(t, "baseline applied")

	tripQ := &hostAwareQuerier{inventoryRetryStorm: true}
	r := newReconciler(cl, scheme, tripQ)

	reconcilePolicy(t, r, ctx)

	policy := getPolicy(t, cl, ctx)
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("after trip reconcile: phase=%s sig=%s, want Tripped/RetryStorm",
			policy.Status.Phase, policy.Status.LastSignature)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Fatalf("lastSignature = %s, want RetryStorm", policy.Status.LastSignature)
	}

	vsSpec := resourceSpecJSON(t, cl, ctx, vsGVK, inventoryName)
	drSpec := resourceSpecJSON(t, cl, ctx, drGVK, inventoryName)

	t.Logf("VirtualService spec after trip (raw JSON from apiserver):\n%s", formatJSONForLog(vsSpec))
	t.Logf("DestinationRule spec after trip (raw JSON from apiserver):\n%s", formatJSONForLog(drSpec))

	if !bytes.Contains(vsSpec, []byte(`"attempts":0`)) {
		t.Fatalf("VirtualService raw JSON missing literal \"attempts\":0 — typed read cannot prove this field reached etcd")
	}
	if !bytes.Contains(drSpec, []byte(`"maxRetries":1`)) {
		t.Fatalf("DestinationRule raw JSON missing literal \"maxRetries\":1 — Istio Pilot ignores 0 (see PROPOSALS.md direction 2)")
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

	vsSpec = resourceSpecJSON(t, cl, ctx, vsGVK, inventoryName)
	drSpec = resourceSpecJSON(t, cl, ctx, drGVK, inventoryName)

	t.Logf("VirtualService spec after restore (raw JSON from apiserver):\n%s", formatJSONForLog(vsSpec))
	t.Logf("DestinationRule spec after restore (raw JSON from apiserver):\n%s", formatJSONForLog(drSpec))

	assertVSRetriesRestored(t, vsSpec)
	assertNoTrafficPolicy(t, drSpec)

	policy = getPolicy(t, cl, ctx)
	if policy.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Fatalf("lastSignature cleared unexpectedly: %s", policy.Status.LastSignature)
	}
}
