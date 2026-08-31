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

package mitigation

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testVSName            = "payments-service"
	testVSNS              = "default"
	testVSHost            = "payments-service.default.svc.cluster.local"
	testRetryOn5xx        = "5xx"
	testRedirectURI       = "/elsewhere"
	testKeepMeRouteID     = "keep-me"
	testRedirectOnlyRoute = "redirect-only"
)

func destRoute() []*apinet.HTTPRouteDestination {
	return []*apinet.HTTPRouteDestination{{Destination: &apinet.Destination{Host: testVSHost}}}
}

func TestApplyRetryStormTripMultiRoute(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{
					// No explicit retries: relies on Istio's implicit default.
					Name:  "no-explicit-retries",
					Route: destRoute(),
				},
				{
					// Explicit retries with other fields that must be
					// cleared on trip (still captured in the original
					// snapshot below) — Istio's validating webhook rejects
					// attempts:0 alongside a non-empty retryOn/
					// perTryTimeout/backoff, so trip must not leave them set.
					Name:  "explicit-retries",
					Route: destRoute(),
					Retries: &apinet.HTTPRetry{
						Attempts:      5,
						RetryOn:       testRetryOn5xx,
						PerTryTimeout: durationpb.New(2 * time.Second),
						Backoff:       durationpb.New(500 * time.Millisecond),
					},
				},
				{
					// Redirect-only: no destination, retries meaningless.
					Name:     testRedirectOnlyRoute,
					Redirect: &apinet.HTTPRedirect{Uri: testRedirectURI},
				},
			},
		},
	}

	ApplyRetryStormTrip(vs)

	if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Fatalf("managed-by = %q", vs.Annotations[AnnotationManagedBy])
	}

	routes := vs.Spec.Http
	if routes[0].Retries == nil || routes[0].Retries.Attempts != TripRetryAttempts {
		t.Errorf("route[0] (implicit default) retries = %+v, want attempts=%d", routes[0].Retries, TripRetryAttempts)
	}
	if routes[1].Retries.Attempts != TripRetryAttempts {
		t.Errorf("route[1] attempts = %d, want %d", routes[1].Retries.Attempts, TripRetryAttempts)
	}
	// retryOn/perTryTimeout/backoff must be cleared, not preserved: Istio's
	// validating webhook rejects attempts:0 combined with any of these set
	// ("http retry policy configured when attempts are set to 0
	// (disabled)"), confirmed live against this exact shape.
	if routes[1].Retries.RetryOn != "" {
		t.Errorf("route[1] retryOn not cleared: %q", routes[1].Retries.RetryOn)
	}
	if routes[1].Retries.PerTryTimeout != nil {
		t.Errorf("route[1] perTryTimeout not cleared: %s", routes[1].Retries.GetPerTryTimeout().AsDuration())
	}
	if routes[1].Retries.Backoff != nil {
		t.Errorf("route[1] backoff not cleared: %s", routes[1].Retries.GetBackoff().AsDuration())
	}
	if routes[2].Retries != nil {
		t.Error("skipped (no destination) route should never get a retries block")
	}

	var snaps []originalRouteRetriesJSON
	if err := json.Unmarshal([]byte(vs.Annotations[AnnotationOriginalRetries]), &snaps); err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Fatalf("original snapshot has %d entries, want 3", len(snaps))
	}
	if !snaps[0].Unset {
		t.Errorf("route[0] original = %+v, want Unset", snaps[0])
	}
	if snaps[1].Unset || snaps[1].Attempts != 5 || snaps[1].RetryOn != testRetryOn5xx ||
		snaps[1].PerTryTimeout != "2s" || snaps[1].Backoff != "500ms" {
		t.Errorf("route[1] original = %+v, want attempts=5 retryOn=%s perTryTimeout=2s backoff=500ms", snaps[1], testRetryOn5xx)
	}
	if !snaps[2].Skipped {
		t.Errorf("route[2] original = %+v, want Skipped", snaps[2])
	}
}

func TestApplyRetryStormTripDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	const kept = `[{"unset":true}]`
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testVSName,
			Namespace: testVSNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalRetries: kept,
			},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{Route: destRoute(), Retries: &apinet.HTTPRetry{Attempts: TripRetryAttempts}},
			},
		},
	}

	ApplyRetryStormTrip(vs)

	if got := vs.Annotations[AnnotationOriginalRetries]; got != kept {
		t.Errorf("original overwritten: %s", got)
	}
	if vs.Spec.Http[0].Retries.Attempts != TripRetryAttempts {
		t.Error("re-trip did not re-apply trip attempts")
	}
}

func TestApplyRetryStormTripEmptyHttpList(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec:       apinet.VirtualService{Hosts: []string{testVSHost}},
	}
	ApplyRetryStormTrip(vs)
	if vs.Annotations[AnnotationOriginalRetries] != "[]" {
		t.Errorf("original = %s, want empty array", vs.Annotations[AnnotationOriginalRetries])
	}
}

// TestRetryStormAttemptsJSONPatchContainsExplicitZero is the regression
// lock for PROPOSALS.md's omitempty finding: encoding/json of the typed
// HTTPRetry struct drops attempts:0, and the JSON Patch this package
// actually sends to the API server must not.
func TestRetryStormAttemptsJSONPatchContainsExplicitZero(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{
					Name:  "no-explicit-retries",
					Route: destRoute(),
				},
				{
					Name:  "explicit-retries",
					Route: destRoute(),
					Retries: &apinet.HTTPRetry{
						Attempts:      5,
						RetryOn:       testRetryOn5xx,
						PerTryTimeout: durationpb.New(2 * time.Second),
					},
				},
				{
					Name:     testRedirectOnlyRoute,
					Redirect: &apinet.HTTPRedirect{Uri: testRedirectURI},
				},
			},
		},
	}
	ApplyRetryStormTrip(vs)

	typed, err := json.Marshal(vs.Spec.Http[1].Retries)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(typed, []byte(`"attempts":0`)) {
		t.Fatalf("typed HTTPRetry marshal kept attempts:0 (%s); this test no longer demonstrates the omitempty bug", typed)
	}

	patch := RetryStormAttemptsJSONPatch(vs)
	if !bytes.Contains(patch, []byte(`"attempts":0`)) {
		t.Errorf("JSON Patch missing explicit attempts:0: %s", patch)
	}
	if !bytes.Contains(patch, []byte(`"/spec/http/0/retries"`)) {
		t.Errorf("JSON Patch missing route[0] retries path: %s", patch)
	}
	if !bytes.Contains(patch, []byte(`"/spec/http/1/retries"`)) {
		t.Errorf("JSON Patch missing route[1] retries path: %s", patch)
	}
	if bytes.Contains(patch, []byte(`"/spec/http/2/retries"`)) {
		t.Errorf("JSON Patch touched skipped route %q: %s", testRedirectOnlyRoute, patch)
	}
	if bytes.Contains(patch, []byte(`"retryOn"`)) {
		t.Errorf("JSON Patch left retryOn on the wire (webhook would reject): %s", patch)
	}
}

// vsWithCapturedZeroAttemptsOriginal builds a VirtualService whose route[0]
// was tripped with a *true* pre-trip Attempts of exactly 0 (an explicit
// "attempts: 0" the user or another tool had already configured, distinct
// from route[0] having no retries block at all) — the specific case PLAN.md
// §5 Phase 5 flagged: restoring this needs the exact same explicit-zero
// treatment the trip path already got, not just a route that was Unset.
func vsWithCapturedZeroAttemptsOriginal(t *testing.T) *networkingv1.VirtualService {
	t.Helper()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{
					Route:   destRoute(),
					Retries: &apinet.HTTPRetry{Attempts: 0, RetryOn: testRetryOn5xx},
				},
			},
		},
	}
	ApplyRetryStormTrip(vs)
	return vs
}

// TestRetryStormRestoreCompleteJSONPatchContainsExplicitZero is the
// restore-side regression lock for the same omitempty finding
// TestRetryStormAttemptsJSONPatchContainsExplicitZero locks on the trip
// side: a route whose true original Attempts was exactly 0 must restore to
// an explicit "attempts":0 in the wire payload, not silently drop to "no
// retries configured".
func TestRetryStormRestoreCompleteJSONPatchContainsExplicitZero(t *testing.T) {
	t.Parallel()
	vs := vsWithCapturedZeroAttemptsOriginal(t)

	if err := CompleteRetryStormRestore(vs); err != nil {
		t.Fatalf("CompleteRetryStormRestore: %v", err)
	}

	typed, err := json.Marshal(vs.Spec.Http[0].Retries)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(typed, []byte(`"attempts":0`)) {
		t.Fatalf("typed HTTPRetry marshal kept attempts:0 (%s); this test no longer demonstrates the omitempty bug", typed)
	}

	patch := RetryStormRestoreCompleteJSONPatch(vs)
	if !bytes.Contains(patch, []byte(`"attempts":0`)) {
		t.Errorf("restore-complete merge patch missing explicit attempts:0: %s", patch)
	}
	if !bytes.Contains(patch, []byte(testRetryOn5xx)) {
		t.Errorf("restore-complete merge patch dropped the true original retryOn: %s", patch)
	}
	if !bytes.Contains(patch, []byte(`"`+AnnotationManagedBy+`":null`)) {
		t.Errorf("restore-complete merge patch missing null-delete of %s: %s", AnnotationManagedBy, patch)
	}
	if !bytes.Contains(patch, []byte(`"`+AnnotationOriginalRetries+`":null`)) {
		t.Errorf("restore-complete merge patch missing null-delete of %s: %s", AnnotationOriginalRetries, patch)
	}
}

// TestRetryStormRestoreStepThenCompleteBothProduceValidZeroPatches guards
// the exact bug this fix replaced: the real production sequence is
// ApplyRetryStormRestoreStep(vs, RestoreFinalStep) on one tick (writes the
// true original into memory but leaves annotations in place — only
// CompleteRetryStormRestore strips those) followed by CompleteRetryStormRestore
// on a *later* tick, once the policy actually transitions to Normal. Both
// ticks reach the identical in-memory retries state and both used to write
// it via r.Update — switching only the completion write to a JSON Patch
// "remove" (this fix's first attempt) broke exactly this sequence, since a
// route whose retries key the *step* tick's own write already cleared made
// the *complete* tick's "remove" target nonexistent — confirmed live via
// this project's own controller-level fake-client test suite failing with
// "missing value" on the second write. The merge patch this function now
// builds must be well-formed both times.
func TestRetryStormRestoreStepThenCompleteBothProduceValidZeroPatches(t *testing.T) {
	t.Parallel()
	vs := vsWithCapturedZeroAttemptsOriginal(t)

	if err := ApplyRetryStormRestoreStep(vs, RestoreFinalStep); err != nil {
		t.Fatalf("ApplyRetryStormRestoreStep(final): %v", err)
	}
	stepPatch := RetryStormRestoreStepMergePatch(vs)
	var stepDecoded map[string]any
	if err := json.Unmarshal(stepPatch, &stepDecoded); err != nil {
		t.Fatalf("step patch not valid JSON: %v (%s)", err, stepPatch)
	}
	if !bytes.Contains(stepPatch, []byte(`"attempts":0`)) {
		t.Errorf("step patch missing explicit attempts:0: %s", stepPatch)
	}
	if _, ok := vs.Annotations[AnnotationOriginalRetries]; !ok {
		t.Fatal("test setup: ApplyRetryStormRestoreStep must not strip annotations — only CompleteRetryStormRestore does")
	}

	if err := CompleteRetryStormRestore(vs); err != nil {
		t.Fatalf("CompleteRetryStormRestore: %v", err)
	}
	completePatch := RetryStormRestoreCompleteJSONPatch(vs)
	var completeDecoded map[string]any
	if err := json.Unmarshal(completePatch, &completeDecoded); err != nil {
		t.Fatalf("complete patch not valid JSON: %v (%s)", err, completePatch)
	}
	if !bytes.Contains(completePatch, []byte(`"attempts":0`)) {
		t.Errorf("complete patch missing explicit attempts:0: %s", completePatch)
	}
	if !bytes.Contains(completePatch, []byte(`"`+AnnotationOriginalRetries+`":null`)) {
		t.Errorf("complete patch missing null-delete of %s: %s", AnnotationOriginalRetries, completePatch)
	}
}

// TestRetryStormRestoreStepMergePatchContainsExplicitZero covers the
// intermediate-ramp-step twin: lerpI32(0, target, t) can round to exactly
// 0 for a small restore target at an early step, so every tick's write —
// not only the final one — needs the same explicit-zero treatment.
func TestRetryStormRestoreStepMergePatchContainsExplicitZero(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{Route: destRoute(), Retries: &apinet.HTTPRetry{Attempts: 1, RetryOn: testRetryOn5xx}},
			},
		},
	}
	ApplyRetryStormTrip(vs)

	// step 0 of 5: lerp(TripRetryAttempts=0, target=1, t=0.2) = round(0.2) = 0.
	if err := ApplyRetryStormRestoreStep(vs, 0); err != nil {
		t.Fatalf("ApplyRetryStormRestoreStep: %v", err)
	}
	if got := vs.Spec.Http[0].Retries.GetAttempts(); got != 0 {
		t.Fatalf("test setup: step-0 interpolated attempts = %d, want 0 (adjust the fixture if lerp math changed)", got)
	}

	patch := RetryStormRestoreStepMergePatch(vs)
	if !bytes.Contains(patch, []byte(`"attempts":0`)) {
		t.Errorf("restore-step merge patch missing explicit attempts:0: %s", patch)
	}
	if bytes.Contains(patch, []byte(AnnotationManagedBy)) {
		t.Errorf("restore-step merge patch should not touch annotations (only the completion step does): %s", patch)
	}
}
