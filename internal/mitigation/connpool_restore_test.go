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

// managedConnPoolDR builds a DestinationRule already at fan-out trip-time
// values (http1MaxPendingRequests=http2MaxRequests=1), annotated as ours,
// with original as the stored pre-trip snapshot.
func managedConnPoolDR(original string) *networkingv1.DestinationRule {
	return &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:              ManagedByValue,
				AnnotationOriginalConnectionPool: original,
			},
		},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				Tls: &apinet.ClientTLSSettings{Mode: apinet.ClientTLSSettings_ISTIO_MUTUAL},
				ConnectionPool: &apinet.ConnectionPoolSettings{
					Http: &apinet.ConnectionPoolSettings_HTTPSettings{
						Http1MaxPendingRequests: TripHTTP1MaxPendingRequests,
						Http2MaxRequests:        TripHTTP2MaxRequests,
					},
				},
			},
		},
	}
}

func TestFanOutRestoreProgressMonotonicTowardOriginal(t *testing.T) {
	t.Parallel()
	// Original: http1=64, http2=128 (well below the Envoy default of 1024,
	// so this exercises the "explicit original, not Unset" ramp path).
	const original = testFanOutOriginalConnPoolJSON

	var prev1, prev2 int32
	for step := int32(0); step <= RestoreFinalStep; step++ {
		dr := managedConnPoolDR(original)
		if err := ApplyFanOutConnectionPoolRestoreStep(dr, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		http := dr.Spec.TrafficPolicy.ConnectionPool.Http
		got1 := http.GetHttp1MaxPendingRequests()
		got2 := http.GetHttp2MaxRequests()
		if step > 0 {
			if got1 < prev1 {
				t.Errorf("http1MaxPendingRequests went backwards at step %d", step)
			}
			if got2 < prev2 {
				t.Errorf("http2MaxRequests went backwards at step %d", step)
			}
		}
		prev1, prev2 = got1, got2
		if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
			t.Error("restore step stripped managed-by")
		}
		if dr.Spec.TrafficPolicy.Tls == nil {
			t.Error("TLS clobbered")
		}
	}
	if prev1 != 64 || prev2 != 128 {
		t.Errorf("final step = (%d, %d), want (64, 128)", prev1, prev2)
	}
}

func TestFanOutRestoreUnsetRampsTowardEnvoyDefault(t *testing.T) {
	t.Parallel()
	dr := managedConnPoolDR(OriginalConnectionPoolUnsetJSON)
	if err := ApplyFanOutConnectionPoolRestoreStep(dr, 0); err != nil {
		t.Fatal(err)
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	// lerp(1, 1024, 1/5) = round(1 + 1023*0.2) = round(205.6) = 206.
	if got := http.GetHttp1MaxPendingRequests(); got != 206 {
		t.Errorf("step 0 http1MaxPendingRequests = %d, want 206 (ramping toward Envoy default 1024)", got)
	}
	if got := http.GetHttp2MaxRequests(); got != 206 {
		t.Errorf("step 0 http2MaxRequests = %d, want 206 (ramping toward Envoy default 1024)", got)
	}
}

func TestCompleteFanOutConnectionPoolRestoreRestoresOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	const original = testFanOutOriginalConnPoolJSON
	dr := managedConnPoolDR(original)
	if err := CompleteFanOutConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Annotations[AnnotationManagedBy] != "" || dr.Annotations[AnnotationOriginalConnectionPool] != "" {
		t.Errorf("annotations remain: %v", dr.Annotations)
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests = %d, want 64", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests = %d, want 128", http.GetHttp2MaxRequests())
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS clobbered on complete")
	}
}

func TestCompleteFanOutConnectionPoolRestoreUnsetClearsHTTPBlock(t *testing.T) {
	t.Parallel()
	dr := managedConnPoolDR(OriginalConnectionPoolUnsetJSON)
	if err := CompleteFanOutConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.ConnectionPool != nil {
		t.Errorf("unset original should clear connectionPool entirely (no tcp left), got tp=%+v", dr.Spec.TrafficPolicy)
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS should survive clearing connectionPool")
	}
	if dr.Annotations[AnnotationManagedBy] != "" {
		t.Error("managed-by remains after complete")
	}
}

func TestCompleteFanOutConnectionPoolRestoreUnsetPreservesConnectionPoolTCP(t *testing.T) {
	t.Parallel()
	dr := managedConnPoolDR(OriginalConnectionPoolUnsetJSON)
	dr.Spec.TrafficPolicy.ConnectionPool.Tcp = &apinet.ConnectionPoolSettings_TCPSettings{MaxConnections: 50}
	if err := CompleteFanOutConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy.ConnectionPool == nil || dr.Spec.TrafficPolicy.ConnectionPool.Http != nil {
		t.Errorf("http should be cleared, tcp preserved: %+v", dr.Spec.TrafficPolicy.ConnectionPool)
	}
	if dr.Spec.TrafficPolicy.ConnectionPool.Tcp.GetMaxConnections() != 50 {
		t.Error("connectionPool.tcp clobbered while clearing http")
	}
}

func TestCompleteFanOutConnectionPoolRestoreUnsetClearsEmptyTrafficPolicy(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDRName,
			Annotations: map[string]string{
				AnnotationManagedBy:              ManagedByValue,
				AnnotationOriginalConnectionPool: OriginalConnectionPoolUnsetJSON,
			},
		},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				ConnectionPool: &apinet.ConnectionPoolSettings{
					Http: &apinet.ConnectionPoolSettings_HTTPSettings{
						Http1MaxPendingRequests: TripHTTP1MaxPendingRequests,
						Http2MaxRequests:        TripHTTP2MaxRequests,
					},
				},
			},
		},
	}
	if err := CompleteFanOutConnectionPoolRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy != nil {
		t.Errorf("empty TrafficPolicy should be nilled, got %+v", dr.Spec.TrafficPolicy)
	}
}

func TestFanOutRestoreStepErrorsOnMissingOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName},
		Spec:       apinet.DestinationRule{Host: testDRHost},
	}
	if err := ApplyFanOutConnectionPoolRestoreStep(dr, 0); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
	if err := CompleteFanOutConnectionPoolRestore(dr); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
}
