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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	spv1alpha2 "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd/serviceprofile/v1alpha2"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

const (
	testPolicyName = "checkout-service"
	testNS         = "linkerd-demo"
	testDepName    = "inventory-service"
	testDepHost    = "inventory-service.linkerd-demo.svc.cluster.local"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := cascadev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := spv1alpha2.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func testMitigator(t *testing.T, objs ...client.Object) (*Mitigator, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return NewMitigator(c), c
}

func testPolicy(mode cascadev1alpha1.PolicyMode) *cascadev1alpha1.CascadePolicy {
	return &cascadev1alpha1.CascadePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: testPolicyName, Namespace: testNS},
		Spec: cascadev1alpha1.CascadePolicySpec{
			Service:   "checkout-service.linkerd-demo.svc.cluster.local",
			DependsOn: []string{testDepHost},
			Mode:      mode,
			Mesh:      cascadev1alpha1.MeshLinkerd,
		},
	}
}

func testService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: testDepName, Namespace: testNS},
	}
}

func testServiceProfile(rb *spv1alpha2.RetryBudget) *spv1alpha2.ServiceProfile {
	return &spv1alpha2.ServiceProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testDepHost, Namespace: testNS},
		Spec: spv1alpha2.ServiceProfileSpec{
			Routes: []spv1alpha2.RouteSpec{
				{Name: "GET /", Condition: spv1alpha2.RequestMatch{Method: "GET", PathRegex: "/"}, IsRetryable: true},
			},
			RetryBudget: rb,
		},
	}
}

// --- fan-out amplification: detect-only, no primitive ---

func TestApplyFanOutTripFindsServiceButPatchesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, c := testMitigator(t, testService())

	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureFanOutAmplification, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.PrimaryFound {
		t.Error("PrimaryFound = false, want true (Service exists)")
	}
	if len(outcome.AppliedKinds) != 0 {
		t.Errorf("AppliedKinds = %v, want empty (Linkerd has no fan-out primitive)", outcome.AppliedKinds)
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepName, Namespace: testNS}, svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Annotations) != 0 {
		t.Errorf("Service annotations = %v, want none written", svc.Annotations)
	}
}

func TestApplyFanOutTripMissingServiceReportsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := testMitigator(t)

	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureFanOutAmplification, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.PrimaryFound {
		t.Error("PrimaryFound = true, want false (Service absent)")
	}
}

func TestFanOutHasManagedEdgesAlwaysFalse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := testMitigator(t, testService())
	has, err := m.HasManagedEdges(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureFanOutAmplification)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("HasManagedEdges = true, want false (fan-out never manages anything on Linkerd)")
	}
}

// --- latency/error-cascade: Service failure-accrual annotations ---

func TestApplyLatencyErrorTripDetectOnlyDoesNotPatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, c := testMitigator(t, testService())

	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeDetectOnly), cascadev1alpha1.SignatureLatencyErrorCascade, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.PrimaryFound {
		t.Error("PrimaryFound = false, want true")
	}
	if len(outcome.AppliedKinds) != 0 {
		t.Errorf("AppliedKinds = %v, want empty in DetectOnly", outcome.AppliedKinds)
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepName, Namespace: testNS}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Annotations[mitigation.AnnotationManagedBy] != "" {
		t.Errorf("DetectOnly wrote managed-by: %v", svc.Annotations)
	}
}

func TestApplyLatencyErrorTripMissingServiceReportsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := testMitigator(t)
	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureLatencyErrorCascade, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.PrimaryFound {
		t.Error("PrimaryFound = true, want false (Service absent)")
	}
}

func TestApplyLatencyErrorTripFirstTripPatchesAndCapturesUnsetOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, c := testMitigator(t, testService())

	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureLatencyErrorCascade, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.AppliedKinds) != 1 || outcome.AppliedKinds[0] != KindService {
		t.Errorf("AppliedKinds = %v, want [%s]", outcome.AppliedKinds, KindService)
	}

	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepName, Namespace: testNS}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Errorf("managed-by = %q, want %q", svc.Annotations[mitigation.AnnotationManagedBy], mitigation.ManagedByValue)
	}
	if got := svc.Annotations[annotationFailureAccrualMaxFailures]; got != formatInt32(tripFailureAccrualMaxFailures) {
		t.Errorf("max-failures = %q, want %q", got, formatInt32(tripFailureAccrualMaxFailures))
	}
	orig, err := parseOriginalFailureAccrual(svc.Annotations[annotationOriginalFailureAccrual])
	if err != nil {
		t.Fatal(err)
	}
	if !orig.Unset {
		t.Errorf("captured original = %+v, want Unset (Service had no failure-accrual annotations pre-trip)", orig)
	}
}

func TestLatencyErrorRestoreRampReachesTrueOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, c := testMitigator(t, testService())
	policy := testPolicy(cascadev1alpha1.PolicyModeMitigate)

	if _, err := m.ApplyTrip(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade, testDepHost); err != nil {
		t.Fatal(err)
	}
	has, err := m.HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("HasManagedEdges = false right after trip, want true")
	}

	if err := m.ApplyRestoreStep(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade, 0); err != nil {
		t.Fatal(err)
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepName, Namespace: testNS}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Annotations[mitigation.AnnotationManagedBy] != mitigation.ManagedByValue {
		t.Error("managed-by annotation removed mid-ramp, want present until CompleteRestore")
	}

	if err := m.CompleteRestore(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepName, Namespace: testNS}, svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Annotations) != 0 {
		t.Errorf("annotations after CompleteRestore = %v, want none (original was Unset)", svc.Annotations)
	}
	has, err = m.HasManagedEdges(ctx, policy, cascadev1alpha1.SignatureLatencyErrorCascade)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("HasManagedEdges = true after CompleteRestore, want false")
	}
}

// --- retry storm: ServiceProfile.spec.retryBudget ---

func TestApplyRetryStormTripDetectOnlyDoesNotPatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := testServiceProfile(&spv1alpha2.RetryBudget{RetryRatio: 1.0, MinRetriesPerSecond: 10, TTL: defaultRetryBudgetTTL})
	m, c := testMitigator(t, sp)

	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeDetectOnly), cascadev1alpha1.SignatureRetryStorm, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.PrimaryFound {
		t.Error("PrimaryFound = false, want true")
	}
	if len(outcome.AppliedKinds) != 0 {
		t.Errorf("AppliedKinds = %v, want empty in DetectOnly", outcome.AppliedKinds)
	}
	got := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepHost, Namespace: testNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.RetryBudget.RetryRatio != 1.0 {
		t.Errorf("retryRatio = %v, want unchanged 1.0 in DetectOnly", got.Spec.RetryBudget.RetryRatio)
	}
}

func TestApplyRetryStormTripMissingServiceProfileReportsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := testMitigator(t)
	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureRetryStorm, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.PrimaryFound {
		t.Error("PrimaryFound = true, want false (ServiceProfile absent)")
	}
}

func TestApplyRetryStormTripCutsRetryBudgetAndCapturesOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := testServiceProfile(&spv1alpha2.RetryBudget{RetryRatio: 1.0, MinRetriesPerSecond: 10, TTL: defaultRetryBudgetTTL})
	m, c := testMitigator(t, sp)

	outcome, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureRetryStorm, testDepHost)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.AppliedKinds) != 1 || outcome.AppliedKinds[0] != KindServiceProfile {
		t.Errorf("AppliedKinds = %v, want [%s]", outcome.AppliedKinds, KindServiceProfile)
	}

	got := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepHost, Namespace: testNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.RetryBudget.RetryRatio != tripRetryRatio || got.Spec.RetryBudget.MinRetriesPerSecond != tripMinRetriesPerSecond {
		t.Errorf("retryBudget = %+v, want fully suppressed (ratio=%v, minRPS=%v)", got.Spec.RetryBudget, tripRetryRatio, tripMinRetriesPerSecond)
	}
	if got.Spec.RetryBudget.TTL != defaultRetryBudgetTTL {
		t.Errorf("ttl = %q, want the pre-existing budget's own ttl (10s) preserved", got.Spec.RetryBudget.TTL)
	}
	// The pre-existing route must be left untouched by this Mitigator —
	// it only manages retryBudget, relying on the fixture's own
	// isRetryable (see this package's mitigator.go doc comment).
	if len(got.Spec.Routes) != 1 || !got.Spec.Routes[0].IsRetryable {
		t.Errorf("routes = %+v, want the pre-existing retryable route unchanged", got.Spec.Routes)
	}
	orig, err := parseOriginalRetryBudget(got.Annotations[annotationOriginalRetryBudget])
	if err != nil {
		t.Fatal(err)
	}
	if orig.Unset || orig.RetryRatio == nil || *orig.RetryRatio != 1.0 {
		t.Errorf("captured original = %+v, want retryRatio=1.0 captured", orig)
	}
}

func TestApplyRetryStormTripCreatesRetryBudgetWhenPreviouslyUnset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := testServiceProfile(nil)
	m, c := testMitigator(t, sp)

	if _, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureRetryStorm, testDepHost); err != nil {
		t.Fatal(err)
	}
	got := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepHost, Namespace: testNS}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.RetryBudget == nil {
		t.Fatal("retryBudget = nil, want created (suppressed) at trip time")
	}
	orig, err := parseOriginalRetryBudget(got.Annotations[annotationOriginalRetryBudget])
	if err != nil {
		t.Fatal(err)
	}
	if !orig.Unset {
		t.Errorf("captured original = %+v, want Unset", orig)
	}
}

func TestRetryStormRestoreRampReachesTrueOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := testServiceProfile(&spv1alpha2.RetryBudget{RetryRatio: 1.0, MinRetriesPerSecond: 10, TTL: defaultRetryBudgetTTL})
	m, c := testMitigator(t, sp)
	policy := testPolicy(cascadev1alpha1.PolicyModeMitigate)

	if _, err := m.ApplyTrip(ctx, policy, cascadev1alpha1.SignatureRetryStorm, testDepHost); err != nil {
		t.Fatal(err)
	}

	if err := m.ApplyRestoreStep(ctx, policy, cascadev1alpha1.SignatureRetryStorm, 0); err != nil {
		t.Fatal(err)
	}
	mid := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepHost, Namespace: testNS}, mid); err != nil {
		t.Fatal(err)
	}
	if mid.Spec.RetryBudget.RetryRatio <= tripRetryRatio || mid.Spec.RetryBudget.RetryRatio >= 1.0 {
		t.Errorf("retryRatio mid-ramp = %v, want strictly between trip (0) and original (1.0)", mid.Spec.RetryBudget.RetryRatio)
	}

	if err := m.CompleteRestore(ctx, policy, cascadev1alpha1.SignatureRetryStorm); err != nil {
		t.Fatal(err)
	}
	final := &spv1alpha2.ServiceProfile{}
	if err := c.Get(ctx, types.NamespacedName{Name: testDepHost, Namespace: testNS}, final); err != nil {
		t.Fatal(err)
	}
	if final.Spec.RetryBudget == nil || final.Spec.RetryBudget.RetryRatio != 1.0 || final.Spec.RetryBudget.MinRetriesPerSecond != 10 {
		t.Errorf("retryBudget after CompleteRestore = %+v, want the true original (1.0, 10, 10s)", final.Spec.RetryBudget)
	}
	if len(final.Annotations) != 0 {
		t.Errorf("annotations after CompleteRestore = %v, want none", final.Annotations)
	}
}

func TestRetryStormHasManagedEdgesFalseBeforeTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := testServiceProfile(&spv1alpha2.RetryBudget{RetryRatio: 1.0, MinRetriesPerSecond: 10, TTL: defaultRetryBudgetTTL})
	m, _ := testMitigator(t, sp)
	has, err := m.HasManagedEdges(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureRetryStorm)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("HasManagedEdges = true before any trip, want false (ServiceProfile exists but isn't operator-managed yet)")
	}
}

func TestUnknownSignatureIsAnError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := testMitigator(t)
	if _, err := m.ApplyTrip(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), "NotASignature", testDepHost); err == nil {
		t.Error("ApplyTrip with an unknown signature = nil error, want one")
	}
	if _, err := m.HasManagedEdges(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), "NotASignature"); err == nil {
		t.Error("HasManagedEdges with an unknown signature = nil error, want one")
	}
	if err := m.ApplyRestoreStep(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), "NotASignature", 0); err == nil {
		t.Error("ApplyRestoreStep with an unknown signature = nil error, want one")
	}
	if err := m.CompleteRestore(ctx, testPolicy(cascadev1alpha1.PolicyModeMitigate), "NotASignature"); err == nil {
		t.Error("CompleteRestore with an unknown signature = nil error, want one")
	}
}
