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
	"maps"
	"testing"

	"google.golang.org/protobuf/proto"
	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A restore function that cannot read its "original" annotation must fail
// loudly and leave the object exactly as it found it. If it instead
// proceeded, or stripped the annotations on the way out, the operator would
// destroy the only record of the pre-trip configuration, which could then
// never be restored. The annotation can be missing (removed by someone),
// empty, or corrupt (hand-edited, truncated, or written by another version),
// so every corruption is run against every signature's restore, both the
// per-step ramp and the final completion.

// corruptDepName is the dependency the tripped fixtures are built for.
const corruptDepName = "inventory-service"

// subject is one tripped object, seen through the pieces the test needs.
type subject struct {
	annotations func() map[string]string
	spec        func() proto.Message
	step        func(step int32) error
	complete    func() error
}

type restoreFamily struct {
	name  string
	key   string // annotation holding the original configuration
	build func() subject
}

func trippedDR(trip func(*networkingv1.DestinationRule)) *networkingv1.DestinationRule {
	dr := &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: corruptDepName, Namespace: testVSNS},
		Spec:       apinet.DestinationRule{Host: corruptDepName + ".default.svc.cluster.local"},
	}
	trip(dr)
	return dr
}

func trippedVS(trip func(*networkingv1.VirtualService)) *networkingv1.VirtualService {
	const host = corruptDepName + ".default.svc.cluster.local"
	vs := &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: corruptDepName, Namespace: testVSNS},
		Spec: apinet.VirtualService{
			Hosts: []string{host},
			Http: []*apinet.HTTPRoute{{
				Retries: &apinet.HTTPRetry{Attempts: 3},
				Route:   []*apinet.HTTPRouteDestination{{Destination: &apinet.Destination{Host: host}}},
			}},
		},
	}
	trip(vs)
	return vs
}

func drSubject(
	trip func(*networkingv1.DestinationRule),
	step func(*networkingv1.DestinationRule, int32) error,
	complete func(*networkingv1.DestinationRule) error,
) func() subject {
	return func() subject {
		dr := trippedDR(trip)
		return subject{
			annotations: func() map[string]string { return dr.Annotations },
			spec:        func() proto.Message { return &dr.Spec },
			step:        func(s int32) error { return step(dr, s) },
			complete:    func() error { return complete(dr) },
		}
	}
}

func vsSubject(
	trip func(*networkingv1.VirtualService),
	step func(*networkingv1.VirtualService, int32) error,
	complete func(*networkingv1.VirtualService) error,
) func() subject {
	return func() subject {
		vs := trippedVS(trip)
		return subject{
			annotations: func() map[string]string { return vs.Annotations },
			spec:        func() proto.Message { return &vs.Spec },
			step:        func(s int32) error { return step(vs, s) },
			complete:    func() error { return complete(vs) },
		}
	}
}

func TestRestoreWithUnreadableOriginalAnnotationChangesNothing(t *testing.T) {
	t.Parallel()

	const tripTimeoutMs = 500
	families := []restoreFamily{
		{"latency/error outlierDetection", AnnotationOriginalOutlier, drSubject(
			ApplyLatencyErrorOutlierTrip, ApplyLatencyErrorOutlierRestoreStep, CompleteLatencyErrorOutlierRestore)},
		{"fan-out connectionPool", AnnotationOriginalConnectionPool, drSubject(
			ApplyFanOutConnectionPoolTrip, ApplyFanOutConnectionPoolRestoreStep, CompleteFanOutConnectionPoolRestore)},
		{"retry storm connectionPool", AnnotationOriginalRetryConnectionPool, drSubject(
			ApplyRetryStormConnectionPoolTrip, ApplyRetryStormConnectionPoolRestoreStep, CompleteRetryStormConnectionPoolRestore)},
		{"latency/error timeout", AnnotationOriginalTimeout, vsSubject(
			func(vs *networkingv1.VirtualService) { ApplyLatencyErrorTimeoutTrip(vs, tripTimeoutMs) },
			func(vs *networkingv1.VirtualService, s int32) error {
				return ApplyLatencyErrorTimeoutRestoreStep(vs, s, tripTimeoutMs)
			},
			CompleteLatencyErrorTimeoutRestore)},
		{"retry storm retries", AnnotationOriginalRetries, vsSubject(
			ApplyRetryStormTrip, ApplyRetryStormRestoreStep, CompleteRetryStormRestore)},
	}

	corruptions := []struct {
		name  string
		apply func(a map[string]string, key string)
	}{
		{"annotation missing", func(a map[string]string, key string) { delete(a, key) }},
		{"annotation empty", func(a map[string]string, key string) { a[key] = "" }},
		{"annotation is not JSON", func(a map[string]string, key string) { a[key] = "not-json{" }},
		{"annotation is truncated JSON", func(a map[string]string, key string) { a[key] = a[key][:len(a[key])/2] }},
		{"annotation has the wrong JSON shape", func(a map[string]string, key string) { a[key] = `"just a string"` }},
	}

	calls := []struct {
		name string
		do   func(s subject) error
	}{
		{"ramp step 1", func(s subject) error { return s.step(1) }},
		{"ramp final step", func(s subject) error { return s.step(RestoreFinalStep) }},
		{"complete", func(s subject) error { return s.complete() }},
	}

	for _, fam := range families {
		for _, corr := range corruptions {
			for _, call := range calls {
				t.Run(fam.name+"/"+corr.name+"/"+call.name, func(t *testing.T) {
					t.Parallel()
					s := fam.build()
					if _, ok := s.annotations()[fam.key]; !ok {
						t.Fatalf("setup: trip did not write annotation %s", fam.key)
					}
					corr.apply(s.annotations(), fam.key)
					annosBefore := maps.Clone(s.annotations())
					specBefore := proto.Clone(s.spec())

					if err := call.do(s); err == nil {
						t.Fatal("want an error when the original annotation is unreadable")
					}
					if !maps.Equal(s.annotations(), annosBefore) {
						t.Errorf("annotations changed on failure:\n before %v\n after  %v", annosBefore, s.annotations())
					}
					if !proto.Equal(s.spec(), specBefore) {
						t.Errorf("spec changed on failure:\n before %v\n after  %v", specBefore, s.spec())
					}
				})
			}
		}
	}
}
