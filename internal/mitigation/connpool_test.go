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

	"google.golang.org/protobuf/types/known/wrapperspb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyFanOutConnectionPoolTripFirstPatch(t *testing.T) {
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
						Http1MaxPendingRequests:  64,
						Http2MaxRequests:         128,
						MaxRequestsPerConnection: 10,
					},
				},
			},
		},
	}

	ApplyFanOutConnectionPoolTrip(dr)

	if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Fatalf("managed-by = %q", dr.Annotations[AnnotationManagedBy])
	}
	var orig originalConnectionPoolJSON
	if err := json.Unmarshal([]byte(dr.Annotations[AnnotationOriginalConnectionPool]), &orig); err != nil {
		t.Fatal(err)
	}
	if orig.Unset || orig.Http1MaxPendingRequests != 64 || orig.Http2MaxRequests != 128 {
		t.Fatalf("original snapshot = %+v, want http1=64 http2=128", orig)
	}

	http := dr.Spec.TrafficPolicy.ConnectionPool.Http
	if http.GetHttp1MaxPendingRequests() != TripHTTP1MaxPendingRequests {
		t.Errorf("http1MaxPendingRequests = %d, want %d", http.GetHttp1MaxPendingRequests(), TripHTTP1MaxPendingRequests)
	}
	if http.GetHttp2MaxRequests() != TripHTTP2MaxRequests {
		t.Errorf("http2MaxRequests = %d, want %d", http.GetHttp2MaxRequests(), TripHTTP2MaxRequests)
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

func TestApplyFanOutConnectionPoolTripUnsetOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName, Namespace: testDRNS},
		Spec:       apinet.DestinationRule{Host: testDRHost},
	}
	ApplyFanOutConnectionPoolTrip(dr)
	if dr.Annotations[AnnotationOriginalConnectionPool] != OriginalConnectionPoolUnsetJSON {
		t.Errorf("original = %s, want unset", dr.Annotations[AnnotationOriginalConnectionPool])
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.ConnectionPool == nil || dr.Spec.TrafficPolicy.ConnectionPool.Http == nil {
		t.Fatal("connectionPool.http not created")
	}
}

func TestApplyFanOutConnectionPoolTripDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:              ManagedByValue,
				AnnotationOriginalConnectionPool: `{"http1MaxPendingRequests":64,"http2MaxRequests":128}`,
			},
		},
		Spec: apinet.DestinationRule{Host: testDRHost},
	}
	ApplyFanOutConnectionPoolTrip(dr)
	if got := dr.Annotations[AnnotationOriginalConnectionPool]; got != `{"http1MaxPendingRequests":64,"http2MaxRequests":128}` {
		t.Errorf("original overwritten: %s", got)
	}
	if dr.Spec.TrafficPolicy.ConnectionPool.Http.GetHttp1MaxPendingRequests() != TripHTTP1MaxPendingRequests {
		t.Error("re-patch did not apply trip values")
	}
}

// TestApplyFanOutConnectionPoolTripLeavesOutlierDetectionAlone guards the
// shared-object-kind subtlety: fan-out and latency/error can both manage the
// same DestinationRule (disjoint field sets), so a fan-out trip must never
// touch outlierDetection even if it is already present (e.g. from a prior
// latency/error trip on the same object).
func TestApplyFanOutConnectionPoolTripLeavesOutlierDetectionAlone(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalOutlier: OriginalOutlierUnsetJSON,
			},
		},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				OutlierDetection: &apinet.OutlierDetection{
					Consecutive_5XxErrors: wrapperspb.UInt32(TripConsecutive5xx),
				},
			},
		},
	}

	// Fan-out re-trips a DestinationRule that already carries managed-by
	// from latency/error — managed-by is already ManagedByValue, so the
	// original-connection-pool snapshot is still taken fresh (fan-out has
	// never captured its own baseline on this object yet).
	ApplyFanOutConnectionPoolTrip(dr)

	if dr.Annotations[AnnotationOriginalConnectionPool] == "" {
		t.Error("fan-out did not capture its own original-connection-pool snapshot")
	}
	if dr.Spec.TrafficPolicy.OutlierDetection.GetConsecutive_5XxErrors().GetValue() != TripConsecutive5xx {
		t.Error("fan-out trip disturbed latency/error's outlierDetection field")
	}
	if dr.Annotations[AnnotationOriginalOutlier] != OriginalOutlierUnsetJSON {
		t.Error("fan-out trip disturbed latency/error's own original-outlier annotation")
	}
}
