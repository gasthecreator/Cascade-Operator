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
	"google.golang.org/protobuf/types/known/wrapperspb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testDRName = "payments-service"
	testDRNS   = "default"
	testDRHost = "payments-service.default.svc.cluster.local"
)

func TestApplyLatencyErrorOutlierTripFirstPatch(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName, Namespace: testDRNS},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				Tls: &apinet.ClientTLSSettings{Mode: apinet.ClientTLSSettings_ISTIO_MUTUAL},
				OutlierDetection: &apinet.OutlierDetection{
					Consecutive_5XxErrors: wrapperspb.UInt32(7),
					Interval:              durationpb.New(50 * time.Second),
					MaxEjectionPercent:    40,
				},
			},
		},
	}

	ApplyLatencyErrorOutlierTrip(dr)

	if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
		t.Fatalf("managed-by = %q", dr.Annotations[AnnotationManagedBy])
	}
	var orig originalOutlierJSON
	if err := json.Unmarshal([]byte(dr.Annotations[AnnotationOriginalOutlier]), &orig); err != nil {
		t.Fatal(err)
	}
	if orig.Unset || orig.Consecutive5xxErrors == nil || *orig.Consecutive5xxErrors != 7 {
		t.Fatalf("original snapshot = %+v, want consecutive5xx=7", orig)
	}

	od := dr.Spec.TrafficPolicy.OutlierDetection
	if od.GetConsecutive_5XxErrors().GetValue() != TripConsecutive5xx {
		t.Errorf("consecutive5xx = %d, want %d", od.GetConsecutive_5XxErrors().GetValue(), TripConsecutive5xx)
	}
	if od.GetInterval().AsDuration() != TripInterval {
		t.Errorf("interval = %s, want %s", od.GetInterval().AsDuration(), TripInterval)
	}
	if od.GetBaseEjectionTime().AsDuration() != TripBaseEjection {
		t.Errorf("baseEjectionTime = %s, want %s", od.GetBaseEjectionTime().AsDuration(), TripBaseEjection)
	}
	if od.MaxEjectionPercent != 40 {
		t.Errorf("maxEjectionPercent clobbered: %d", od.MaxEjectionPercent)
	}
	if dr.Spec.TrafficPolicy.Tls == nil || dr.Spec.TrafficPolicy.Tls.Mode != apinet.ClientTLSSettings_ISTIO_MUTUAL {
		t.Error("TLS settings were clobbered")
	}
	if dr.Spec.Host != testDRHost {
		t.Errorf("host clobbered: %s", dr.Spec.Host)
	}
}

func TestApplyLatencyErrorOutlierTripUnsetOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName, Namespace: testDRNS},
		Spec:       apinet.DestinationRule{Host: testDRHost},
	}
	ApplyLatencyErrorOutlierTrip(dr)
	if dr.Annotations[AnnotationOriginalOutlier] != OriginalOutlierUnsetJSON {
		t.Errorf("original = %s, want unset", dr.Annotations[AnnotationOriginalOutlier])
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.OutlierDetection == nil {
		t.Fatal("outlierDetection not created")
	}
}

func TestApplyLatencyErrorOutlierTripDoesNotOverwriteOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalOutlier: `{"consecutive5xxErrors":5,"interval":"10s"}`,
			},
		},
		Spec: apinet.DestinationRule{Host: testDRHost},
	}
	ApplyLatencyErrorOutlierTrip(dr)
	if got := dr.Annotations[AnnotationOriginalOutlier]; got != `{"consecutive5xxErrors":5,"interval":"10s"}` {
		t.Errorf("original overwritten: %s", got)
	}
	if dr.Spec.TrafficPolicy.OutlierDetection.GetConsecutive_5XxErrors().GetValue() != TripConsecutive5xx {
		t.Error("re-patch did not apply trip values")
	}
}
