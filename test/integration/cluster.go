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
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/controller"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

const (
	testNamespace         = "default"
	policyName            = "checkout-service"
	inventoryName         = "inventory-service"
	defaultKubeContext    = "kind-cascade-operator"
	integrationSkipEnv    = "INTEGRATION_SKIP"
	integrationContextEnv = "INTEGRATION_CONTEXT"
)

var (
	vsGVK = schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1", Kind: "VirtualService"}
	drGVK = schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1", Kind: "DestinationRule"}
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func kubeContext() string {
	if ctx := strings.TrimSpace(os.Getenv(integrationContextEnv)); ctx != "" {
		return ctx
	}
	return defaultKubeContext
}

func kubectl(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx := kubeContext()
	full := append([]string{"--context", ctx}, args...)
	cmd := exec.Command("kubectl", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func applyDemoFixtures(t *testing.T, root string) {
	t.Helper()
	demoDir := filepath.Join(root, "demo", "k8s")
	kubectl(t, "apply", "-f", demoDir)
	crdDir := filepath.Join(root, "config", "crd", "bases")
	if _, err := os.Stat(crdDir); err == nil {
		kubectl(t, "apply", "-f", crdDir)
	}
}

func resetInventoryDestinationRule(t *testing.T) {
	t.Helper()
	// kubectl apply does not remove fields the fixture never declared; strip
	// any leftover trafficPolicy from prior manual runs (see review worklog).
	_ = exec.Command("kubectl", "--context", kubeContext(), "patch",
		"destinationrule", inventoryName, "-n", testNamespace,
		"--type=json",
		"-p", `[{"op":"remove","path":"/spec/trafficPolicy"}]`).Run()
	kubectl(t, "apply", "-f", filepath.Join(repoRoot(t), "demo", "k8s", "inventory-destinationrule.yaml"))
}

func resetCascadePolicyStatus(t *testing.T, c client.Client, ctx context.Context) {
	t.Helper()
	policy := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: policyName, Namespace: testNamespace}, policy); err != nil {
		t.Fatalf("get CascadePolicy: %v", err)
	}
	policy.Status.Phase = cascadev1alpha1.PolicyPhaseNormal
	policy.Status.RestoreStep = 0
	policy.Status.LastSignature = ""
	policy.Status.LastTrippedAt = nil
	policy.Status.Conditions = nil
	if err := c.Status().Update(ctx, policy); err != nil {
		t.Fatalf("reset CascadePolicy status: %v", err)
	}
}

func newClusterClient(t *testing.T) (client.Client, *runtime.Scheme, *rest.Config) {
	t.Helper()
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: kubeContext(),
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
	if err != nil {
		t.Fatalf("kubeconfig for context %q: %v", kubeContext(), err)
	}
	s := runtime.NewScheme()
	if err := cascadev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		t.Fatal(err)
	}
	return cl, s, cfg
}

func newReconciler(cl client.Client, scheme *runtime.Scheme, q metrics.Querier) *controller.CascadePolicyReconciler {
	return &controller.CascadePolicyReconciler{
		Client:  cl,
		Scheme:  scheme,
		Metrics: q,
	}
}

func reconcilePolicy(t *testing.T, r *controller.CascadePolicyReconciler, ctx context.Context) {
	t.Helper()
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: policyRequest()})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func resourceSpecJSON(t *testing.T, c client.Client, ctx context.Context, gvk schema.GroupVersionKind, name string) []byte {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, u); err != nil {
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

func assertPolicyPhase(t *testing.T, c client.Client, ctx context.Context, want cascadev1alpha1.PolicyPhase) {
	t.Helper()
	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: policyName, Namespace: testNamespace}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != want {
		t.Fatalf("phase = %s, want %s (lastSignature=%s restoreStep=%d)", p.Status.Phase, want, p.Status.LastSignature, p.Status.RestoreStep)
	}
}

func verifyClusterReady(t *testing.T) {
	t.Helper()
	out := kubectl(t, "cluster-info", "--request-timeout=5s")
	if !bytes.Contains(out, []byte("is running")) {
		t.Fatalf("cluster not healthy:\n%s", out)
	}
	// CascadePolicy CRD must exist on the dev cluster.
	if err := exec.Command("kubectl", "--context", kubeContext(),
		"get", "crd", "cascadepolicies.cascade.gideonsanni.dev").Run(); err != nil {
		t.Fatalf("CascadePolicy CRD missing on context %q — run make install against the dev cluster first", kubeContext())
	}
}

func skipIfDisabled(t *testing.T) {
	t.Helper()
	if os.Getenv(integrationSkipEnv) == "1" {
		t.Skip(integrationSkipEnv + "=1")
	}
}

func cleanupCascadeAnnotations(t *testing.T, name string) {
	t.Helper()
	patch := `[{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1managed-by"},{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-retries"},{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-retry-connection-pool"},{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-connection-pool"},{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-outlier"},{"op":"remove","path":"/metadata/annotations/cascade.gideonsanni.dev~1original-timeout"}]`
	for _, kind := range []string{"virtualservice", "destinationrule"} {
		_ = exec.Command("kubectl", "--context", kubeContext(), "patch",
			kind, name, "-n", testNamespace, "--type=json", "-p", patch).Run()
	}
}

func cleanupInventoryAnnotations(t *testing.T) {
	t.Helper()
	cleanupCascadeAnnotations(t, inventoryName)
}

func resetPaymentsObjects(t *testing.T, root string) {
	t.Helper()
	kubectl(t, "delete", "destinationrule", "payments-service", "-n", testNamespace, "--ignore-not-found")
	kubectl(t, "apply", "-f", filepath.Join(root, "demo", "k8s", "payments-destinationrule.yaml"))
	kubectl(t, "apply", "-f", filepath.Join(root, "demo", "k8s", "payments-virtualservice.yaml"))
}

func objectExists(t *testing.T, c client.Client, ctx context.Context, gvk schema.GroupVersionKind, name string) bool {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, u)
	return err == nil
}

func ensureInventoryVirtualService(t *testing.T, c client.Client, ctx context.Context, root string) {
	t.Helper()
	if objectExists(t, c, ctx, vsGVK, inventoryName) {
		return
	}
	kubectl(t, "apply", "-f", filepath.Join(root, "demo", "k8s", "inventory-retry-vs.yaml"))
}

func policyRequest() types.NamespacedName {
	return types.NamespacedName{Name: policyName, Namespace: testNamespace}
}

func waitForCR(ctx context.Context, c client.Client, gvk schema.GroupVersionKind, name string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	return c.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, u)
}

func formatJSONForLog(b []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return string(b)
	}
	return buf.String()
}

// hostAwareQuerier stubs Prometheus for deterministic detection. Metrics are
// not what this suite proves — wire-format through a real apiserver Patch is.
// Inventory alone gets a tripping dest:source ratio; every other host/query
// stays healthy so payments does not win the per-host detector race.
type hostAwareQuerier struct {
	inventoryRetryStorm bool
	healthy             bool
}

func (q *hostAwareQuerier) Query(_ context.Context, promql string) (metrics.Snapshot, error) {
	if q.healthy {
		return metrics.Snapshot{Samples: []metrics.Sample{{Value: 1.0}}}, nil
	}
	var v float64
	switch {
	case strings.Contains(promql, "histogram_quantile"):
		v = 80
	case strings.Contains(promql, `reporter="source"`):
		v = 1.0
		if q.inventoryRetryStorm && strings.Contains(promql, "inventory-service") {
			v = 4.0
		}
	case strings.Contains(promql, `reporter="destination"`):
		v = 1.0
	default:
		v = 0.001
	}
	return metrics.Snapshot{Samples: []metrics.Sample{{Value: v}}}, nil
}

func getPolicy(t *testing.T, c client.Client, ctx context.Context) *cascadev1alpha1.CascadePolicy {
	t.Helper()
	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, policyRequest(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertNoTrafficPolicy(t *testing.T, drSpec []byte) {
	t.Helper()
	if bytes.Contains(drSpec, []byte(`"trafficPolicy"`)) {
		t.Fatalf("DestinationRule spec still has trafficPolicy after restore:\n%s", formatJSONForLog(drSpec))
	}
}

func assertVSRetriesRestored(t *testing.T, vsSpec []byte) {
	t.Helper()
	if bytes.Contains(vsSpec, []byte(`"attempts":0`)) {
		t.Fatalf("VirtualService still has attempts:0 after restore:\n%s", formatJSONForLog(vsSpec))
	}
	if !bytes.Contains(vsSpec, []byte(`"attempts":3`)) {
		t.Fatalf("VirtualService missing restored attempts:3 from demo fixture:\n%s", formatJSONForLog(vsSpec))
	}
}

var _ metrics.Querier = (*hostAwareQuerier)(nil)

func initCheck(t *testing.T) (context.Context, client.Client, *runtime.Scheme) {
	t.Helper()
	skipIfDisabled(t)
	verifyClusterReady(t)
	ctx := context.Background()
	cl, scheme, _ := newClusterClient(t)
	return ctx, cl, scheme
}

func baselineSetup(t *testing.T, ctx context.Context, cl client.Client, root string) {
	t.Helper()
	applyDemoFixtures(t, root)
	resetPaymentsObjects(t, root)
	resetInventoryDestinationRule(t)
	ensureInventoryVirtualService(t, cl, ctx, root)
	if err := waitForCR(ctx, cl, vsGVK, inventoryName); err != nil {
		t.Fatalf("inventory VirtualService: %v", err)
	}
	if err := waitForCR(ctx, cl, drGVK, inventoryName); err != nil {
		t.Fatalf("inventory DestinationRule: %v", err)
	}
	resetCascadePolicyStatus(t, cl, ctx)
}

func baselineTeardown(t *testing.T, ctx context.Context, cl client.Client, root string) {
	t.Helper()
	cleanupInventoryAnnotations(t)
	resetInventoryDestinationRule(t)
	resetPaymentsObjects(t, root)
	kubectl(t, "apply", "-f", filepath.Join(root, "demo", "k8s", "inventory-retry-vs.yaml"))
	resetCascadePolicyStatus(t, cl, ctx)
}

func logHeader(t *testing.T, msg string) {
	t.Helper()
	t.Logf("=== %s (context=%s) ===", msg, kubeContext())
}
