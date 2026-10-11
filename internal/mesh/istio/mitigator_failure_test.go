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

package istio

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	apinet "istio.io/api/networking/v1alpha3"
	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Failure-path tests for the Istio Mitigator. The happy paths are exercised
// through internal/controller's Reconcile tests; what was missing is what
// the Mitigator does when the API server misbehaves: a failed read, a
// conflicting or rejected write, or a failure halfway through a
// two-object-kind trip. Faults are injected with controller-runtime's
// interceptor client around a real fake-client store, so every test can
// assert both the returned error and the state actually left behind.

const (
	mfNS      = "default"
	mfDepName = "inventory-service"
	mfDepHost = "inventory-service.default.svc.cluster.local"
)

var errBoom = errors.New("etcdserver: request timed out")

func mfScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := cascadev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func mfPolicy(mode cascadev1alpha1.PolicyMode) *cascadev1alpha1.CascadePolicy {
	return &cascadev1alpha1.CascadePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-service", Namespace: mfNS},
		Spec: cascadev1alpha1.CascadePolicySpec{
			Service:   "checkout-service.default.svc.cluster.local",
			DependsOn: []string{mfDepHost},
			Mode:      mode,
		},
	}
}

func mfDR() *networkingv1.DestinationRule {
	return &networkingv1.DestinationRule{
		ObjectMeta: metav1.ObjectMeta{Name: mfDepName, Namespace: mfNS},
		Spec:       apinet.DestinationRule{Host: mfDepHost},
	}
}

func mfVS() *networkingv1.VirtualService {
	return &networkingv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: mfDepName, Namespace: mfNS},
		Spec: apinet.VirtualService{
			Hosts: []string{mfDepHost},
			Http: []*apinet.HTTPRoute{{
				Retries: &apinet.HTTPRetry{Attempts: 3},
				Route:   []*apinet.HTTPRouteDestination{{Destination: &apinet.Destination{Host: mfDepHost}}},
			}},
		},
	}
}

func mfStore(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(mfScheme(t)).WithObjects(objs...).Build()
}

// writeCounter counts mutating calls that reached the interceptor.
type writeCounter struct{ n atomic.Int32 }

func (w *writeCounter) count() int { return int(w.n.Load()) }

// failing returns a client over store whose reads and writes fail
// according to the given per-verb, per-kind rules, plus a counter of
// attempted writes.
type faults struct {
	getErr    func(obj client.Object) error
	updateErr func(obj client.Object) error
	patchErr  func(obj client.Object) error
}

func mfFaulty(store client.WithWatch, f faults) (client.Client, *writeCounter) {
	wc := &writeCounter{}
	return interceptor.NewClient(store, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if f.getErr != nil {
				if err := f.getErr(obj); err != nil {
					return err
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			wc.n.Add(1)
			if f.updateErr != nil {
				if err := f.updateErr(obj); err != nil {
					return err
				}
			}
			return c.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			wc.n.Add(1)
			if f.patchErr != nil {
				if err := f.patchErr(obj); err != nil {
					return err
				}
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	}), wc
}

func isDR(obj client.Object) bool { _, ok := obj.(*networkingv1.DestinationRule); return ok }
func isVS(obj client.Object) bool { _, ok := obj.(*networkingv1.VirtualService); return ok }

func onlyIf(match func(client.Object) bool, err error) func(client.Object) error {
	return func(obj client.Object) error {
		if match(obj) {
			return err
		}
		return nil
	}
}

func mfConflict() error {
	return apierrors.NewConflict(schema.GroupResource{Group: "networking.istio.io", Resource: "destinationrules"},
		mfDepName, errors.New("the object has been modified"))
}

func TestApplyTripFailurePaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		sig       cascadev1alpha1.SignatureType
		mode      cascadev1alpha1.PolicyMode
		host      string
		objs      []client.Object
		faults    faults
		wantErr   string // substring; empty means no error
		wantFound bool
		wantKinds []string
		wantWrite int // attempted writes (Update or Patch)
		checkErr  func(t *testing.T, err error)
	}{
		// --- fan-out amplification: one object kind (DestinationRule) ---
		{
			name: "fan-out: read error is returned and nothing is written",
			sig:  cascadev1alpha1.SignatureFanOutAmplification, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:    []client.Object{mfDR()},
			faults:  faults{getErr: onlyIf(isDR, errBoom)},
			wantErr: "get DestinationRule default/inventory-service",
		},
		{
			name: "fan-out: missing DestinationRule is not an error",
			sig:  cascadev1alpha1.SignatureFanOutAmplification, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
		},
		{
			name: "fan-out: write conflict surfaces as a conflict so the caller requeues",
			sig:  cascadev1alpha1.SignatureFanOutAmplification, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfDR()},
			faults:    faults{updateErr: onlyIf(isDR, mfConflict())},
			wantErr:   "update DestinationRule default/inventory-service",
			wantFound: true, wantWrite: 1,
			checkErr: func(t *testing.T, err error) {
				t.Helper()
				if !apierrors.IsConflict(err) {
					t.Errorf("wrapped error must still satisfy IsConflict, got %v", err)
				}
			},
		},
		{
			name: "fan-out: DetectOnly never writes, even if writes would fail",
			sig:  cascadev1alpha1.SignatureFanOutAmplification, mode: cascadev1alpha1.PolicyModeDetectOnly, host: mfDepHost,
			objs:      []client.Object{mfDR()},
			faults:    faults{updateErr: onlyIf(isDR, errBoom)},
			wantFound: true,
		},
		{
			name: "fan-out: unresolvable host is treated as not found, not an error",
			sig:  cascadev1alpha1.SignatureFanOutAmplification, mode: cascadev1alpha1.PolicyModeMitigate, host: "not-a-service-fqdn",
			objs: []client.Object{mfDR()},
		},

		// --- latency/error cascade: DestinationRule primary + VirtualService secondary ---
		{
			name: "latency: primary read error aborts before any write",
			sig:  cascadev1alpha1.SignatureLatencyErrorCascade, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:    []client.Object{mfDR(), mfVS()},
			faults:  faults{getErr: onlyIf(isDR, errBoom)},
			wantErr: "get DestinationRule",
		},
		{
			name: "latency: primary write failure stops before the secondary is touched",
			sig:  cascadev1alpha1.SignatureLatencyErrorCascade, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfDR(), mfVS()},
			faults:    faults{updateErr: onlyIf(isDR, errBoom)},
			wantErr:   "update DestinationRule",
			wantFound: true, wantWrite: 1,
		},
		{
			name: "latency: secondary read error after the primary landed reports the partial result",
			sig:  cascadev1alpha1.SignatureLatencyErrorCascade, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfDR(), mfVS()},
			faults:    faults{getErr: onlyIf(isVS, errBoom)},
			wantErr:   "get VirtualService",
			wantFound: true, wantKinds: []string{KindDestinationRule}, wantWrite: 1,
		},
		{
			name: "latency: secondary write failure after the primary landed reports the partial result",
			sig:  cascadev1alpha1.SignatureLatencyErrorCascade, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfDR(), mfVS()},
			faults:    faults{updateErr: onlyIf(isVS, errBoom)},
			wantErr:   "update VirtualService",
			wantFound: true, wantKinds: []string{KindDestinationRule}, wantWrite: 2,
		},
		{
			name: "latency: missing secondary is independent of the primary",
			sig:  cascadev1alpha1.SignatureLatencyErrorCascade, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfDR()},
			wantFound: true, wantKinds: []string{KindDestinationRule}, wantWrite: 1,
		},

		// --- retry storm: VirtualService primary + DestinationRule secondary, both via Patch ---
		{
			name: "retry storm: primary read error is returned",
			sig:  cascadev1alpha1.SignatureRetryStorm, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:    []client.Object{mfVS(), mfDR()},
			faults:  faults{getErr: onlyIf(isVS, errBoom)},
			wantErr: "get VirtualService",
		},
		{
			name: "retry storm: primary patch rejected leaves the secondary untouched",
			sig:  cascadev1alpha1.SignatureRetryStorm, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfVS(), mfDR()},
			faults:    faults{patchErr: onlyIf(isVS, errBoom)},
			wantErr:   "patch VirtualService",
			wantFound: true, wantWrite: 1,
		},
		{
			name: "retry storm: secondary patch rejected after the primary landed reports the partial result",
			sig:  cascadev1alpha1.SignatureRetryStorm, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfVS(), mfDR()},
			faults:    faults{patchErr: onlyIf(isDR, errBoom)},
			wantErr:   "patch DestinationRule",
			wantFound: true, wantKinds: []string{KindVirtualService}, wantWrite: 2,
		},
		{
			name: "retry storm: missing primary does not stop the secondary",
			sig:  cascadev1alpha1.SignatureRetryStorm, mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:      []client.Object{mfDR()},
			wantKinds: []string{KindDestinationRule}, wantWrite: 1,
		},

		// --- dispatch ---
		{
			name: "unknown signature is an error, not a silent no-op",
			sig:  cascadev1alpha1.SignatureType("Gremlins"), mode: cascadev1alpha1.PolicyModeMitigate, host: mfDepHost,
			objs:    []client.Object{mfDR()},
			wantErr: "unknown signature",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, writes := mfFaulty(mfStore(t, tc.objs...), tc.faults)
			m := NewMitigator(c)

			outcome, err := m.ApplyTrip(context.Background(), mfPolicy(tc.mode), tc.sig, tc.host)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				if tc.checkErr != nil {
					tc.checkErr(t, err)
				}
			}
			if outcome.PrimaryFound != tc.wantFound {
				t.Errorf("PrimaryFound = %t, want %t", outcome.PrimaryFound, tc.wantFound)
			}
			if !slices.Equal(outcome.AppliedKinds, tc.wantKinds) {
				t.Errorf("AppliedKinds = %v, want %v", outcome.AppliedKinds, tc.wantKinds)
			}
			if writes.count() != tc.wantWrite {
				t.Errorf("attempted writes = %d, want %d", writes.count(), tc.wantWrite)
			}
		})
	}
}

// tripped returns a store in which the latency/error cascade has already
// tripped on the dependency (both objects managed), plus the policy.
func trippedLatencyStore(t *testing.T) client.WithWatch {
	t.Helper()
	store := mfStore(t, mfDR(), mfVS())
	if _, err := NewMitigator(store).ApplyTrip(context.Background(),
		mfPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureLatencyErrorCascade, mfDepHost); err != nil {
		t.Fatalf("setup trip: %v", err)
	}
	return store
}

func managed(t *testing.T, store client.Client, obj client.Object) bool {
	t.Helper()
	if err := store.Get(context.Background(), types.NamespacedName{Name: mfDepName, Namespace: mfNS}, obj); err != nil {
		t.Fatal(err)
	}
	return obj.GetAnnotations()[mitigation.AnnotationManagedBy] == mitigation.ManagedByValue
}

func TestHasManagedEdgesReadErrors(t *testing.T) {
	t.Parallel()

	for _, sig := range []cascadev1alpha1.SignatureType{
		cascadev1alpha1.SignatureFanOutAmplification,
		cascadev1alpha1.SignatureLatencyErrorCascade,
		cascadev1alpha1.SignatureRetryStorm,
	} {
		t.Run(string(sig), func(t *testing.T) {
			t.Parallel()
			c, _ := mfFaulty(trippedLatencyStore(t), faults{getErr: func(client.Object) error { return errBoom }})
			has, err := NewMitigator(c).HasManagedEdges(context.Background(), mfPolicy(cascadev1alpha1.PolicyModeMitigate), sig)
			if err == nil {
				t.Fatal("a failed read must surface as an error, not as 'nothing to restore'")
			}
			if has {
				t.Error("HasManagedEdges must report false alongside an error")
			}
		})
	}

	t.Run("unknown signature", func(t *testing.T) {
		t.Parallel()
		_, err := NewMitigator(mfStore(t)).HasManagedEdges(context.Background(),
			mfPolicy(cascadev1alpha1.PolicyModeMitigate), cascadev1alpha1.SignatureType("Gremlins"))
		if err == nil {
			t.Fatal("want an error for an unknown signature")
		}
	})
}

func TestRestoreFailureLeavesObjectsRetriable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := mfPolicy(cascadev1alpha1.PolicyModeMitigate)
	sig := cascadev1alpha1.SignatureLatencyErrorCascade

	t.Run("complete restore: failed write keeps the managed annotation so the next reconcile retries", func(t *testing.T) {
		t.Parallel()
		store := trippedLatencyStore(t)
		failing, _ := mfFaulty(store, faults{updateErr: onlyIf(isDR, mfConflict())})

		err := NewMitigator(failing).CompleteRestore(ctx, policy, sig)
		if err == nil || !apierrors.IsConflict(err) {
			t.Fatalf("want a wrapped conflict error, got %v", err)
		}
		if !managed(t, store, &networkingv1.DestinationRule{}) {
			t.Fatal("a failed completion must not strip the managed-by annotation: the edge would be orphaned")
		}

		// The requeued reconcile uses a healthy client and finishes the job.
		if err := NewMitigator(store).CompleteRestore(ctx, policy, sig); err != nil {
			t.Fatalf("retry after the transient failure: %v", err)
		}
		if managed(t, store, &networkingv1.DestinationRule{}) || managed(t, store, &networkingv1.VirtualService{}) {
			t.Error("after a successful retry both objects must be released")
		}
	})

	t.Run("complete restore: second object failing does not undo the first, and a retry converges", func(t *testing.T) {
		t.Parallel()
		store := trippedLatencyStore(t)
		failing, _ := mfFaulty(store, faults{updateErr: onlyIf(isVS, errBoom)})

		if err := NewMitigator(failing).CompleteRestore(ctx, policy, sig); err == nil {
			t.Fatal("want an error when the VirtualService write fails")
		}
		if managed(t, store, &networkingv1.DestinationRule{}) {
			t.Error("the DestinationRule completed before the failure and should be released")
		}
		if !managed(t, store, &networkingv1.VirtualService{}) {
			t.Fatal("the VirtualService write failed, so it must still be managed")
		}
		if err := NewMitigator(store).CompleteRestore(ctx, policy, sig); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if managed(t, store, &networkingv1.VirtualService{}) {
			t.Error("retry must release the VirtualService")
		}
	})

	t.Run("restore step: a failed write is surfaced with the edge named", func(t *testing.T) {
		t.Parallel()
		failing, _ := mfFaulty(trippedLatencyStore(t), faults{updateErr: onlyIf(isDR, errBoom)})
		err := NewMitigator(failing).ApplyRestoreStep(ctx, policy, sig, 1)
		if err == nil || !strings.Contains(err.Error(), "update DestinationRule during restore") {
			t.Fatalf("error = %v, want it to name the failed DestinationRule update", err)
		}
	})

	t.Run("restore step: a read error is surfaced", func(t *testing.T) {
		t.Parallel()
		failing, _ := mfFaulty(trippedLatencyStore(t), faults{getErr: func(client.Object) error { return errBoom }})
		if err := NewMitigator(failing).ApplyRestoreStep(ctx, policy, sig, 1); err == nil {
			t.Fatal("want an error when the managed edges cannot be listed")
		}
	})

	t.Run("DetectOnly restore never writes", func(t *testing.T) {
		t.Parallel()
		failing, writes := mfFaulty(trippedLatencyStore(t), faults{
			updateErr: func(client.Object) error { return errBoom },
			patchErr:  func(client.Object) error { return errBoom },
		})
		detectOnly := mfPolicy(cascadev1alpha1.PolicyModeDetectOnly)
		m := NewMitigator(failing)
		if err := m.ApplyRestoreStep(ctx, detectOnly, sig, 2); err != nil {
			t.Fatalf("ApplyRestoreStep: %v", err)
		}
		if err := m.CompleteRestore(ctx, detectOnly, sig); err != nil {
			t.Fatalf("CompleteRestore: %v", err)
		}
		if writes.count() != 0 {
			t.Errorf("DetectOnly attempted %d writes, want 0", writes.count())
		}
	})

	t.Run("unknown signature", func(t *testing.T) {
		t.Parallel()
		m := NewMitigator(mfStore(t))
		bad := cascadev1alpha1.SignatureType("Gremlins")
		if err := m.ApplyRestoreStep(ctx, policy, bad, 1); err == nil {
			t.Error("ApplyRestoreStep: want an error")
		}
		if err := m.CompleteRestore(ctx, policy, bad); err == nil {
			t.Error("CompleteRestore: want an error")
		}
	})
}
