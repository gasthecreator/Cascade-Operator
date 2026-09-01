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
	"testing"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// TestLinkerdFanOutDetectOnlyNeverPatchesTheService confirms, against a
// real apiserver, that fan-out amplification detects and trips on Linkerd
// exactly like the other two signatures (status.Phase/LastSignature) but
// never mutates anything — Linkerd has no connection-pool/concurrency-
// limiting primitive (internal/mesh/linkerd/mitigator.go's own doc
// comment). Unlike the latency/error-cascade and retry-storm wire-format
// tests, there is no patched-field assertion to make here; the assertion
// that matters is the negative one — the real Service object is
// byte-for-byte the fixture's own annotations (none) before and after.
func TestLinkerdFanOutDetectOnlyNeverPatchesTheService(t *testing.T) {
	root := repoRoot(t)
	ctx, cl, scheme := initLinkerdCheck(t)

	t.Cleanup(func() {
		linkerdBaselineTeardown(t, ctx, cl, root)
	})

	linkerdBaselineSetup(t, root)
	logHeader(t, "Linkerd baseline applied")

	beforeAnnotations := linkerdResourceAnnotationsJSON(t, cl, ctx, svcGVK, inventoryName)

	tripQ := &linkerdHostAwareQuerier{inventoryFanOut: true}
	r := newReconciler(cl, scheme, tripQ)

	reconcileLinkerdPolicy(t, r, ctx)

	policy := getLinkerdPolicy(t, cl, ctx)
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("after trip reconcile: phase=%s sig=%s, want Tripped/FanOutAmplification",
			policy.Status.Phase, policy.Status.LastSignature)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureFanOutAmplification {
		t.Fatalf("lastSignature = %s, want FanOutAmplification", policy.Status.LastSignature)
	}

	afterAnnotations := linkerdResourceAnnotationsJSON(t, cl, ctx, svcGVK, inventoryName)
	t.Logf("Service annotations after trip (raw JSON from apiserver):\n%s", formatJSONForLog(afterAnnotations))
	if string(afterAnnotations) != string(beforeAnnotations) {
		t.Fatalf("Service annotations changed after a fan-out trip — Linkerd has no primitive for "+
			"this signature, nothing should ever be written:\nbefore: %s\nafter:  %s",
			beforeAnnotations, afterAnnotations)
	}

	// Restore: healthy metrics. HasManagedEdges always returns false for
	// this signature on Linkerd (internal/mesh/linkerd/mitigator.go), so
	// the very first reconcile should snap straight back to Normal without
	// a five-step ramp.
	healthyQ := &linkerdHostAwareQuerier{healthy: true}
	r.Metrics = healthyQ
	reconcileLinkerdPolicy(t, r, ctx)

	assertLinkerdPolicyPhase(t, cl, ctx, cascadev1alpha1.PolicyPhaseNormal)

	finalAnnotations := linkerdResourceAnnotationsJSON(t, cl, ctx, svcGVK, inventoryName)
	if string(finalAnnotations) != string(beforeAnnotations) {
		t.Fatalf("Service annotations changed after restore: before: %s after: %s", beforeAnnotations, finalAnnotations)
	}
}
