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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/notify"
)

// recordingNotifier is a Notifier stub that records every call instead of
// sending anything, so tests can assert Reconcile actually reaches the
// notify call sites rather than trusting they're wired by inspection alone.
type recordingNotifier struct {
	trips    []notify.TripEvent
	restores []notify.RestoreEvent
	tripErr  error
}

func (n *recordingNotifier) NotifyTrip(_ context.Context, e notify.TripEvent) error {
	n.trips = append(n.trips, e)
	return n.tripErr
}

func (n *recordingNotifier) NotifyRestore(_ context.Context, e notify.RestoreEvent) error {
	n.restores = append(n.restores, e)
	return nil
}

var _ notify.Notifier = (*recordingNotifier)(nil)

func TestReconcileSendsTripNotification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
	n := &recordingNotifier{}
	r.Notify = n

	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(n.trips) != 1 {
		t.Fatalf("trips recorded = %d, want 1: %+v", len(n.trips), n.trips)
	}
	got := n.trips[0]
	if got.PolicyName != patchPolicyName || got.PolicyNamespace != patchPolicyNS {
		t.Errorf("trip event policy = %s/%s, want %s/%s", got.PolicyNamespace, got.PolicyName, patchPolicyNS, patchPolicyName)
	}
	if got.Signature != string(cascadev1alpha1.SignatureLatencyErrorCascade) {
		t.Errorf("trip event signature = %s, want %s", got.Signature, cascadev1alpha1.SignatureLatencyErrorCascade)
	}
	if got.Dependency != patchDepHost {
		t.Errorf("trip event dependency = %s, want %s", got.Dependency, patchDepHost)
	}
}

func TestReconcileSendsRestoreNotificationOnceRampCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
	n := &recordingNotifier{}
	r.Notify = n
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("trip reconcile: %v", err)
	}
	if len(n.restores) != 0 {
		t.Fatalf("restores recorded before healing = %d, want 0", len(n.restores))
	}

	r.Metrics = &fakeQuerier{p99: 1, errorRate: 0}
	for i := range 6 {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("restore reconcile %d: %v", i, err)
		}
	}

	if len(n.restores) != 1 {
		t.Fatalf("restores recorded = %d, want 1: %+v", len(n.restores), n.restores)
	}
	got := n.restores[0]
	if got.Signature != string(cascadev1alpha1.SignatureLatencyErrorCascade) {
		t.Errorf("restore event signature = %s, want %s", got.Signature, cascadev1alpha1.SignatureLatencyErrorCascade)
	}
	if got.PolicyName != patchPolicyName || got.PolicyNamespace != patchPolicyNS {
		t.Errorf("restore event policy = %s/%s, want %s/%s", got.PolicyNamespace, got.PolicyName, patchPolicyNS, patchPolicyName)
	}
}

func TestReconcileDoesNotFailWhenNotifyErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
	r.Notify = &recordingNotifier{tripErr: errors.New("webhook unreachable")}

	// A notification failure must never surface as a reconcile error — it's
	// observability, not part of the mitigation correctness path.
	if _, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	}); err != nil {
		t.Fatalf("Reconcile returned an error from a failed notification: %v", err)
	}
}

func TestReconcileWithNilNotifierDoesNotPanic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := patchReconcile(t, patchTestPolicy(cascadev1alpha1.PolicyModeMitigate), patchTestDR())
	// r.Notify is nil (the default, matching no --notify-webhook-url).
	if _, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: patchPolicyName, Namespace: patchPolicyNS},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}
