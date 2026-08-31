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

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testRetryStormOriginalConnPoolJSON = `{"maxRetries":10}`

// managedRetryConnPoolDR builds a DestinationRule already at retry storm's
// trip-time MaxRetries, annotated as ours, with original as the stored
// pre-trip snapshot. fanOutHttp1/fanOutHttp2 let a test seed fan-out's own
// fields on the same sub-message to prove retry storm's restore never
// touches them.
func managedRetryConnPoolDR(original string, fanOutHttp1, fanOutHttp2 int32) *networkingv1.DestinationRule {
	return &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:                   ManagedByValue,
				AnnotationOriginalRetryConnectionPool: original,
			},
		},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				Tls: &apinet.ClientTLSSettings{Mode: apinet.ClientTLSSettings_ISTIO_MUTUAL},
				ConnectionPool: &apinet.ConnectionPoolSettings{
					Http: &apinet.ConnectionPoolSettings_HTTPSettings{
						MaxRetries:              TripRetryStormMaxRetries,
						Http1MaxPendingRequests: fanOutHttp1,
						Http2MaxRequests:        fanOutHttp2,
					},
				},
			},
		},
	}
}

func TestRetryStormConnPoolRestoreProgressMonotonicTowardOriginal(t *testing.T) {
	t.Parallel()
	var prevRetries int32
	for step := int32(0); step <= RestoreFinalStep; step++ {
		dr := managedRetryConnPoolDR(testRetryStormOriginalConnPoolJSON, 0, 0)
		if err := ApplyRetryStormConnectionPoolRestoreStep(dr, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		http := dr.Spec.TrafficPolicy.ConnectionPool.Http
		gotRetries := http.GetMaxRetries()
		if step > 0 && gotRetries < prevRetries {
			t.Errorf("maxRetries went backwards at step %d", step)
		}
		prevRetries = gotRetries
		if http.GetHttp1MaxPendingRequests() != 0 {
			t.Errorf("step %d: http1MaxPendingRequests = %d, want 0 (never this signature's field)", step, http.GetHttp1MaxPendingRequests())
		}
		if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
			t.Error("restore step stripped managed-by")
		}
		if dr.Spec.TrafficPolicy.Tls == nil {
			t.Error("TLS clobbered")
		}
	}
	if prevRetries != 10 {
		t.Errorf("final step maxRetries = %d, want 10", prevRetries)
	}
}

func TestRetryStormConnPoolRestoreZeroRampsTowardEnvoyDefault(t *testing.T) {
	t.Parallel()
	dr := managedRetryConnPoolDR(`{}`, 0, 0)
	if err := ApplyRetryStormConnectionPoolRestoreStep(dr, 0); err != nil {
		t.Fatal(err)
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	// lerp(TripRetryStormMaxRetries=1, 3, 1/5) = round(1 + 2*0.2) = round(1.4) = 1.
	if got := http.GetMaxRetries(); got != 1 {
		t.Errorf("step 0 maxRetries = %d, want 1 (ramping toward Envoy default 3)", got)
	}
	if http.GetHttp1MaxPendingRequests() != 0 {
		t.Error("zero-original restore wrote http1MaxPendingRequests")
	}
}

// TestRetryStormConnPoolRestoreStepNeverTouchesFanOutFields is the
// restore-side twin of TestApplyRetryStormConnectionPoolTripLeavesFanOutFieldsAlone:
// every restore step, including the final one, must leave fan-out's own
// two fields exactly as it found them.
func TestRetryStormConnPoolRestoreStepNeverTouchesFanOutFields(t *testing.T) {
	t.Parallel()
	for step := int32(0); step <= RestoreFinalStep; step++ {
		dr := managedRetryConnPoolDR(testRetryStormOriginalConnPoolJSON, 64, 128)
		if err := ApplyRetryStormConnectionPoolRestoreStep(dr, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		http := dr.Spec.TrafficPolicy.ConnectionPool.Http
		if http.GetHttp1MaxPendingRequests() != 64 {
			t.Errorf("step %d: http1MaxPendingRequests = %d, want 64 (fan-out's own field, untouched)", step, http.GetHttp1MaxPendingRequests())
		}
		if http.GetHttp2MaxRequests() != 128 {
			t.Errorf("step %d: http2MaxRequests = %d, want 128 (fan-out's own field, untouched)", step, http.GetHttp2MaxRequests())
		}
	}
}

func TestCompleteRetryStormConnectionPoolRestoreRestoresOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	dr := managedRetryConnPoolDR(testRetryStormOriginalConnPoolJSON, 0, 0)
	if err := CompleteRetryStormConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Annotations[AnnotationManagedBy] != "" || dr.Annotations[AnnotationOriginalRetryConnectionPool] != "" {
		t.Errorf("annotations remain: %v", dr.Annotations)
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetMaxRetries() != 10 {
		t.Errorf("maxRetries = %d, want 10", http.GetMaxRetries())
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS clobbered on complete")
	}
}

func TestCompleteRetryStormConnectionPoolRestoreZeroWithSiblingKeepsBlock(t *testing.T) {
	t.Parallel()
	// Original {} means MaxRetries was 0/absent pre-trip. Fan-out's own
	// Http2MaxRequests is legitimately live on the same sub-message, so
	// the block must survive — we only prune when the http message is
	// empty after writing MaxRetries back to 0.
	dr := managedRetryConnPoolDR(`{}`, 0, 128)
	if err := CompleteRetryStormConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.ConnectionPool == nil || dr.Spec.TrafficPolicy.ConnectionPool.Http == nil {
		t.Fatal("http sub-message was cleared; must survive since fan-out's own field is live on it")
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetMaxRetries() != 0 {
		t.Errorf("maxRetries = %d, want 0", http.GetMaxRetries())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want 128 (fan-out's own field, must survive)", http.GetHttp2MaxRequests())
	}
}

func TestCompleteRetryStormConnectionPoolRestoreZeroPrunesEmptyShell(t *testing.T) {
	t.Parallel()
	// No sibling fields: writing MaxRetries back to 0 leaves an empty
	// http message, which is the live-observed `connectionPool.http: {}`
	// shell. Prune it (and cascade empty parents), keeping TLS.
	dr := managedRetryConnPoolDR(`{}`, 0, 0)
	if err := CompleteRetryStormConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy == nil {
		t.Fatal("trafficPolicy nilled; TLS must survive")
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS clobbered by empty-shell prune")
	}
	if dr.Spec.TrafficPolicy.ConnectionPool != nil {
		t.Errorf("connectionPool survived empty-shell prune: %+v", dr.Spec.TrafficPolicy.ConnectionPool)
	}
}

func TestCompleteRetryStormConnectionPoolRestoreNeverTouchesFanOutFields(t *testing.T) {
	t.Parallel()
	dr := managedRetryConnPoolDR(testRetryStormOriginalConnPoolJSON, 64, 128)
	if err := CompleteRetryStormConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want 64 (fan-out's own field, untouched by completion)", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want 128 (fan-out's own field, untouched by completion)", http.GetHttp2MaxRequests())
	}
}

func TestRetryStormConnPoolRestoreStepErrorsOnMissingOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName},
		Spec:       apinet.DestinationRule{Host: testDRHost},
	}
	if err := ApplyRetryStormConnectionPoolRestoreStep(dr, 0); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
	if err := CompleteRetryStormConnectionPoolRestore(dr); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
}
