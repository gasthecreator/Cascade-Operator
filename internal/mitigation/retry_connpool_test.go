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

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyRetryStormConnectionPoolTripFirstPatch(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName, Namespace: testDRNS},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				Tls: &apinet.ClientTLSSettings{Mode: apinet.ClientTLSSettings_ISTIO_MUTUAL},
				ConnectionPool: &apinet.ConnectionPoolSettings{
					Tcp: &apinet.ConnectionPoolSettings_TCPSettings{MaxConnections: 200},
					Http: &apinet.ConnectionPoolSettings_HTTPSettings{
						MaxRetries:               10,
						Http1MaxPendingRequests:  64,
						Http2MaxRequests:         128,
						MaxRequestsPerConnection: 10,
					},
				},
			},
		},
	}

	ApplyRetryStormConnectionPoolTrip(dr)

	if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Fatalf("managed-by = %q", dr.Annotations[AnnotationManagedBy])
	}
	var orig originalRetryConnectionPoolJSON
	if err := json.Unmarshal([]byte(dr.Annotations[AnnotationOriginalRetryConnectionPool]), &orig); err != nil {
		t.Fatal(err)
	}
	if orig.MaxRetries != 10 {
		t.Fatalf("original snapshot = %+v, want maxRetries=10", orig)
	}

	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetMaxRetries() != TripRetryStormMaxRetries {
		t.Errorf("maxRetries = %d, want %d", http.GetMaxRetries(), TripRetryStormMaxRetries)
	}
	// Fan-out's own fields, and every other connectionPool.http sibling,
	// must be left exactly as they were — retry storm no longer writes
	// Http1MaxPendingRequests at all (PLAN.md §2.6, overlap resolved
	// direction 2).
	if http.GetHttp1MaxPendingRequests() != 64 {
		t.Errorf("http1MaxPendingRequests clobbered: %d, want 64 (fan-out's own field)", http.GetHttp1MaxPendingRequests())
	}
	if http.GetHttp2MaxRequests() != 128 {
		t.Errorf("http2MaxRequests clobbered: %d, want 128 (fan-out's own field)", http.GetHttp2MaxRequests())
	}
	if http.GetMaxRequestsPerConnection() != 10 {
		t.Errorf("maxRequestsPerConnection clobbered: %d", http.GetMaxRequestsPerConnection())
	}
	if dr.Spec.TrafficPolicy.ConnectionPool.Tcp.GetMaxConnections() != 200 {
		t.Error("connectionPool.tcp clobbered")
	}
	if dr.Spec.TrafficPolicy.Tls == nil || dr.Spec.TrafficPolicy.Tls.Mode != apinet.ClientTLSSettings_ISTIO_MUTUAL {
		t.Error("TLS settings were clobbered")
	}
	if dr.Spec.Host != testDRHost {
		t.Errorf("host clobbered: %s", dr.Spec.Host)
	}
}

func TestApplyRetryStormConnectionPoolTripUnsetOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName, Namespace: testDRNS},
		Spec:       apinet.DestinationRule{Host: testDRHost},
	}
	ApplyRetryStormConnectionPoolTrip(dr)

	var orig originalRetryConnectionPoolJSON
	if err := json.Unmarshal([]byte(dr.Annotations[AnnotationOriginalRetryConnectionPool]), &orig); err != nil {
		t.Fatal(err)
	}
	if orig.MaxRetries != 0 {
		t.Errorf("original snapshot = %+v, want zero value (block was absent)", orig)
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.ConnectionPool == nil || dr.Spec.TrafficPolicy.ConnectionPool.Http == nil {
		t.Fatal("connectionPool.http not created")
	}
	if dr.Spec.TrafficPolicy.ConnectionPool.Http.GetHttp1MaxPendingRequests() != 0 {
		t.Error("unset original grew an http1MaxPendingRequests write")
	}
}

func TestApplyRetryStormConnectionPoolTripDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	const kept = `{"maxRetries":10}`
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:                   ManagedByValue,
				AnnotationOriginalRetryConnectionPool: kept,
			},
		},
		Spec: apinet.DestinationRule{Host: testDRHost},
	}
	ApplyRetryStormConnectionPoolTrip(dr)
	if got := dr.Annotations[AnnotationOriginalRetryConnectionPool]; got != kept {
		t.Errorf("original overwritten: %s", got)
	}
	if dr.Spec.TrafficPolicy.ConnectionPool.Http.GetMaxRetries() != TripRetryStormMaxRetries {
		t.Error("re-patch did not apply trip value")
	}
}

// TestApplyRetryStormConnectionPoolTripLeavesFanOutFieldsAlone: fan-out's
// primary can already have Http1MaxPendingRequests and Http2MaxRequests
// live on this same connectionPool.http block, and retry storm's own trip
// must never touch either — they are fan-out's fields, not a shared claim.
func TestApplyRetryStormConnectionPoolTripLeavesFanOutFieldsAlone(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:              ManagedByValue,
				AnnotationOriginalConnectionPool: testFanOutOriginalConnPoolJSON,
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

	ApplyRetryStormConnectionPoolTrip(dr)

	if dr.Annotations[AnnotationOriginalRetryConnectionPool] == "" {
		t.Error("retry storm did not capture its own original-retry-connection-pool snapshot")
	}
	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetHttp1MaxPendingRequests() != TripHTTP1MaxPendingRequests {
		t.Error("retry storm trip disturbed fan-out's own Http1MaxPendingRequests field")
	}
	if http.GetHttp2MaxRequests() != TripHTTP2MaxRequests {
		t.Error("retry storm trip disturbed fan-out's own Http2MaxRequests field")
	}
	if dr.Annotations[AnnotationOriginalConnectionPool] != testFanOutOriginalConnPoolJSON {
		t.Error("retry storm trip disturbed fan-out's own original-connection-pool annotation")
	}
	if http.GetMaxRetries() != TripRetryStormMaxRetries {
		t.Error("retry storm's own MaxRetries was not applied")
	}
}
