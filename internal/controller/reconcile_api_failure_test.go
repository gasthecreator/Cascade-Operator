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

package controller

import (
	"context"
	"errors"
	"testing"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/mitigation"
)

// Reconcile-level behavior when the API server fails. What matters to an
// operator is not that an error is returned but what state it leaves
// behind, because controller-runtime will call Reconcile again with backoff.
// These tests pin the recovery properties: a failed mitigation or status
// write is reported (so the request is requeued), and the next reconcile
// against a healthy API converges without corrupting the snapshot of the
// pre-trip configuration.

var errAPIDown = errors.New("etcdserver: leader changed")

type apiFaults struct {
	getPolicy    error // Get on a CascadePolicy
	updateDR     error // Update on a DestinationRule
	updateStatus error // Update on the CascadePolicy status subresource
}

// faultyReconciler builds a reconciler whose client injects f over store.
func faultyReconciler(t *testing.T, store client.WithWatch, f apiFaults) *CascadePolicyReconciler {
	t.Helper()
	c := interceptor.NewClient(store, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*cascadev1alpha1.CascadePolicy); ok && f.getPolicy != nil {
				return f.getPolicy
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*networkingv1.DestinationRule); ok && f.updateDR != nil {
				return f.updateDR
			}
			return c.Update(ctx, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, ok := obj.(*cascadev1alpha1.CascadePolicy); ok && sub == "status" && f.updateStatus != nil {
				return f.updateStatus
			}
			return c.SubResource(sub).Update(ctx, obj, opts...)
		},
	})
	return &CascadePolicyReconciler{Client: c, Scheme: store.Scheme(), Metrics: &fakeQuerier{p99: 900, errorRate: 0.2}}
}

func faultStore(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(patchTestScheme(t)).
		WithStatusSubresource(&cascadev1alpha1.CascadePolicy{}).
		WithObjects(objs...).
		Build()
}

var policyKey = types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}

func reconcileOnce(r *CascadePolicyReconciler) error {
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: policyKey})
	return err
}

func getPolicy(t *testing.T, c client.Client) *cascadev1alpha1.CascadePolicy {
	t.Helper()
	p := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(context.Background(), policyKey, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func getDR(t *testing.T, c client.Client) *networkingv1.DestinationRule {
	t.Helper()
	dr := &networkingv1.DestinationRule{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: patchDepName, Namespace: patchPolicyNS}, dr); err != nil {
		t.Fatal(err)
	}
	return dr
}

func TestReconcileReturnsPolicyReadErrors(t *testing.T) {
	t.Parallel()
	store := faultStore(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())

	err := reconcileOnce(faultyReconciler(t, store, apiFaults{getPolicy: errAPIDown}))
	if !errors.Is(err, errAPIDown) {
		t.Fatalf("a failed read of the policy must be returned for requeue, got %v", err)
	}
	if mitigation.IsOperatorManaged(getDR(t, store)) {
		t.Error("nothing may be mitigated when the policy could not be read")
	}
}

func TestReconcileOfDeletedPolicyIsNotAnError(t *testing.T) {
	t.Parallel()
	store := faultStore(t)
	if err := reconcileOnce(faultyReconciler(t, store, apiFaults{})); err != nil {
		t.Fatalf("a policy deleted between event and reconcile must be ignored, got %v", err)
	}
}

func TestReconcileRecoversAfterTransientMitigationFailure(t *testing.T) {
	t.Parallel()
	store := faultStore(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())

	// 1. The DestinationRule write fails: the error is returned so the
	//    request is retried, and the mesh object is not left half-managed.
	err := reconcileOnce(faultyReconciler(t, store, apiFaults{updateDR: errAPIDown}))
	if !errors.Is(err, errAPIDown) {
		t.Fatalf("want the write failure returned for requeue, got %v", err)
	}
	if mitigation.IsOperatorManaged(getDR(t, store)) {
		t.Fatal("a rejected write must not leave the object marked as managed")
	}
	// The trip itself was detected and recorded, so the next reconcile
	// re-applies the same signature instead of starting over.
	if got := getPolicy(t, store).Status; got.Phase != cascadev1alpha1.PolicyPhaseTripped ||
		got.LastSignature != cascadev1alpha1.SignatureLatencyErrorCascade {
		t.Fatalf("status after failed mitigation = %+v, want Tripped/LatencyErrorCascade", got)
	}

	// 2. The API recovers: the very next reconcile converges.
	if err := reconcileOnce(faultyReconciler(t, store, apiFaults{})); err != nil {
		t.Fatalf("reconcile against a healthy API: %v", err)
	}
	dr := getDR(t, store)
	if !mitigation.IsOperatorManaged(dr) {
		t.Fatal("the retry must apply the mitigation")
	}
	if dr.Annotations[mitigation.AnnotationOriginalOutlier] == "" {
		t.Error("the retry must have captured the pre-trip configuration")
	}
}

func TestReconcileStatusWriteFailureDoesNotCorruptTheOriginalSnapshot(t *testing.T) {
	t.Parallel()
	store := faultStore(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())

	// The mesh object is patched, then the status write fails.
	err := reconcileOnce(faultyReconciler(t, store, apiFaults{updateStatus: errAPIDown}))
	if !errors.Is(err, errAPIDown) {
		t.Fatalf("want the status failure returned for requeue, got %v", err)
	}
	first := getDR(t, store)
	if !mitigation.IsOperatorManaged(first) {
		t.Fatal("setup: the mitigation should have landed before the status write failed")
	}
	snapshot := first.Annotations[mitigation.AnnotationOriginalOutlier]
	if snapshot == "" {
		t.Fatal("setup: no original snapshot captured")
	}
	if got := getPolicy(t, store).Status.Phase; got == cascadev1alpha1.PolicyPhaseTripped {
		t.Fatal("setup: status must not have been persisted")
	}

	// Requeue against a healthy API: the trip is re-applied. It must not
	// overwrite the snapshot with the already-tripped values, or the
	// restore would later "restore" to the tripped configuration.
	if err := reconcileOnce(faultyReconciler(t, store, apiFaults{})); err != nil {
		t.Fatalf("retry: %v", err)
	}
	second := getDR(t, store)
	if got := second.Annotations[mitigation.AnnotationOriginalOutlier]; got != snapshot {
		t.Errorf("original snapshot changed on retry:\n before %s\n after  %s", snapshot, got)
	}
	if got := getPolicy(t, store).Status.Phase; got != cascadev1alpha1.PolicyPhaseTripped {
		t.Errorf("phase after the retry = %q, want Tripped", got)
	}
}

func TestReconcileConflictOnMitigationIsRetriable(t *testing.T) {
	t.Parallel()
	store := faultStore(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
	conflict := apierrors.NewConflict(
		networkingv1.Resource("destinationrules"), patchDepName, errors.New("the object has been modified"))

	err := reconcileOnce(faultyReconciler(t, store, apiFaults{updateDR: conflict}))
	if !apierrors.IsConflict(err) {
		t.Fatalf("a write conflict must reach controller-runtime as a conflict, got %v", err)
	}
	if err := reconcileOnce(faultyReconciler(t, store, apiFaults{})); err != nil {
		t.Fatalf("retry after a conflict: %v", err)
	}
	if !mitigation.IsOperatorManaged(getDR(t, store)) {
		t.Error("the retry after a conflict must apply the mitigation")
	}
}
