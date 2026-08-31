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

// TestLatencyErrorCascadeTripAndRestoreWireFormat mirrors
// TestRetryStormTripAndRestoreWireFormat's discipline for the other primary
// signature that manages two object kinds on one trip: DestinationRule
// outlierDetection (primary) and VirtualService timeout (secondary), both
// on inventory-service. Reads raw apiserver JSON, not the typed struct.
func TestLatencyErrorCascadeTripAndRestoreWireFormat(t *testing.T) {
	root := repoRoot(t)
	ctx, cl, scheme := initCheck(t)

	t.Cleanup(func() {
		baselineTeardown(t, ctx, cl, root)
	})

	baselineSetup(t, ctx, cl, root)
	logHeader(t, "baseline applied")

	tripQ := &hostAwareQuerier{inventoryLatencyError: true}
	r := newReconciler(cl, scheme, tripQ)

	reconcilePolicy(t, r, ctx)

	policy := getPolicy(t, cl, ctx)
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("after trip reconcile: phase=%s sig=%s, want Tripped/LatencyErrorCascade",
			policy.Status.Phase, policy.Status.LastSignature)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Fatalf("lastSignature = %s, want LatencyErrorCascade", policy.Status.LastSignature)
	}

	drSpec := resourceSpecJSON(t, cl, ctx, drGVK, inventoryName)
	vsSpec := resourceSpecJSON(t, cl, ctx, vsGVK, inventoryName)

	t.Logf("DestinationRule spec after trip (raw JSON from apiserver):\n%s", formatJSONForLog(drSpec))
	t.Logf("VirtualService spec after trip (raw JSON from apiserver):\n%s", formatJSONForLog(vsSpec))

	if !bytes.Contains(drSpec, []byte(`"consecutive5xxErrors":3`)) {
		t.Fatalf("DestinationRule raw JSON missing literal \"consecutive5xxErrors\":3: %s", drSpec)
	}
	if !bytes.Contains(drSpec, []byte(`"interval":"5s"`)) {
		t.Fatalf("DestinationRule raw JSON missing literal \"interval\":\"5s\": %s", drSpec)
	}
	if !bytes.Contains(drSpec, []byte(`"baseEjectionTime":"30s"`)) {
		t.Fatalf("DestinationRule raw JSON missing literal \"baseEjectionTime\":\"30s\": %s", drSpec)
	}
	// Confirmed live: Istio's client-go types marshal durationpb.Duration
	// using protojson's canonical string form ("0.500s"), not Go's native
	// time.Duration.String() ("500ms") — the two coincide for whole seconds
	// (retry storm's "2s") but not here, which is exactly why this was
	// checked against a real apiserver read rather than assumed.
	if !bytes.Contains(vsSpec, []byte(`"timeout":"0.500s"`)) {
		t.Fatalf("VirtualService raw JSON missing literal \"timeout\":\"0.500s\": %s", vsSpec)
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
	vsSpec = resourceSpecJSON(t, cl, ctx, vsGVK, inventoryName)

	t.Logf("DestinationRule spec after restore (raw JSON from apiserver):\n%s", formatJSONForLog(drSpec))
	t.Logf("VirtualService spec after restore (raw JSON from apiserver):\n%s", formatJSONForLog(vsSpec))

	assertNoTrafficPolicy(t, drSpec)
	if bytes.Contains(vsSpec, []byte(`"timeout"`)) {
		t.Fatalf("VirtualService still has a timeout field after restore:\n%s", formatJSONForLog(vsSpec))
	}

	policy = getPolicy(t, cl, ctx)
	if policy.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Fatalf("lastSignature cleared unexpectedly: %s", policy.Status.LastSignature)
	}
}
