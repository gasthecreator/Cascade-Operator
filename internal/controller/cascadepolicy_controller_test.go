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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

// fakeQuerier returns canned p99 / error-rate / dest:source-ratio samples. It
// implements metrics.Querier without HTTP or Prometheus.
type fakeQuerier struct {
	p99             float64
	errorRate       float64
	retryStormRatio float64
	err             error
}

func (f *fakeQuerier) Query(_ context.Context, promql string) (metrics.Snapshot, error) {
	if f.err != nil {
		return metrics.Snapshot{}, f.err
	}
	v := f.errorRate
	switch {
	case strings.Contains(promql, "histogram_quantile"):
		v = f.p99
	case strings.Contains(promql, `reporter="destination"`):
		v = f.retryStormRatio
	}
	return metrics.Snapshot{Samples: []metrics.Sample{{Value: v}}}, nil
}

var _ = Describe("CascadePolicy Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "checkout-service"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind CascadePolicy")
			cascadepolicy := &cascadev1alpha1.CascadePolicy{}
			err := k8sClient.Get(ctx, typeNamespacedName, cascadepolicy)
			if err != nil && errors.IsNotFound(err) {
				resource := &cascadev1alpha1.CascadePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: cascadev1alpha1.CascadePolicySpec{
						Service: "checkout-service.default.svc.cluster.local",
						DependsOn: []string{
							"payments-service.default.svc.cluster.local",
							"inventory-service.default.svc.cluster.local",
						},
						Thresholds: cascadev1alpha1.Thresholds{
							LatencyP99Ms:         500,
							ErrorRateFraction:    0.05,
							WindowSeconds:        30,
							RetryStormMultiplier: 3.0,
							FanOutMultiplier:     5.0,
						},
						Mode: cascadev1alpha1.PolicyModeMitigate,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &cascadev1alpha1.CascadePolicy{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance CascadePolicy")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should requeue and set phase Normal when Metrics is nil", func() {
			controllerReconciler := &CascadePolicyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(DefaultRequeueAfter))

			updated := &cascadev1alpha1.CascadePolicy{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(cascadev1alpha1.PolicyPhaseNormal))
			Expect(updated.Status.LastSignature).To(BeEmpty())
		})

		It("should stay Normal when readings are under threshold", func() {
			controllerReconciler := &CascadePolicyReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				Metrics: &fakeQuerier{p99: 80, errorRate: 0.001, retryStormRatio: 1.0},
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(DefaultRequeueAfter))

			updated := &cascadev1alpha1.CascadePolicy{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(cascadev1alpha1.PolicyPhaseNormal))
			Expect(updated.Status.LastSignature).To(BeEmpty())
		})

		It("should trip LatencyErrorCascade when both signals exceed threshold", func() {
			controllerReconciler := &CascadePolicyReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				Metrics: &fakeQuerier{p99: 900, errorRate: 0.2},
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(DefaultRequeueAfter))

			updated := &cascadev1alpha1.CascadePolicy{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(cascadev1alpha1.PolicyPhaseTripped))
			Expect(updated.Status.LastSignature).To(Equal(cascadev1alpha1.SignatureLatencyErrorCascade))
			Expect(updated.Status.LastTrippedAt).NotTo(BeNil())

			cond := meta.FindStatusCondition(updated.Status.Conditions, cascadev1alpha1.ConditionTypeDependencyObjectMissing)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should not fail reconcile when Query returns an error", func() {
			controllerReconciler := &CascadePolicyReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				Metrics: &fakeQuerier{err: fmt.Errorf("prometheus unavailable")},
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(DefaultRequeueAfter))

			updated := &cascadev1alpha1.CascadePolicy{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(cascadev1alpha1.PolicyPhaseNormal))
		})
	})
})
