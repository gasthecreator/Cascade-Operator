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

// managedRetryConnPoolDR builds a DestinationRule already at retry storm's
// trip-time values (maxRetries=0, http1MaxPendingRequests=1), annotated as
// ours, with original as the stored pre-trip snapshot. fanOutHttp2 lets a
// test seed fan-out's own field on the same sub-message to prove retry
// storm's restore never touches it.
func managedRetryConnPoolDR(original string, fanOutHttp2 int32) *networkingv1.DestinationRule {
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
						Http1MaxPendingRequests: TripRetryStormMaxPendingRequests,
						Http2MaxRequests:        fanOutHttp2,
					},
				},
			},
		},
	}
}

func TestRetryStormConnPoolRestoreProgressMonotonicTowardOriginal(t *testing.T) {
	t.Parallel()
	const original = `{"maxRetries":10,"http1MaxPendingRequests":64}`

	var prevRetries, prevPending int32
	for step := int32(0); step <= RestoreFinalStep; step++ {
		dr := managedRetryConnPoolDR(original, 0)
		if err := ApplyRetryStormConnectionPoolRestoreStep(dr, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		http := dr.Spec.TrafficPolicy.ConnectionPool.Http
		gotRetries := http.GetMaxRetries()
		gotPending := http.GetHttp1MaxPendingRequests()
		if step > 0 {
			if gotRetries < prevRetries {
				t.Errorf("maxRetries went backwards at step %d", step)
			}
			if gotPending < prevPending {
				t.Errorf("http1MaxPendingRequests went backwards at step %d", step)
			}
		}
		prevRetries, prevPending = gotRetries, gotPending
		if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
			t.Error("restore step stripped managed-by")
		}
		if dr.Spec.TrafficPolicy.Tls == nil {
			t.Error("TLS clobbered")
		}
	}
	if prevRetries != 10 || prevPending != 64 {
		t.Errorf("final step = (%d, %d), want (10, 64)", prevRetries, prevPending)
	}
}

func TestRetryStormConnPoolRestoreZeroRampsTowardEnvoyDefaults(t *testing.T) {
	t.Parallel()
	dr := managedRetryConnPoolDR(`{}`, 0)
	if err := ApplyRetryStormConnectionPoolRestoreStep(dr, 0); err != nil {
		t.Fatal(err)
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	// lerp(0, 3, 1/5) = round(0 + 3*0.2) = round(0.6) = 1.
	if got := http.GetMaxRetries(); got != 1 {
		t.Errorf("step 0 maxRetries = %d, want 1 (ramping toward Envoy default 3)", got)
	}
	// lerp(1, 1024, 1/5) = round(1 + 1023*0.2) = round(205.6) = 206.
	if got := http.GetHttp1MaxPendingRequests(); got != 206 {
		t.Errorf("step 0 http1MaxPendingRequests = %d, want 206 (ramping toward Envoy default 1024)", got)
	}
}

// TestRetryStormConnPoolRestoreStepNeverTouchesFanOutField is the
// restore-side twin of TestApplyRetryStormConnectionPoolTripLeavesFanOutFieldAlone:
// every restore step, including the final one, must leave
// Http2MaxRequests exactly as it found it, at every step of the ramp, not
// just on trip.
func TestRetryStormConnPoolRestoreStepNeverTouchesFanOutField(t *testing.T) {
	t.Parallel()
	const original = `{"maxRetries":10,"http1MaxPendingRequests":64}`
	for step := int32(0); step <= RestoreFinalStep; step++ {
		dr := managedRetryConnPoolDR(original, 128)
		if err := ApplyRetryStormConnectionPoolRestoreStep(dr, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if got := dr.Spec.TrafficPolicy.ConnectionPool.Http.GetHttp2MaxRequests(); got != 128 {
			t.Errorf("step %d: http2MaxRequests = %d, want 128 (fan-out's own field, untouched)", step, got)
		}
	}
}

func TestCompleteRetryStormConnectionPoolRestoreRestoresOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	const original = `{"maxRetries":10,"http1MaxPendingRequests":64}`
	dr := managedRetryConnPoolDR(original, 0)
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
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want 64", http.GetHttp1MaxPendingRequests())
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS clobbered on complete")
	}
}

func TestCompleteRetryStormConnectionPoolRestoreZeroWritesZeroNotClearBlock(t *testing.T) {
	t.Parallel()
	// Original {} means both fields were 0/absent pre-trip. Unlike
	// fan-out's whole-block Unset restore, this must write the fields back
	// to 0 in place — never nil the http sub-message, since fan-out's own
	// Http2MaxRequests (or a user's maxRequestsPerConnection) might be
	// legitimately live on it.
	dr := managedRetryConnPoolDR(`{}`, 128)
	if err := CompleteRetryStormConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.ConnectionPool == nil || dr.Spec.TrafficPolicy.ConnectionPool.Http == nil {
		t.Fatal("http sub-message was cleared; must survive since it is shared with fan-out's own field")
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetMaxRetries() != 0 {
		t.Errorf("maxRetries = %d, want 0", http.GetMaxRetries())
	}
	if http.GetHttp1MaxPendingRequests() != 0 {
		t.Errorf("http1MaxPendingRequests = %d, want 0", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want 128 (fan-out's own field, must survive)", http.GetHttp2MaxRequests())
	}
}

// TestCompleteRetryStormConnectionPoolRestoreNeverTouchesFanOutField is the
// completion-time twin of the restore-step guard above.
func TestCompleteRetryStormConnectionPoolRestoreNeverTouchesFanOutField(t *testing.T) {
	t.Parallel()
	const original = `{"maxRetries":10,"http1MaxPendingRequests":64}`
	dr := managedRetryConnPoolDR(original, 128)
	if err := CompleteRetryStormConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if got := dr.Spec.TrafficPolicy.ConnectionPool.Http.GetHttp2MaxRequests(); got != 128 {
		t.Errorf("http2MaxRequests = %d, want 128 (fan-out's own field, untouched by completion)", got)
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
