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
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/controller"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

// This file is cluster.go's Linkerd twin (PLAN.md §5 Phase 6.6's last
// slice) — separate helpers, not reused/generalized versions of
// cluster.go's own (testNamespace/policyName-scoped) functions, since the
// Linkerd demo topology lives in its own namespace with its own
// CascadePolicy object, not a namespace/mesh toggle on the Istio fixture's
// existing one. Genuinely shared, namespace-agnostic helpers (repoRoot,
// kubectl, kubeContext, skipIfDisabled, newClusterClient, newReconciler,
// reconcilePolicy's own reconcile.Request-based shape) are reused directly
// from cluster.go; this file exists only for what actually differs.
const linkerdNS = "linkerd-demo"

var (
	spGVK  = schema.GroupVersionKind{Group: "linkerd.io", Version: "v1alpha2", Kind: "ServiceProfile"}
	svcGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
)

// verifyLinkerdClusterReady skips (not fails) the calling test when the
// ServiceProfile CRD isn't installed — unlike verifyClusterReady's own
// CascadePolicy CRD check (a hard requirement for every test in this
// package), Linkerd is an optional second mesh: the existing CI job
// (.github/workflows/integration.yml) only runs `make istio-install`, and
// these tests must not fail it — they should skip cleanly until that
// workflow is separately updated to also run `make linkerd-install`.
func verifyLinkerdClusterReady(t *testing.T) {
	t.Helper()
	if err := exec.Command("kubectl", "--context", kubeContext(),
		"get", "crd", "serviceprofiles.linkerd.io").Run(); err != nil {
		t.Skip("Linkerd not installed on this cluster (serviceprofiles.linkerd.io CRD missing) — " +
			"run make linkerd-install first")
	}
}

// applyLinkerdDemoFixtures applies namespace.yaml first, on its own,
// before applying the rest of the directory — a real race, not a
// hypothetical one: confirmed live against a genuinely fresh cluster
// (this repo's own CI, where linkerd-demo does not pre-exist the way it
// does on a long-running local dev cluster) that a single
// `kubectl apply -f demo/k8s-linkerd` sends every file's objects in one
// batch without guaranteeing the Namespace object is visible to the
// apiserver before the namespaced objects that depend on it are created,
// producing `namespaces "linkerd-demo" not found` on several of them.
// Applying the namespace alone first, then the rest, removes the race
// instead of relying on alphabetical file ordering (which doesn't
// actually help here either — cascadepolicy.yaml/checkout.yaml sort
// before namespace.yaml).
func applyLinkerdDemoFixtures(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "demo", "k8s-linkerd")
	kubectl(t, "apply", "-f", filepath.Join(dir, "namespace.yaml"))
	kubectl(t, "apply", "-f", dir)
}

func linkerdPolicyRequestName() types.NamespacedName {
	return types.NamespacedName{Name: policyName, Namespace: linkerdNS}
}

func reconcileLinkerdPolicy(t *testing.T, r *controller.CascadePolicyReconciler, ctx context.Context) {
	t.Helper()
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: linkerdPolicyRequestName()}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func getLinkerdPolicy(t *testing.T, c client.Client, ctx context.Context) *cascadev1alpha1.CascadePolicy {
	t.Helper()
	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, linkerdPolicyRequestName(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertLinkerdPolicyPhase(t *testing.T, c client.Client, ctx context.Context, want cascadev1alpha1.PolicyPhase) {
	t.Helper()
	p := getLinkerdPolicy(t, c, ctx)
	if p.Status.Phase != want {
		t.Fatalf("phase = %s, want %s (lastSignature=%s restoreStep=%d)",
			p.Status.Phase, want, p.Status.LastSignature, p.Status.RestoreStep)
	}
}

func resetLinkerdPolicyStatus(t *testing.T, c client.Client, ctx context.Context) {
	t.Helper()
	policy := getLinkerdPolicy(t, c, ctx)
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
	policy.Status.RestoreStep = 0
	policy.Status.LastSignature = ""
	policy.Status.LastTrippedAt = nil
	policy.Status.Conditions = nil
	if err := c.Status().Update(ctx, policy); err != nil {
		t.Fatalf("reset Linkerd CascadePolicy status: %v", err)
	}
}

// linkerdResourceSpecJSON reads name's .spec (in linkerdNS) as raw JSON
// from the real apiserver — the ServiceProfile twin of cluster.go's own
// resourceSpecJSON.
func linkerdResourceSpecJSON(
	t *testing.T, c client.Client, ctx context.Context, gvk schema.GroupVersionKind, name string,
) []byte {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: linkerdNS}, u); err != nil {
		t.Fatalf("get %s/%s: %v", gvk.Kind, name, err)
	}
	spec, ok := u.Object["spec"]
	if !ok {
		return []byte("{}")
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal %s spec: %v", gvk.Kind, err)
	}
	return b
}

// linkerdResourceAnnotationsJSON reads name's .metadata.annotations (in
// linkerdNS) as raw JSON — latency/error-cascade's failure-accrual
// mitigation lives entirely in Service annotations, not spec, so this
// (not linkerdResourceSpecJSON) is the wire-format read that matters for
// that signature.
func linkerdResourceAnnotationsJSON(
	t *testing.T, c client.Client, ctx context.Context, gvk schema.GroupVersionKind, name string,
) []byte {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: linkerdNS}, u); err != nil {
		t.Fatalf("get %s/%s: %v", gvk.Kind, name, err)
	}
	b, err := json.Marshal(u.GetAnnotations())
	if err != nil {
		t.Fatalf("marshal %s annotations: %v", gvk.Kind, err)
	}
	return b
}

// resetLinkerdInventoryService strips every annotation
// applyFailureAccrualTrip/CompleteRestore could have left (a fresh
// kubectl apply of the fixture Service, unlike a fresh apply of a
// ServiceProfile's spec.retryBudget, never removes annotations the
// fixture itself never declared — same caveat cluster.go's own
// resetInventoryDestinationRule documents for Istio's DestinationRule).
func resetLinkerdInventoryService(t *testing.T) {
	t.Helper()
	patch := `[` +
		`{"op":"remove","path":"/metadata/annotations/balancer.linkerd.io~1failure-accrual"},` +
		`{"op":"remove","path":"/metadata/annotations/balancer.linkerd.io~1failure-accrual-consecutive-max-failures"},` +
		`{"op":"remove","path":"/metadata/annotations/balancer.linkerd.io~1failure-accrual-consecutive-min-penalty"},` +
		`{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1managed-by"},` +
		`{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-failure-accrual"}` +
		`]`
	_ = exec.Command("kubectl", "--context", kubeContext(), "patch",
		"service", inventoryName, "-n", linkerdNS, "--type=json", "-p", patch).Run()
}

// resetLinkerdInventoryServiceProfile strips the operator's own
// annotations, then reapplies the fixture so spec.retryBudget is back at
// its seeded original — mirrors cluster.go's own resetPaymentsObjects
// delete-then-reapply shape, except this fixture is fully owned by this
// repo (not a shared Istio object with its own lifecycle concerns), so a
// plain reapply after stripping annotations is enough; no delete needed.
func resetLinkerdInventoryServiceProfile(t *testing.T, root string) {
	t.Helper()
	patch := `[` +
		`{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1managed-by"},` +
		`{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-retry-budget"}` +
		`]`
	_ = exec.Command("kubectl", "--context", kubeContext(), "patch",
		"serviceprofile", inventoryHost, "-n", linkerdNS, "--type=json", "-p", patch).Run()
	kubectl(t, "apply", "-f", filepath.Join(root, "demo", "k8s-linkerd", "inventory-serviceprofile.yaml"))
}

const inventoryHost = "inventory-service.linkerd-demo.svc.cluster.local"

func linkerdBaselineSetup(t *testing.T, root string) {
	t.Helper()
	applyLinkerdDemoFixtures(t, root)
}

func linkerdBaselineTeardown(t *testing.T, ctx context.Context, c client.Client, root string) {
	t.Helper()
	resetLinkerdInventoryService(t)
	resetLinkerdInventoryServiceProfile(t, root)
	resetLinkerdPolicyStatus(t, c, ctx)
}

// linkerdHostAwareQuerier is hostAwareQuerier's Linkerd twin — same
// per-signature boolean-elevates-one-query-shape-above-threshold shape,
// but dispatching on internal/mesh/linkerd's own query text instead of
// Istio's reporter=/histogram_quantile substrings. Unlike hostAwareQuerier,
// disambiguating retry storm from fan-out needs no caller-host check:
// Linkerd's retry-storm query uses a metric name
// (route_actual_request_total) no other query shape ever contains, so
// checking for it is unambiguous on its own.
type linkerdHostAwareQuerier struct {
	inventoryRetryStorm   bool
	inventoryLatencyError bool
	inventoryFanOut       bool
	healthy               bool
}

//nolint:dupl // deliberately mirrors hostAwareQuerier.Query one mesh over — see this type's own doc comment.
func (q *linkerdHostAwareQuerier) Query(_ context.Context, promql string) (metrics.Snapshot, error) {
	if q.healthy {
		return metrics.Snapshot{Samples: []metrics.Sample{{Value: 1.0}}}, nil
	}
	var v float64
	switch {
	case strings.Contains(promql, "histogram_quantile"):
		v = 80
		if q.inventoryLatencyError && strings.Contains(promql, inventoryName) {
			v = 600 // above thresholds.latencyP99Ms:500
		}
	case strings.Contains(promql, "route_actual_request_total"):
		v = 1.0
		if q.inventoryRetryStorm && strings.Contains(promql, inventoryName) {
			v = 6.0 // above thresholds.retryStormMultiplier:3.0
		}
	case strings.Contains(promql, `direction="inbound"`):
		v = 1.0
		if q.inventoryFanOut && strings.Contains(promql, inventoryName) && strings.Contains(promql, "checkout-service") {
			v = 3.0 // above thresholds.fanOutMultiplier:2.0
		}
	default: // error-rate query (direction="outbound", classification="failure")
		v = 0.001
		if q.inventoryLatencyError && strings.Contains(promql, inventoryName) {
			v = 0.10 // above thresholds.errorRateFraction:0.05
		}
	}
	return metrics.Snapshot{Samples: []metrics.Sample{{Value: v}}}, nil
}

var _ metrics.Querier = (*linkerdHostAwareQuerier)(nil)

// initLinkerdCheck mirrors cluster.go's own initCheck, plus the extra
// Linkerd-specific skip (verifyLinkerdClusterReady).
func initLinkerdCheck(t *testing.T) (context.Context, client.Client, *runtime.Scheme) {
	t.Helper()
	skipIfDisabled(t)
	verifyClusterReady(t)
	verifyLinkerdClusterReady(t)
	ctx := context.Background()
	cl, scheme, _ := newClusterClient(t)
	return ctx, cl, scheme
}

func assertServiceAnnotationsClean(t *testing.T, annotationsJSON []byte) {
	t.Helper()
	if bytes.Contains(annotationsJSON, []byte("balancer.linkerd.io")) {
		t.Fatalf("Service annotations still carry a failure-accrual key after restore:\n%s",
			formatJSONForLog(annotationsJSON))
	}
	if bytes.Contains(annotationsJSON, []byte("cascade.gideonsanni.dev")) {
		t.Fatalf("Service annotations still carry an operator-managed key after restore:\n%s",
			formatJSONForLog(annotationsJSON))
	}
}

func assertServiceProfileClean(t *testing.T, drSpec []byte, wantRatio, wantMinRPS string) {
	t.Helper()
	if !bytes.Contains(drSpec, []byte(`"retryRatio":`+wantRatio)) {
		t.Fatalf("ServiceProfile raw JSON missing literal \"retryRatio\":%s after restore: %s", wantRatio, drSpec)
	}
	if !bytes.Contains(drSpec, []byte(`"minRetriesPerSecond":`+wantMinRPS)) {
		t.Fatalf("ServiceProfile raw JSON missing literal \"minRetriesPerSecond\":%s after restore: %s", wantMinRPS, drSpec)
	}
}
