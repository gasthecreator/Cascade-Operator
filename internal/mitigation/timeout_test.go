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
)

const testLatencyP99Ms = int32(500)

func TestApplyLatencyErrorTimeoutTripMultiRoute(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http: []*apinet.HTTPRoute{
				{
					// No explicit timeout: relies on Envoy's implicit 15s default.
					Name:  "no-explicit-timeout",
					Route: destRoute(),
				},
				{
					// Explicit timeout, longer than latencyP99Ms: must be
					// cut down to the threshold on trip.
					Name:    "explicit-longer-timeout",
					Route:   destRoute(),
					Timeout: durationpb.New(2 * time.Second),
				},
				{
					// Redirect-only: no destination, timeout meaningless.
					Name:     "redirect-only",
					Redirect: &apinet.HTTPRedirect{Uri: testRedirectURI},
				},
			},
		},
	}

	ApplyLatencyErrorTimeoutTrip(vs, testLatencyP99Ms)

	if vs.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Fatalf("managed-by = %q", vs.Annotations[AnnotationManagedBy])
	}

	wantTrip := 500 * time.Millisecond
	routes := vs.Spec.Http
	if routes[0].Timeout.AsDuration() != wantTrip {
		t.Errorf("route[0] timeout = %s, want %s", routes[0].Timeout.AsDuration(), wantTrip)
	}
	if routes[1].Timeout.AsDuration() != wantTrip {
		t.Errorf("route[1] timeout = %s, want %s (must be cut down, not left at 2s)", routes[1].Timeout.AsDuration(), wantTrip)
	}
	if routes[2].Timeout != nil {
		t.Error("redirect-only route should never get a timeout")
	}

	var snaps []originalRouteTimeoutJSON
	if err := json.Unmarshal([]byte(vs.Annotations[AnnotationOriginalTimeout]), &snaps); err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Fatalf("original snapshot has %d entries, want 3", len(snaps))
	}
	if !snaps[0].Unset {
		t.Errorf("route[0] original = %+v, want Unset", snaps[0])
	}
	if snaps[1].Unset || snaps[1].Timeout != "2s" {
		t.Errorf("route[1] original = %+v, want timeout=2s", snaps[1])
	}
	if !snaps[2].Skipped {
		t.Errorf("route[2] original = %+v, want Skipped", snaps[2])
	}
}

// TestApplyLatencyErrorTimeoutTripDoesNotOverwriteOriginal confirms the
// capture check is keyed off this function's own annotation (not
// managed-by) — the fix retries.go's ApplyRetryStormTrip also needed once
// VirtualService became a shared object kind. Seeding managed-by without
// AnnotationOriginalTimeout (as if a *different* signature, e.g. retry
// storm, had already claimed managed-by on this object) must still capture
// fresh here, not skip capture because managed-by happened to already be
// set by someone else.
func TestApplyLatencyErrorTimeoutTripDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	const kept = `[{"timeout":"3s"}]`
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testVSName,
			Namespace: testVSNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalTimeout: kept,
			},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http:  []*apinet.HTTPRoute{{Route: destRoute(), Timeout: durationpb.New(500 * time.Millisecond)}},
		},
	}

	ApplyLatencyErrorTimeoutTrip(vs, testLatencyP99Ms)

	if got := vs.Annotations[AnnotationOriginalTimeout]; got != kept {
		t.Errorf("original overwritten: %s", got)
	}
	if vs.Spec.Http[0].Timeout.AsDuration() != 500*time.Millisecond {
		t.Error("re-trip did not re-apply trip timeout")
	}
}

// TestApplyLatencyErrorTimeoutTripCapturesFreshWhenManagedByADifferentSignature
// is the actual regression case for the fix described above: managed-by
// already set (by a hypothetically different signature) but this
// function's own annotation absent.
func TestApplyLatencyErrorTimeoutTripCapturesFreshWhenManagedByADifferentSignature(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testVSName,
			Namespace:   testVSNS,
			Annotations: map[string]string{AnnotationManagedBy: ManagedByValue},
		},
		Spec: apinet.VirtualService{
			Hosts: []string{testVSHost},
			Http:  []*apinet.HTTPRoute{{Route: destRoute(), Timeout: durationpb.New(9 * time.Second)}},
		},
	}

	ApplyLatencyErrorTimeoutTrip(vs, testLatencyP99Ms)

	var snaps []originalRouteTimeoutJSON
	if err := json.Unmarshal([]byte(vs.Annotations[AnnotationOriginalTimeout]), &snaps); err != nil {
		t.Fatal(err)
	}
	if snaps[0].Timeout != "9s" {
		t.Errorf("original = %+v, want the true pre-trip 9s captured (not skipped because managed-by was already set)", snaps[0])
	}
}

func TestApplyLatencyErrorTimeoutTripEmptyHttpList(t *testing.T) {
	t.Parallel()
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: testVSName, Namespace: testVSNS},
		Spec:       apinet.VirtualService{Hosts: []string{testVSHost}},
	}
	ApplyLatencyErrorTimeoutTrip(vs, testLatencyP99Ms)
	if vs.Annotations[AnnotationOriginalTimeout] != "[]" {
		t.Errorf("original = %s, want empty array", vs.Annotations[AnnotationOriginalTimeout])
	}
}
