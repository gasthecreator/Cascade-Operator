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
	"google.golang.org/protobuf/types/known/wrapperspb"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func managedDR(original string) *networkingv1.DestinationRule {
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDRName,
			Namespace: testDRNS,
			Annotations: map[string]string{
				AnnotationManagedBy:       ManagedByValue,
				AnnotationOriginalOutlier: original,
			},
		},
		Spec: apinet.DestinationRule{
			Host: testDRHost,
			TrafficPolicy: &apinet.TrafficPolicy{
				Tls: &apinet.ClientTLSSettings{Mode: apinet.ClientTLSSettings_ISTIO_MUTUAL},
				OutlierDetection: &apinet.OutlierDetection{
					Consecutive_5XxErrors: wrapperspb.UInt32(TripConsecutive5xx),
					Interval:              durationpb.New(TripInterval),
					BaseEjectionTime:      durationpb.New(TripBaseEjection),
					MaxEjectionPercent:    40,
				},
			},
		},
	}
	return dr
}

func TestRestoreProgressMonotonicTowardOriginal(t *testing.T) {
	t.Parallel()
	// Original: consecutive=7, interval=10s, baseEjectionTime=30s (same as trip).
	const original = `{"consecutive5xxErrors":7,"interval":"10s","baseEjectionTime":"30s"}`
	wantConsec := []uint32{4, 5, 5, 6, 7}
	wantInterval := []time.Duration{6 * time.Second, 7 * time.Second, 8 * time.Second, 9 * time.Second, 10 * time.Second}

	var prevConsec uint32
	var prevInterval time.Duration
	for step := int32(0); step <= RestoreFinalStep; step++ {
		dr := managedDR(original)
		if err := ApplyLatencyErrorOutlierRestoreStep(dr, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		od := dr.Spec.TrafficPolicy.OutlierDetection
		gotC := od.GetConsecutive_5XxErrors().GetValue()
		gotI := od.GetInterval().AsDuration()
		if gotC != wantConsec[step] {
			t.Errorf("step %d consecutive5xx = %d, want %d", step, gotC, wantConsec[step])
		}
		if gotI != wantInterval[step] {
			t.Errorf("step %d interval = %s, want %s", step, gotI, wantInterval[step])
		}
		if od.GetBaseEjectionTime().AsDuration() != TripBaseEjection {
			t.Errorf("step %d baseEjectionTime moved; original matched trip", step)
		}
		if step > 0 {
			if gotC < prevConsec {
				t.Errorf("consecutive5xx went backwards at step %d", step)
			}
			if gotI < prevInterval {
				t.Errorf("interval went backwards at step %d", step)
			}
		}
		prevConsec, prevInterval = gotC, gotI
		if dr.Annotations[AnnotationManagedBy] != ManagedByValue {
			t.Error("restore step stripped managed-by")
		}
		if od.MaxEjectionPercent != 40 {
			t.Errorf("maxEjectionPercent clobbered at step %d", step)
		}
		if dr.Spec.TrafficPolicy.Tls == nil {
			t.Error("TLS clobbered")
		}
	}
}

func TestCompleteRestoreRestoresOriginalAndStripsAnnotations(t *testing.T) {
	t.Parallel()
	const original = `{"consecutive5xxErrors":7,"interval":"10s"}`
	dr := managedDR(original)
	if err := CompleteLatencyErrorOutlierRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Annotations[AnnotationManagedBy] != "" || dr.Annotations[AnnotationOriginalOutlier] != "" {
		t.Errorf("annotations remain: %v", dr.Annotations)
	}
	od := dr.Spec.TrafficPolicy.OutlierDetection
	if od.GetConsecutive_5XxErrors().GetValue() != 7 {
		t.Errorf("consecutive5xx = %d, want 7", od.GetConsecutive_5XxErrors().GetValue())
	}
	if od.GetInterval().AsDuration() != 10*time.Second {
		t.Errorf("interval = %s, want 10s", od.GetInterval().AsDuration())
	}
	if od.BaseEjectionTime != nil {
		t.Error("baseEjectionTime should be nil (was unset in original)")
	}
	if od.MaxEjectionPercent != 40 {
		t.Error("maxEjectionPercent clobbered on complete")
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS clobbered on complete")
	}
}

func TestCompleteRestoreUnsetClearsOutlierDetection(t *testing.T) {
	t.Parallel()
	dr := managedDR(OriginalOutlierUnsetJSON)
	if err := CompleteLatencyErrorOutlierRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy == nil || dr.Spec.TrafficPolicy.OutlierDetection != nil {
		t.Errorf("unset original should clear outlierDetection, tp=%v", dr.Spec.TrafficPolicy)
	}
	if dr.Spec.TrafficPolicy.Tls == nil {
		t.Error("TLS should survive clearing outlierDetection")
	}
	if dr.Annotations[AnnotationManagedBy] != "" {
		t.Error("managed-by remains after complete")
	}
}

func TestCompleteRestoreUnsetClearsEmptyTrafficPolicy(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDRName,
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
					Interval:              durationpb.New(TripInterval),
					BaseEjectionTime:      durationpb.New(TripBaseEjection),
				},
			},
		},
	}
	if err := CompleteLatencyErrorOutlierRestore(dr); err != nil {
		t.Fatal(err)
	}
	if dr.Spec.TrafficPolicy != nil {
		t.Errorf("empty TrafficPolicy should be nilled, got %+v", dr.Spec.TrafficPolicy)
	}
}

func TestRestoreStepErrorsOnMissingOriginal(t *testing.T) {
	t.Parallel()
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: testDRName},
		Spec:       apinet.DestinationRule{Host: testDRHost},
	}
	if err := ApplyLatencyErrorOutlierRestoreStep(dr, 0); err == nil {
		t.Fatal("expected error for missing original annotation")
	}
}
