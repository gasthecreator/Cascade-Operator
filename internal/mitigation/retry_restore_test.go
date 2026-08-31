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

// managedVS builds a VirtualService already at trip-time values (attempts=0
// on every forwarding route), annotated as ours, with original as the
// stored pre-trip snapshot.
func managedVS(original string) *networkingv1.VirtualService {
	return &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testVSName,
			Namespace: testVSNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalRetries: original,
			},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				// Trip only ever sets Attempts (ApplyRetryStormTrip), so a
				// route with a captured RetryOn already carries it at
				// trip time — this fixture must too, or the ramp test
				// below would be checking a route shape trip never
				// actually produces.
				{Route: destRoute(), Retries: &apinet.HTTPRetry{Attempts: TripRetryAttempts}},
				{Route: destRoute(), Retries: &apinet.HTTPRetry{Attempts: TripRetryAttempts, RetryOn: testRetryOn5xx}},
				{Redirect: &apinet.HTTPRedirect{Uri: testRedirectURI}},
			},
		},
	}
}

func TestRetryStormRestoreProgressMonotonicTowardOriginal(t *testing.T) {
	t.Parallel()
	// route[0]: unset pre-trip -> ramps toward implicit default 2.
	// route[1]: explicit original attempts=5, retryOn=5xx.
	// route[2]: skipped (redirect), never touched.
	const original = `[{"unset":true},{"attempts":5,"retryOn":"5xx"},{"skipped":true}]`
	wantRoute0 := []int32{0, 1, 1, 2}
	wantRoute1 := []int32{1, 2, 3, 4}

	var prev0, prev1 int32
	for step := range RestoreFinalStep {
		vs := managedVS(original)
		if err := ApplyRetryStormRestoreStep(vs, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		routes := vs.Spec.Http
		got0 := routes[0].Retries.GetAttempts()
		got1 := routes[1].Retries.GetAttempts()
		if got0 != wantRoute0[step] {
			t.Errorf("step %d route[0] attempts = %d, want %d", step, got0, wantRoute0[step])
		}
		if got1 != wantRoute1[step] {
			t.Errorf("step %d route[1] attempts = %d, want %d", step, got1, wantRoute1[step])
		}
		if routes[1].Retries.GetRetryOn() != testRetryOn5xx {
			t.Errorf("step %d route[1] retryOn clobbered mid-ramp: %q", step, routes[1].Retries.GetRetryOn())
		}
		if routes[2].Retries != nil {
			t.Errorf("step %d skipped route got a retries block", step)
		}
		if step > 0 {
			if got0 < prev0 {
				t.Errorf("route[0] attempts went backwards at step %d", step)
			}
			if got1 < prev1 {
				t.Errorf("route[1] attempts went backwards at step %d", step)
			}
		}
		prev0, prev1 = got0, got1
		if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
			t.Error("restore step stripped managed-by")
		}
	}
}

func TestRetryStormRestoreFinalStepWritesOriginalWithoutStrippingAnnotations(t *testing.T) {
	t.Parallel()
	const original = `[{"unset":true},{"attempts":5,"retryOn":"5xx","perTryTimeout":"2s"},{"skipped":true}]`
	vs := managedVS(original)
	if err := ApplyRetryStormRestoreStep(vs, RestoreFinalStep); err != nil {
		t.Fatal(err)
	}
	routes := vs.Spec.Http
	if routes[0].Retries != nil {
		t.Errorf("route[0] (unset) should be cleared at final step, got %+v", routes[0].Retries)
	}
	if routes[1].Retries.GetAttempts() != 5 || routes[1].Retries.GetRetryOn() != testRetryOn5xx {
		t.Errorf("route[1] = %+v, want attempts=5 retryOn=5xx", routes[1].Retries)
	}
	if routes[1].Retries.GetPerTryTimeout().AsDuration() != 2*time.Second {
		t.Errorf("route[1] perTryTimeout = %s, want 2s", routes[1].Retries.GetPerTryTimeout().AsDuration())
	}
	if routes[2].Retries != nil {
		t.Error("skipped route should never get a retries block")
	}
	// Final ramp step writes the original but is not "complete" — the
	// caller (CompleteRetryStormRestore) owns annotation cleanup, same
	// contract as ApplyLatencyErrorOutlierRestoreStep.
	if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Error("managed-by stripped by a restore step, not complete")
	}
}

func TestCompleteRetryStormRestoreRestoresOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	const original = `[{"unset":true},{"attempts":3,"retryOn":"5xx","backoff":"25ms"},{"skipped":true}]`
	vs := managedVS(original)
	if err := CompleteRetryStormRestore(vs); err != nil {
		t.Fatal(err)
	}
	if vs.Annotations[AnnotationManagedBy] != "" || vs.Annotations[AnnotationOriginalRetries] != "" {
		t.Errorf("annotations remain: %v", vs.Annotations)
	}
	routes := vs.Spec.Http
	if routes[0].Retries != nil {
		t.Error("unset original should clear retries, not write attempts=2")
	}
	if routes[1].Retries.GetAttempts() != 3 {
		t.Errorf("route[1] attempts = %d, want 3", routes[1].Retries.GetAttempts())
	}
	if routes[1].Retries.GetBackoff().AsDuration() != 25*time.Millisecond {
		t.Errorf("route[1] backoff = %s, want 25ms", routes[1].Retries.GetBackoff().AsDuration())
	}
	if routes[2].Retries != nil {
		t.Error("skipped route should never get a retries block")
	}
}

func TestCompleteRetryStormRestorePreservesUnrelatedRouteFields(t *testing.T) {
	t.Parallel()
	const original = `[{"attempts":5,"retryOn":"5xx"}]`
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testVSName,
			Namespace: testVSNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalRetries: original,
			},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{
					Name:    testKeepMeRouteID,
					Route:   destRoute(),
					Retries: &apinet.HTTPRetry{Attempts: TripRetryAttempts},
					Timeout: durationpb.New(3 * time.Second),
				},
			},
		},
	}
	if err := CompleteRetryStormRestore(vs); err != nil {
		t.Fatal(err)
	}
	route := vs.Spec.Http[0]
	if route.Name != testKeepMeRouteID {
		t.Error("route name clobbered")
	}
	if route.Timeout.AsDuration() != 3*time.Second {
		t.Error("route timeout clobbered")
	}
	if route.Retries.GetAttempts() != 5 {
		t.Errorf("attempts = %d, want 5", route.Retries.GetAttempts())
	}
}

func TestRetryStormRestoreStepErrorsOnMissingOriginal(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec:       apinet.VirtualService{Hosts: []string{testVSHost}},
	}
	if err := ApplyRetryStormRestoreStep(vs, 0); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
	if err := CompleteRetryStormRestore(vs); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
}

func TestIsVirtualServiceManaged(t *testing.T) {
	t.Parallel()
	if IsVirtualServiceManaged(nil) {
		t.Error("nil VirtualService reported managed")
	}
	unmanaged := &networkingv1.VirtualService{}
	if IsVirtualServiceManaged(unmanaged) {
		t.Error("VirtualService with no annotations reported managed")
	}
	managed := managedVS(`[{"unset":true}]`)
	if !IsVirtualServiceManaged(managed) {
		t.Error("annotated VirtualService not reported managed")
	}
}
