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
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"pgregory.net/rapid"
)

// buildRapidRetriesVS constructs a single-route VirtualService whose
// pre-trip retries state is drawn by rapid — the exact input space the
// zero-value serialization bug thread was about: an explicit attempts of
// any value including 0, or no retries block at all (Unset). Returns the
// object plus whether the route was Unset and, if not, its true original
// attempts value, so the caller can assert against it later.
func buildRapidRetriesVS(t *rapid.T) (vs *networkingv1.VirtualService, unset bool, originalAttempts int32) {
	unset = rapid.Bool().Draw(t, "unset")
	route := &apinet.HTTPRoute{Route: destRoute()}
	if !unset {
		originalAttempts = rapid.Int32Range(0, 5).Draw(t, "originalAttempts")
		retryOn := rapid.SampledFrom([]string{"", "5xx", "connect-failure,refused-stream"}).Draw(t, "retryOn")
		withTimeout := rapid.Bool().Draw(t, "withTimeout")
		route.Retries = &apinet.HTTPRetry{Attempts: originalAttempts, RetryOn: retryOn}
		if withTimeout {
			route.Retries.PerTryTimeout = durationpb.New(2 * time.Second)
		}
	}
	vs = &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec:       apinet.VirtualService{Hosts: []string{testVSHost}, Http: []*apinet.HTTPRoute{route}},
	}
	return vs, unset, originalAttempts
}

// mergePatchRouteAttempts parses a spec.http merge patch and returns route
// 0's "attempts" value (if the patch's route object has a "retries" key at
// all) — the same raw-JSON inspection retries_test.go's fixed-fixture tests
// use, generalized to any patch this file's rapid tests generate.
func mergePatchRouteAttempts(t *rapid.T, patch []byte) (attempts float64, hasRetries, hasAttempts bool) {
	var decoded map[string]any
	if err := json.Unmarshal(patch, &decoded); err != nil {
		t.Fatalf("merge patch not valid JSON: %v (%s)", err, patch)
	}
	spec, _ := decoded["spec"].(map[string]any)
	httpArr, _ := spec["http"].([]any)
	if len(httpArr) == 0 {
		t.Fatalf("merge patch spec.http missing/empty: %s", patch)
	}
	route0, _ := httpArr[0].(map[string]any)
	retries, hasRetries := route0["retries"].(map[string]any)
	if !hasRetries {
		return 0, false, false
	}
	v, hasAttempts := retries["attempts"]
	if !hasAttempts {
		return 0, true, false
	}
	f, _ := v.(float64)
	return f, true, true
}

// TestRapidRetryStormTripAlwaysPatchesExplicitZero generalizes
// TestRetryStormAttemptsJSONPatchContainsExplicitZero (a single fixed
// fixture) into a property across the whole pre-trip input space: whatever
// a route's original retries state was — any attempts value, retryOn set or
// not, a timeout present or not, or no retries block at all — trip must
// always emit a JSON Patch op with a literal "attempts":0 for that route.
func TestRapidRetryStormTripAlwaysPatchesExplicitZero(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vs, _, _ := buildRapidRetriesVS(t)
		ApplyRetryStormTrip(vs)

		if got := vs.Spec.Http[0].Retries.GetAttempts(); got != TripRetryAttempts {
			t.Fatalf("in-memory trip attempts = %d, want %d", got, TripRetryAttempts)
		}

		patch := RetryStormAttemptsJSONPatch(vs)
		var ops []map[string]any
		if err := json.Unmarshal(patch, &ops); err != nil {
			t.Fatalf("patch not valid JSON: %v (%s)", err, patch)
		}

		found := false
		for _, op := range ops {
			if op["path"] != "/spec/http/0/retries" {
				continue
			}
			found = true
			val, _ := op["value"].(map[string]any)
			attemptsVal, ok := val["attempts"]
			if !ok {
				t.Fatalf("patch op for route 0 missing attempts key entirely: %+v", op)
			}
			n, ok := attemptsVal.(float64)
			if !ok || n != 0 {
				t.Fatalf("patch attempts = %v, want explicit 0", attemptsVal)
			}
		}
		if !found {
			t.Fatal("no /spec/http/0/retries patch op emitted for the single forwarding route")
		}
	})
}

// TestRapidRetryStormRestoreAlwaysRecoversTrueOriginalAttempts generalizes
// the fixed-fixture merge-patch tests into a property across the full
// restore path: for ANY randomly generated pre-trip state — including a
// true original attempts of exactly 0, the case the whole zero-value bug
// thread was about — both the intermediate ramp step (at
// RestoreFinalStep, which writes the true original) and the completion
// patch must carry that exact value forward, never silently drop it, and
// the in-memory object must end up back at the true original too.
func TestRapidRetryStormRestoreAlwaysRecoversTrueOriginalAttempts(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vs, unset, originalAttempts := buildRapidRetriesVS(t)

		ApplyRetryStormTrip(vs)

		if err := ApplyRetryStormRestoreStep(vs, RestoreFinalStep); err != nil {
			t.Fatalf("ApplyRetryStormRestoreStep: %v", err)
		}
		stepPatch := RetryStormRestoreStepMergePatch(vs)
		assertRestoredAttempts(t, stepPatch, unset, originalAttempts)

		if err := CompleteRetryStormRestore(vs); err != nil {
			t.Fatalf("CompleteRetryStormRestore: %v", err)
		}
		completePatch := RetryStormRestoreCompleteJSONPatch(vs)
		assertRestoredAttempts(t, completePatch, unset, originalAttempts)

		if unset {
			if vs.Spec.Http[0].Retries != nil {
				t.Fatalf("Unset route restored with a non-nil retries block: %+v", vs.Spec.Http[0].Retries)
			}
		} else if got := vs.Spec.Http[0].Retries.GetAttempts(); got != originalAttempts {
			t.Fatalf("in-memory restored attempts = %d, want true original %d", got, originalAttempts)
		}

		if _, ok := vs.Annotations[AnnotationManagedBy]; ok {
			t.Fatalf("annotations not stripped after CompleteRetryStormRestore: %+v", vs.Annotations)
		}
		if _, ok := vs.Annotations[AnnotationOriginalRetries]; ok {
			t.Fatalf("original-retries annotation not stripped after CompleteRetryStormRestore: %+v", vs.Annotations)
		}
	})
}

func assertRestoredAttempts(t *rapid.T, patch []byte, unset bool, originalAttempts int32) {
	attempts, hasRetries, hasAttempts := mergePatchRouteAttempts(t, patch)
	if unset {
		// An Unset route restores to no retries block at all; if the patch
		// does carry a retries object for it regardless, it must not
		// silently omit attempts (same failure mode, different route type).
		if hasRetries && !hasAttempts {
			t.Fatalf("unset route's retries object present but missing attempts key: %s", patch)
		}
		return
	}
	if !hasRetries {
		t.Fatalf("merge patch missing retries block for a route with explicit original attempts=%d: %s", originalAttempts, patch)
	}
	if !hasAttempts {
		t.Fatalf("merge patch retries block missing attempts key entirely (the exact omitempty bug this patch exists to fix): %s", patch)
	}
	if int32(attempts) != originalAttempts {
		t.Fatalf("merge patch attempts = %v, want true original %d", attempts, originalAttempts)
	}
}
