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

package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

const (
	testPaymentsFQDN = "payments-service.default.svc.cluster.local"
	testNamespace    = "default"
)

// validPolicy returns a CascadePolicy that passes every check
// validateCascadePolicy performs — tests mutate a copy of this rather than
// building one field at a time, so each test only shows the one thing it's
// actually checking.
func validPolicy() *cascadev1alpha1.CascadePolicy {
	return &cascadev1alpha1.CascadePolicy{
		Spec: cascadev1alpha1.CascadePolicySpec{
			Service: "checkout-service.default.svc.cluster.local",
			DependsOn: []string{
				testPaymentsFQDN,
				"inventory-service.default.svc.cluster.local",
			},
			Thresholds: cascadev1alpha1.Thresholds{
				LatencyP99Ms:         500,
				ErrorRateFraction:    0.05,
				WindowSeconds:        30,
				RetryStormMultiplier: 3,
				FanOutMultiplier:     2,
			},
			Mode: cascadev1alpha1.PolicyModeMitigate,
		},
	}
}

var _ = Describe("CascadePolicy Webhook", func() {
	var (
		ctx       context.Context
		validator CascadePolicyCustomValidator
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = CascadePolicyCustomValidator{}
	})

	Context("When creating or updating CascadePolicy under Validating Webhook", func() {
		It("admits a well-formed policy", func() {
			_, err := validator.ValidateCreate(ctx, validPolicy())
			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a service that isn't a plausible Service FQDN", func() {
			obj := validPolicy()
			obj.Spec.Service = "checkout-service"
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.service"))
		})

		It("rejects a dependsOn entry that isn't a plausible Service FQDN", func() {
			obj := validPolicy()
			obj.Spec.DependsOn = []string{"payments-service"}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.dependsOn"))
		})

		It("rejects a policy that depends on itself", func() {
			obj := validPolicy()
			obj.Spec.DependsOn = append(obj.Spec.DependsOn, obj.Spec.Service)
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must not equal spec.service"))
		})

		It("rejects duplicate dependsOn entries", func() {
			obj := validPolicy()
			obj.Spec.DependsOn = []string{
				testPaymentsFQDN,
				testPaymentsFQDN,
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Duplicate"))
		})

		It("reports every violation at once, not just the first", func() {
			obj := validPolicy()
			obj.Spec.Service = "not-an-fqdn"
			obj.Spec.DependsOn = []string{"also-not-an-fqdn", "also-not-an-fqdn"}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			// One malformed service + one malformed dependsOn entry + one
			// duplicate dependsOn entry = 3 distinct field errors.
			Expect(err.Error()).To(ContainSubstring("spec.service"))
			Expect(err.Error()).To(ContainSubstring("spec.dependsOn[0]"))
			Expect(err.Error()).To(ContainSubstring("spec.dependsOn[1]"))
		})

		It("runs the same checks on update, against the new object", func() {
			oldObj := validPolicy()
			newObj := validPolicy()
			newObj.Spec.DependsOn = []string{"payments-service"}
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.dependsOn"))
		})

		It("admits deletion unconditionally", func() {
			_, err := validator.ValidateDelete(ctx, validPolicy())
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// The Context above calls CascadePolicyCustomValidator's methods
	// directly — that proves the validation logic is correct but never
	// exercises the actual admission path (webhook registration, TLS,
	// path/resource matching against the ValidatingWebhookConfiguration
	// this package's own +kubebuilder:webhook marker generates). This
	// suite's BeforeSuite (webhook_suite_test.go) starts a real envtest
	// API server with that webhook actually wired in, specifically so it
	// can be exercised this way instead of only unit-tested.
	Context("When creating CascadePolicy through the real admission path", func() {
		It("is actually rejected by the live webhook, not just the Go function", func() {
			obj := validPolicy()
			obj.Name = "webhook-live-reject"
			obj.Namespace = testNamespace
			obj.Spec.DependsOn = []string{"not-an-fqdn"}

			err := k8sClient.Create(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("spec.dependsOn"))
		})

		It("is actually admitted and persisted by the live webhook", func() {
			obj := validPolicy()
			obj.Name = "webhook-live-admit"
			obj.Namespace = testNamespace

			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
			})

			fetched := &cascadev1alpha1.CascadePolicy{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), fetched)).To(Succeed())
			Expect(fetched.Spec.Service).To(Equal(obj.Spec.Service))
		})

		It("is actually rejected on update through the live webhook", func() {
			obj := validPolicy()
			obj.Name = "webhook-live-reject-update"
			obj.Namespace = testNamespace
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
			})

			obj.Spec.DependsOn = append(obj.Spec.DependsOn, obj.Spec.Service)
			err := k8sClient.Update(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("must not equal spec.service"))
		})
	})
})
