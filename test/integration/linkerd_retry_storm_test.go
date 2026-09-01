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

// TestLinkerdRetryStormTripAndRestoreWireFormat is
// TestRetryStormTripAndRestoreWireFormat's Linkerd twin: same wire-format
// discipline, applied to internal/mesh/linkerd's ServiceProfile
// spec.retryBudget instead of Istio's VirtualService retries.attempts/
// DestinationRule connectionPool.http pair. Skips (via initLinkerdCheck)
// when Linkerd isn't installed on the target cluster.
func TestLinkerdRetryStormTripAndRestoreWireFormat(t *testing.T) {
	root := repoRoot(t)
	ctx, cl, scheme := initLinkerdCheck(t)

	t.Cleanup(func() {
		linkerdBaselineTeardown(t, ctx, cl, root)
	})

	linkerdBaselineSetup(t, root)
	logHeader(t, "Linkerd baseline applied")

	tripQ := &linkerdHostAwareQuerier{inventoryRetryStorm: true}
	r := newReconciler(cl, scheme, tripQ)

	reconcileLinkerdPolicy(t, r, ctx)

	policy := getLinkerdPolicy(t, cl, ctx)
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("after trip reconcile: phase=%s sig=%s, want Tripped/RetryStorm",
			policy.Status.Phase, policy.Status.LastSignature)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Fatalf("lastSignature = %s, want RetryStorm", policy.Status.LastSignature)
	}

	spSpec := linkerdResourceSpecJSON(t, cl, ctx, spGVK, inventoryHost)
	t.Logf("ServiceProfile spec after trip (raw JSON from apiserver):\n%s", formatJSONForLog(spSpec))

	if !bytes.Contains(spSpec, []byte(`"retryRatio":0`)) {
		t.Fatalf("ServiceProfile raw JSON missing literal \"retryRatio\":0 "+
			"— typed read cannot prove this field reached etcd: %s", spSpec)
	}
	if !bytes.Contains(spSpec, []byte(`"minRetriesPerSecond":0`)) {
		t.Fatalf("ServiceProfile raw JSON missing literal \"minRetriesPerSecond\":0: %s", spSpec)
	}
	// The pre-existing route must survive the trip untouched — this
	// Mitigator only ever manages retryBudget, relying on the fixture's
	// own isRetryable (see internal/mesh/linkerd/mitigator.go's doc
	// comment).
	if !bytes.Contains(spSpec, []byte(`"isRetryable":true`)) {
		t.Fatalf("ServiceProfile raw JSON lost the fixture's isRetryable route: %s", spSpec)
	}

	// Restore: healthy metrics, six reconciles (begin + steps 0–4 complete).
	healthyQ := &linkerdHostAwareQuerier{healthy: true}
	r.Metrics = healthyQ
	for i := range 6 {
		reconcileLinkerdPolicy(t, r, ctx)
		p := getLinkerdPolicy(t, cl, ctx)
		t.Logf("restore tick %d: phase=%s restoreStep=%d", i, p.Status.Phase, p.Status.RestoreStep)
	}

	assertLinkerdPolicyPhase(t, cl, ctx, cascadev1alpha1.PolicyPhaseNormal)

	spSpec = linkerdResourceSpecJSON(t, cl, ctx, spGVK, inventoryHost)
	t.Logf("ServiceProfile spec after restore (raw JSON from apiserver):\n%s", formatJSONForLog(spSpec))
	assertServiceProfileClean(t, spSpec, "1", "10")

	spAnnotations := linkerdResourceAnnotationsJSON(t, cl, ctx, spGVK, inventoryHost)
	if bytes.Contains(spAnnotations, []byte("cascade.gideonsanni.dev")) {
		t.Fatalf("ServiceProfile still carries an operator-managed annotation after restore: %s", spAnnotations)
	}

	policy = getLinkerdPolicy(t, cl, ctx)
	if policy.Status.LastSignature != cascadev1alpha1.SignatureRetryStorm {
		t.Fatalf("lastSignature cleared unexpectedly: %s", policy.Status.LastSignature)
	}
}
