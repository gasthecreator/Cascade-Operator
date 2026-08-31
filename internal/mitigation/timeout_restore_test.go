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
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// managedTimeoutVS builds a VirtualService already at trip-time values
// (every forwarding route's timeout == testLatencyP99Ms), annotated as
// ours, with original as the stored pre-trip snapshot — the timeout twin
// of managedVS (retry_restore_test.go).
func managedTimeoutVS(original string) *networkingv1.VirtualService {
	trip := durationpb.New(time.Duration(testLatencyP99Ms) * time.Millisecond)
	return &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testVSName,
			Namespace: testVSNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalTimeout: original,
			},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{Route: destRoute(), Timeout: trip},
				{Route: destRoute(), Timeout: trip},
				{Redirect: &apinet.HTTPRedirect{Uri: testRedirectURI}},
			},
		},
	}
}

func TestLatencyErrorTimeoutRestoreProgressMonotonicTowardOriginal(t *testing.T) {
	t.Parallel()
	// route[0]: unset pre-trip -> ramps from 500ms toward the 15s Envoy default.
	// route[1]: explicit original timeout=5s.
	// route[2]: skipped (redirect), never touched.
	const original = `[{"unset":true},{"timeout":"5s"},{"skipped":true}]`

	var prev0, prev1 time.Duration
	for step := range RestoreFinalStep {
		vs := managedTimeoutVS(original)
		if err := ApplyLatencyErrorTimeoutRestoreStep(vs, step, testLatencyP99Ms); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		routes := vs.Spec.Http
		got0 := routes[0].Timeout.AsDuration()
		got1 := routes[1].Timeout.AsDuration()
		if step > 0 {
			if got0 < prev0 {
				t.Errorf("route[0] timeout went backwards at step %d: %s -> %s", step, prev0, got0)
			}
			if got1 < prev1 {
				t.Errorf("route[1] timeout went backwards at step %d: %s -> %s", step, prev1, got1)
			}
		}
		if got0 <= 500*time.Millisecond {
			t.Errorf("step %d route[0] timeout = %s, want > trip value 500ms", step, got0)
		}
		if got1 <= 500*time.Millisecond {
			t.Errorf("step %d route[1] timeout = %s, want > trip value 500ms", step, got1)
		}
		if routes[2].Timeout != nil {
			t.Errorf("step %d skipped route got a timeout", step)
		}
		prev0, prev1 = got0, got1
		if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
			t.Error("restore step stripped managed-by")
		}
	}
}

func TestLatencyErrorTimeoutRestoreFinalStepWritesOriginalWithoutStrippingAnnotations(t *testing.T) {
	t.Parallel()
	const original = `[{"unset":true},{"timeout":"5s"},{"skipped":true}]`
	vs := managedTimeoutVS(original)
	if err := ApplyLatencyErrorTimeoutRestoreStep(vs, RestoreFinalStep, testLatencyP99Ms); err != nil {
		t.Fatal(err)
	}
	routes := vs.Spec.Http
	if routes[0].Timeout != nil {
		t.Errorf("route[0] (unset) should be cleared at final step, got %s", routes[0].Timeout.AsDuration())
	}
	if routes[1].Timeout.AsDuration() != 5*time.Second {
		t.Errorf("route[1] timeout = %s, want 5s", routes[1].Timeout.AsDuration())
	}
	if routes[2].Timeout != nil {
		t.Error("skipped route should never get a timeout")
	}
	// Final ramp step writes the original but is not "complete" — the
	// caller (CompleteLatencyErrorTimeoutRestore) owns annotation cleanup,
	// same contract as ApplyLatencyErrorOutlierRestoreStep.
	if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Error("managed-by stripped by a restore step, not complete")
	}
}

func TestCompleteLatencyErrorTimeoutRestoreRestoresOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	const original = `[{"unset":true},{"timeout":"3s"},{"skipped":true}]`
	vs := managedTimeoutVS(original)
	if err := CompleteLatencyErrorTimeoutRestore(vs); err != nil {
		t.Fatal(err)
	}
	if vs.Annotations[AnnotationManagedBy] != "" || vs.Annotations[AnnotationOriginalTimeout] != "" {
		t.Errorf("annotations remain: %v", vs.Annotations)
	}
	routes := vs.Spec.Http
	if routes[0].Timeout != nil {
		t.Error("unset original should clear timeout, not write the 15s Envoy default")
	}
	if routes[1].Timeout.AsDuration() != 3*time.Second {
		t.Errorf("route[1] timeout = %s, want 3s", routes[1].Timeout.AsDuration())
	}
	if routes[2].Timeout != nil {
		t.Error("skipped route should never get a timeout")
	}
}

func TestCompleteLatencyErrorTimeoutRestorePreservesUnrelatedRouteFields(t *testing.T) {
	t.Parallel()
	const original = `[{"timeout":"5s"}]`
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testVSName,
			Namespace: testVSNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalTimeout: original,
			},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{
					Name:    testKeepMeRouteID,
					Route:   destRoute(),
					Retries: &apinet.HTTPRetry{Attempts: 3},
					Timeout: durationpb.New(500 * time.Millisecond),
				},
			},
		},
	}
	if err := CompleteLatencyErrorTimeoutRestore(vs); err != nil {
		t.Fatal(err)
	}
	route := vs.Spec.Http[0]
	if route.Name != testKeepMeRouteID {
		t.Error("route name clobbered")
	}
	if route.Retries.GetAttempts() != 3 {
		t.Error("route retries clobbered")
	}
	if route.Timeout.AsDuration() != 5*time.Second {
		t.Errorf("timeout = %s, want restored 5s", route.Timeout.AsDuration())
	}
}

func TestLatencyErrorTimeoutRestoreStepErrorsOnMissingOriginal(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec:       apinet.VirtualService{Hosts: []string{testVSHost}},
	}
	if err := ApplyLatencyErrorTimeoutRestoreStep(vs, 0, testLatencyP99Ms); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
	if err := CompleteLatencyErrorTimeoutRestore(vs); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
}
