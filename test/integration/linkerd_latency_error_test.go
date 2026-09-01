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

// TestLinkerdLatencyErrorCascadeTripAndRestoreWireFormat is
// TestLatencyErrorCascadeTripAndRestoreWireFormat's Linkerd twin: same
// wire-format discipline (raw apiserver JSON, not a typed struct — a
// typed read cannot prove a field actually reached etcd), applied to
// internal/mesh/linkerd's failure-accrual Service annotations instead of
// Istio's DestinationRule outlierDetection/VirtualService timeout. Skips
// (via initLinkerdCheck) when Linkerd isn't installed on the target
// cluster.
func TestLinkerdLatencyErrorCascadeTripAndRestoreWireFormat(t *testing.T) {
	root := repoRoot(t)
	ctx, cl, scheme := initLinkerdCheck(t)

	t.Cleanup(func() {
		linkerdBaselineTeardown(t, ctx, cl, root)
	})

	linkerdBaselineSetup(t, root)
	logHeader(t, "Linkerd baseline applied")

	tripQ := &linkerdHostAwareQuerier{inventoryLatencyError: true}
	r := newReconciler(cl, scheme, tripQ)

	reconcileLinkerdPolicy(t, r, ctx)

	policy := getLinkerdPolicy(t, cl, ctx)
	if policy.Status.Phase != cascadev1alpha1.PolicyPhaseTripped {
		t.Fatalf("after trip reconcile: phase=%s sig=%s, want Tripped/LatencyErrorCascade",
			policy.Status.Phase, policy.Status.LastSignature)
	}
	if policy.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Fatalf("lastSignature = %s, want LatencyErrorCascade", policy.Status.LastSignature)
	}

	svcAnnotations := linkerdResourceAnnotationsJSON(t, cl, ctx, svcGVK, inventoryName)
	t.Logf("Service annotations after trip (raw JSON from apiserver):\n%s", formatJSONForLog(svcAnnotations))

	if !bytes.Contains(svcAnnotations, []byte(`"balancer.linkerd.io/failure-accrual":"consecutive"`)) {
		t.Fatalf("Service annotations missing literal failure-accrual=consecutive: %s", svcAnnotations)
	}
	if !bytes.Contains(svcAnnotations, []byte(`"balancer.linkerd.io/failure-accrual-consecutive-max-failures":"3"`)) {
		t.Fatalf("Service annotations missing literal max-failures=3: %s", svcAnnotations)
	}
	if !bytes.Contains(svcAnnotations, []byte(`"balancer.linkerd.io/failure-accrual-consecutive-min-penalty":"30s"`)) {
		t.Fatalf("Service annotations missing literal min-penalty=30s: %s", svcAnnotations)
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

	svcAnnotations = linkerdResourceAnnotationsJSON(t, cl, ctx, svcGVK, inventoryName)
	t.Logf("Service annotations after restore (raw JSON from apiserver):\n%s", formatJSONForLog(svcAnnotations))
	assertServiceAnnotationsClean(t, svcAnnotations)

	policy = getLinkerdPolicy(t, cl, ctx)
	if policy.Status.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Fatalf("lastSignature cleared unexpectedly: %s", policy.Status.LastSignature)
	}
}
